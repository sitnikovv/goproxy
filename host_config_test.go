package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHConfigContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hosts    []hostConfig
		want     []string
		includes int
		wantErr  string
	}{
		{
			name: "secure host",
			hosts: []hostConfig{
				{host: "git.example.com", ip: "192.0.2.10"},
			},
			want: []string{
				"Host git.example.com\n",
				"  HostName 192.0.2.10\n",
				"  HostKeyAlias git.example.com\n",
				"  Include ~/.ssh/config\n",
				"Host *\n",
				"  StrictHostKeyChecking accept-new\n",
			},
		},
		{
			name: "multiple hosts",
			hosts: []hostConfig{
				{host: "git-a.example.com", ip: "192.0.2.10"},
				{host: "git-b.example.com", ip: "192.0.2.11"},
			},
			want: []string{
				"Host git-a.example.com\n",
				"  HostName 192.0.2.10\n",
				"Host git-b.example.com\n",
				"  HostName 192.0.2.11\n",
				"  Include ~/.ssh/config\n\nHost *\n",
			},
			includes: 2,
		},
		{
			name: "insecure host",
			hosts: []hostConfig{
				{host: "git.example.com", ip: "192.0.2.10", insecure: true},
			},
			want: []string{
				"  StrictHostKeyChecking no\n",
				"  UserKnownHostsFile /dev/null\n",
				"  CheckHostIP no\n",
			},
		},
		{
			name: "bad host",
			hosts: []hostConfig{
				{host: "git.example.com bad", ip: "192.0.2.10"},
			},
			wantErr: "bad host",
		},
		{
			name: "wildcard host",
			hosts: []hostConfig{
				{host: "*.example.com", ip: "192.0.2.10"},
			},
			wantErr: "bad host",
		},
		{
			name: "bad ip",
			hosts: []hostConfig{
				{host: "git.example.com", ip: "not-an-ip"},
			},
			wantErr: "bad ip",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := sshConfigContent(test.hosts)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("sshConfigContent() error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("sshConfigContent() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Fatalf("sshConfigContent() = %q, want substring %q", got, want)
				}
			}
			if test.includes > 0 {
				count := strings.Count(got, "  Include ~/.ssh/config\n")
				if count != test.includes {
					t.Fatalf("sshConfigContent() has %d Include lines, want %d", count, test.includes)
				}
			}
		})
	}
}

func TestPrepareGitSSHCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		gitSSHCommand string
		hosts         []hostConfig
		wantPrefix    string
		wantFile      bool
	}{
		{
			name:          "no hosts",
			gitSSHCommand: "ssh -i /key",
			wantPrefix:    "ssh -i /key",
		},
		{
			name:          "default command",
			gitSSHCommand: defaultGitSSHCommand,
			hosts: []hostConfig{
				{host: "git.example.com", ip: "192.0.2.10"},
			},
			wantPrefix: "ssh -F ",
			wantFile:   true,
		},
		{
			name:          "custom command",
			gitSSHCommand: "ssh -i /key",
			hosts: []hostConfig{
				{host: "git.example.com", ip: "192.0.2.10"},
			},
			wantPrefix: "ssh -i /key -F ",
			wantFile:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			got, err := prepareGitSSHCommand(cacheDir, test.gitSSHCommand, test.hosts)
			if err != nil {
				t.Fatalf("prepareGitSSHCommand() error = %v", err)
			}
			if !strings.HasPrefix(got, test.wantPrefix) {
				t.Fatalf("prepareGitSSHCommand() = %q, want prefix %q", got, test.wantPrefix)
			}
			if test.wantFile && !strings.Contains(got, filepath.Join(cacheDir, "ssh_config")) {
				t.Fatalf("prepareGitSSHCommand() = %q, want absolute ssh_config path under %q", got, cacheDir)
			}
			_, err = os.Stat(filepath.Join(cacheDir, "ssh_config"))
			if test.wantFile && err != nil {
				t.Fatalf("ssh_config was not written: %v", err)
			}
			if !test.wantFile && !os.IsNotExist(err) {
				t.Fatalf("ssh_config exists for case without hosts")
			}
		})
	}
}
