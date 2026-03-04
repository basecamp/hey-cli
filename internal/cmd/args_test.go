package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestUsageExactArgs(t *testing.T) {
	root := &cobra.Command{Use: "hey"}
	cmd := &cobra.Command{Use: "topic <id>"}
	root.AddCommand(cmd)

	validator := usageExactArgs(1)

	if err := validator(cmd, []string{"123"}); err != nil {
		t.Fatalf("expected nil error for valid args, got %v", err)
	}

	err := validator(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}
	if !strings.Contains(err.Error(), "Usage: hey topic <id>") {
		t.Fatalf("unexpected missing-args error: %q", err.Error())
	}

	err = validator(cmd, []string{"123", "456"})
	if err == nil {
		t.Fatal("expected error for extra args, got nil")
	}
	if err.Error() != "expected 1 argument, got 2" {
		t.Fatalf("unexpected extra-args error: %q", err.Error())
	}
}

func TestCleanUseLineStripsFlagsSuffix(t *testing.T) {
	line := cleanUseLine("hey recordings <calendar-id> [flags]")
	if line != "hey recordings <calendar-id>" {
		t.Fatalf("cleanUseLine() = %q", line)
	}
}
