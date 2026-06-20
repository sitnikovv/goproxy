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
		prefixes []prefixMapping
		want     []string
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
				"Host *\n",
			},
		},
		{
			name: "keyed prefix",
			hosts: []hostConfig{
				{host: "git.example.com", ip: "192.0.2.10"},
			},
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project",
					keyFile:      "project-key",
				},
			},
			want: []string{
				"Host goproxy-prefix-1\n",
				"  HostName 192.0.2.10\n",
				"  User git\n",
				"  Port 7999\n",
				"  HostKeyAlias git.example.com\n",
				"  IdentityFile /home/goproxy/.ssh/project-key\n",
				"  IdentitiesOnly yes\n",
			},
		},
		{
			name: "multiple keyed prefixes on same host",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project-a",
					sshPrefix:    "ssh://git@git.example.com:7999/project-a",
					keyFile:      "project-a-key",
				},
				{
					modulePrefix: "example.com/project-b",
					sshPrefix:    "ssh://git@git.example.com:7999/project-b",
					keyFile:      "project-b-key",
				},
			},
			want: []string{
				"Host goproxy-prefix-1\n",
				"  IdentityFile /home/goproxy/.ssh/project-a-key\n",
				"Host goproxy-prefix-2\n",
				"  IdentityFile /home/goproxy/.ssh/project-b-key\n",
			},
		},
		{
			name: "keyed prefix without user",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git.example.com:7999/project",
					keyFile:      "project-key",
				},
			},
			want: []string{
				"Host goproxy-prefix-1\n",
				"  HostName git.example.com\n",
				"  Port 7999\n",
				"  IdentityFile /home/goproxy/.ssh/project-key\n",
			},
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
		{
			name: "bad key file",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project",
					keyFile:      "../project-key",
				},
			},
			wantErr: "bad key_file",
		},
		{
			name: "bad keyed ssh prefix",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "https://git.example.com/project",
					keyFile:      "project-key",
				},
			},
			wantErr: "requires ssh_prefix",
		},
		{
			name: "keyed ssh prefix with query",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project?bad=true",
					keyFile:      "project-key",
				},
			},
			wantErr: "query or fragment",
		},
		{
			name: "keyed ssh prefix with empty query",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project?",
					keyFile:      "project-key",
				},
			},
			wantErr: "query or fragment",
		},
		{
			name: "keyed ssh prefix with fragment",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project#bad",
					keyFile:      "project-key",
				},
			},
			wantErr: "query or fragment",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := sshConfigContent(test.hosts, test.prefixes)
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
		})
	}
}

func TestPrepareGitSSHCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		gitSSHCommand string
		hosts         []hostConfig
		prefixes      []prefixMapping
		wantPrefix    string
		wantFile      bool
		wantSSHPrefix string
		wantErr       string
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
		{
			name:          "keyed prefix",
			gitSSHCommand: defaultGitSSHCommand,
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project",
					keyFile:      "project-key",
				},
			},
			wantPrefix:    "ssh -F ",
			wantFile:      true,
			wantSSHPrefix: "ssh://git@goproxy-prefix-1:7999/project",
		},
		{
			name:          "keyed prefix with custom command",
			gitSSHCommand: "ssh -o ConnectTimeout=10",
			prefixes: []prefixMapping{
				{
					modulePrefix: "example.com/project",
					sshPrefix:    "ssh://git@git.example.com:7999/project",
					keyFile:      "project-key",
				},
			},
			wantErr: "custom GIT_SSH_COMMAND",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			gotPrefixes, got, err := prepareGitSSHCommand(cacheDir, test.gitSSHCommand, test.hosts, test.prefixes)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("prepareGitSSHCommand() error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
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
			if test.wantSSHPrefix != "" {
				if len(gotPrefixes) != 1 {
					t.Fatalf("prepareGitSSHCommand() returned %d prefixes, want 1", len(gotPrefixes))
				}
				if gotPrefixes[0].sshPrefix != test.wantSSHPrefix {
					t.Fatalf("prepared sshPrefix = %q, want %q", gotPrefixes[0].sshPrefix, test.wantSSHPrefix)
				}
			}
		})
	}
}
