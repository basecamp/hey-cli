package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootRegistersJQFlag(t *testing.T) {
	flag := newRootCmd().PersistentFlags().Lookup("jq")
	if flag == nil {
		t.Fatal("expected --jq flag")
	}
	if flag.Usage != "Filter JSON with a built-in jq expression" {
		t.Errorf("unexpected --jq help: %q", flag.Usage)
	}
}

func TestValidateJQFlags(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		filter string
		ids    bool
		count  bool
		want   string
	}{
		{name: "empty", args: []string{"auth", "status"}},
		{name: "valid", args: []string{"auth", "status"}, filter: ".data[].id"},
		{name: "invalid", args: []string{"auth", "status"}, filter: ".[invalid", want: "invalid --jq expression"},
		{name: "ids conflict", args: []string{"auth", "status"}, filter: ".data", ids: true, want: "cannot use --jq with --ids-only"},
		{name: "count conflict", args: []string{"auth", "status"}, filter: ".data", count: true, want: "cannot use --jq with --count"},
		{name: "root app", filter: ".", want: "--jq is not supported by the interactive app"},
		{name: "auth token", args: []string{"auth", "token"}, filter: ".", want: "--jq is not supported by the auth token command"},
		{name: "completion", args: []string{"completion"}, filter: ".", want: "--jq is not supported by the completion command"},
		{name: "skill display", args: []string{"skill"}, filter: ".", want: "--jq is not supported by the skill display command"},
		{name: "tui", args: []string{"tui"}, filter: ".", want: "--jq is not supported by the interactive app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCmd()
			cmd, _, err := root.Find(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			err = validateJQFlags(cmd, tt.filter, tt.ids, tt.count)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestRootVersionSupportsRawOutputAndRejectsJQ(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		root := newRootCmd()
		var stdout bytes.Buffer
		root.SetOut(&stdout)
		root.SetArgs([]string{"--version"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stdout.String(), "hey version ") {
			t.Errorf("unexpected version output: %q", stdout.String())
		}
	})

	t.Run("jq", func(t *testing.T) {
		root := newRootCmd()
		root.SetArgs([]string{"--version", "--jq", "."})
		err := root.Execute()
		if err == nil || err.Error() != "--jq is not supported by the version command" {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
