package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestUsageExactArgs(t *testing.T) {
	root := &cobra.Command{Use: "hey"}
	cmd := &cobra.Command{Use: "threads <id>"}
	root.AddCommand(cmd)

	validator := usageExactOneArg()

	if err := validator(cmd, []string{"123"}); err != nil {
		t.Fatalf("expected nil error for valid args, got %v", err)
	}

	err := validator(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}
	if !strings.Contains(err.Error(), "Usage: hey threads <id>") {
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

func TestUsageExactArgsCountsWhatItWasGiven(t *testing.T) {
	root := &cobra.Command{Use: "hey"}
	cmd := &cobra.Command{Use: "rename <id> <title>"}
	root.AddCommand(cmd)

	validator := usageExactArgs(2)

	if err := validator(cmd, []string{"123", "Planning"}); err != nil {
		t.Fatalf("expected nil error for valid args, got %v", err)
	}

	err := validator(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "Usage: hey rename <id> <title>") {
		t.Fatalf("unexpected missing-args error: %v", err)
	}

	err = validator(cmd, []string{"123", "Planning", "Q3"})
	if err == nil || err.Error() != "expected 2 arguments, got 3" {
		t.Fatalf("unexpected extra-args error: %v", err)
	}
}

func TestUsageMinArgs(t *testing.T) {
	root := &cobra.Command{Use: "hey"}
	cmd := &cobra.Command{Use: "seen <id>..."}
	root.AddCommand(cmd)

	validator := usageMinArgs(2)

	if err := validator(cmd, []string{"1", "2", "3"}); err != nil {
		t.Fatalf("expected nil error for enough args, got %v", err)
	}

	err := validator(cmd, []string{"1"})
	if err == nil || !strings.Contains(err.Error(), "Usage: hey seen <id>...") {
		t.Fatalf("unexpected too-few-args error: %v", err)
	}
}

func TestCleanUseLineStripsFlagsSuffix(t *testing.T) {
	line := cleanUseLine("hey recordings <calendar-id> [flags]")
	if line != "hey recordings <calendar-id>" {
		t.Fatalf("cleanUseLine() = %q", line)
	}
}
