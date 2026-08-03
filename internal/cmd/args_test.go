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
	for _, tc := range []struct{ arg, want string }{
		{"0", "invalid posting ID: 0 (must be positive)"},
		{"-1", "invalid posting ID: -1 (must be positive)"},
		{"-99999", "invalid posting ID: -99999 (must be positive)"},
	} {
		_, err := parseIntArgs([]string{tc.arg})
		if err == nil {
			t.Errorf("parseIntArgs(%q): expected an error, got nil", tc.arg)
			continue
		}
		// The clearer message is the point of the change, so assert it rather
		// than just the presence of an error.
		if err.Error() != tc.want {
			t.Errorf("parseIntArgs(%q) = %q, want %q", tc.arg, err.Error(), tc.want)
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
