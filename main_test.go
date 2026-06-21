package main

import (
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
