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
		name      string
		args      []string
		filter    string
		requested bool
		ids       bool
		count     bool
		want      string
	}{
		{name: "absent", args: []string{"auth", "status"}},
		{name: "empty", args: []string{"auth", "status"}, requested: true, want: "invalid --jq expression: expression cannot be empty"},
		{name: "valid", args: []string{"auth", "status"}, filter: ".data[].id", requested: true},
		{name: "invalid", args: []string{"auth", "status"}, filter: ".[invalid", requested: true, want: "invalid --jq expression"},
		{name: "ids conflict", args: []string{"auth", "status"}, filter: ".data", requested: true, ids: true, want: "cannot use --jq with --ids-only"},
		{name: "count conflict", args: []string{"auth", "status"}, filter: ".data", requested: true, count: true, want: "cannot use --jq with --count"},
		{name: "root help", filter: ".", requested: true},
		{name: "auth token", args: []string{"auth", "token"}, filter: ".", requested: true, want: "--jq is not supported by the auth token command"},
		{name: "completion", args: []string{"completion"}, filter: ".", requested: true, want: "--jq is not supported by the completion command"},
		{name: "skill display", args: []string{"skill"}, filter: ".", requested: true, want: "--jq is not supported by the skill display command"},
		{name: "tui", args: []string{"tui"}, filter: ".", requested: true, want: "--jq is not supported by the interactive app"},
		{name: "hey alias", args: []string{"hey"}, filter: ".", requested: true, want: "--jq is not supported by the interactive app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCmd()
			cmd, _, err := root.Find(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			err = validateJQFlags(cmd, tt.filter, tt.requested, tt.ids, tt.count)
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

func TestRootRejectsExplicitEmptyJQExpression(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"auth", "status", "--jq="})
	err := root.Execute()
	if err == nil || err.Error() != "invalid --jq expression: expression cannot be empty" {
		t.Fatalf("unexpected error: %v", err)
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
