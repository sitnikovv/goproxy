package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteModContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		modulePath string
		want       []string
	}{
		{
			name:       "rehosted module",
			content:    "module source.example.com/old/module\n\ngo 1.20\n\nrequire example.com/dep v1.2.3\n",
			modulePath: "modules.example.com/project/new-module/v2",
			want: []string{
				"module modules.example.com/project/new-module/v2\n",
				"go 1.20\n",
				"require example.com/dep v1.2.3\n",
			},
		},
		{
			name:       "missing module statement",
			content:    "go 1.22\n",
			modulePath: "modules.example.com/project/sample",
			want: []string{
				"module modules.example.com/project/sample\n",
				"go 1.22\n",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := rewriteModContent([]byte(test.content), test.modulePath)
			if err != nil {
				t.Fatalf("rewriteModContent() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(string(got), want) {
					t.Fatalf("rewriteModContent() = %q, want substring %q", got, want)
				}
			}
		})
	}
}

func TestResolveModuleVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modulePath string
		version    string
		files      map[string]string
		tags       []string
		want       string
	}{
		{
			name:       "major subdirectory with go mod",
			modulePath: "modules.example.com/project/sample/v3",
			version:    "v3.0.0",
			files: map[string]string{
				"README.md": "sample\n",
				"v3/go.mod": "module source.example.com/sample/v3\n\ngo 1.22\n",
				"v3/lib.go": "package sample\n",
			},
			tags: []string{"v3/v3.0.0"},
			want: "v3",
		},
		{
			name:       "major suffix without subdirectory go mod",
			modulePath: "modules.example.com/project/sample/v2",
			version:    "v2.0.0",
			files: map[string]string{
				"go.mod": "module source.example.com/sample\n\ngo 1.22\n",
				"lib.go": "package sample\n",
			},
			tags: []string{"v2.0.0"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repoDir := t.TempDir()
			initTestRepo(t, repoDir, test.files)
			for _, tag := range test.tags {
				runTestGit(t, repoDir, "tag", tag)
			}

			_, got, err := resolveModuleVersion(context.Background(), repoDir, test.modulePath, "", test.version)
			if err != nil {
				t.Fatalf("resolveModuleVersion() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveModuleVersion() subdir = %q, want %q", got, test.want)
			}
		})
	}
}

func initTestRepo(t *testing.T, repoDir string, files map[string]string) {
	t.Helper()

	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	for path, content := range files {
		fullPath := filepath.Join(repoDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	runTestGit(t, repoDir, "add", ".")
	runTestGit(t, repoDir, "commit", "-m", "initial")
}

func runTestGit(t *testing.T, repoDir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}
