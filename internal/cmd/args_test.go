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

func TestCleanUseLineStripsFlagsSuffix(t *testing.T) {
	line := cleanUseLine("hey recordings <calendar-id> [flags]")
	if line != "hey recordings <calendar-id>" {
		t.Fatalf("cleanUseLine() = %q", line)
	}
}

func TestParseIntArgsRejectsNonPositive(t *testing.T) {
	for _, arg := range []string{"0", "-1", "-99999"} {
		if _, err := parseIntArgs([]string{arg}); err == nil {
			t.Errorf("parseIntArgs(%q): expected an error, got nil", arg)
		}
	}
}

func TestParseIntArgsRejectsNonNumeric(t *testing.T) {
	if _, err := parseIntArgs([]string{"abc"}); err == nil {
		t.Error("parseIntArgs(\"abc\"): expected an error, got nil")
	}
}

func TestParseIntArgsDeduplicatesPreservingOrder(t *testing.T) {
	got, err := parseIntArgs([]string{"3", "1", "3", "2", "1"})
	if err != nil {
		t.Fatalf("parseIntArgs: %v", err)
	}
	want := []int64{3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (first occurrence order preserved)", got, want)
		}
	}
}

func TestParseIntArgsAcceptsValidIDs(t *testing.T) {
	got, err := parseIntArgs([]string{"12345", "67890"})
	if err != nil {
		t.Fatalf("parseIntArgs: %v", err)
	}
	if len(got) != 2 || got[0] != 12345 || got[1] != 67890 {
		t.Errorf("got %v, want [12345 67890]", got)
	}
}
