package smoke_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestJSONOutput(t *testing.T) {
	stdout := heyOK(t, "boxes", "--json")

	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("--json output is not valid JSON: %v", err)
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if resp.Data == nil {
		t.Error("expected non-nil data")
	}
}

func TestQuietOutput(t *testing.T) {
	stdout := heyOK(t, "boxes", "--quiet")

	// Quiet mode outputs raw JSON data without the envelope.
	var data []any
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		t.Fatalf("--quiet output is not valid JSON array: %v\nraw: %s", err, stdout)
	}
}

func TestIDsOnlyOutput(t *testing.T) {
	stdout := heyOK(t, "boxes", "--ids-only")
	stdout = strings.TrimSpace(stdout)

	if stdout == "" {
		t.Fatal("no boxes to test --ids-only")
	}

	lines := strings.Split(stdout, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := strconv.Atoi(line); err != nil {
			t.Errorf("--ids-only line is not a number: %q", line)
		}
	}
}

func TestCountOutput(t *testing.T) {
	stdout := heyOK(t, "boxes", "--count")
	stdout = strings.TrimSpace(stdout)

	if _, err := strconv.Atoi(stdout); err != nil {
		t.Errorf("--count output is not a number: %q", stdout)
	}
}

func TestMarkdownOutput(t *testing.T) {
	stdout := heyOK(t, "boxes", "--markdown")

	// Markdown table should have pipe-separated header and separator rows.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines for markdown table, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "|") {
		t.Errorf("first line should start with '|', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "---") {
		t.Errorf("second line should contain '---', got: %s", lines[1])
	}
}

func TestStyledOutput(t *testing.T) {
	// --styled forces styled output even when piped.
	stdout := heyOK(t, "boxes", "--styled")
	// Styled output has a table with ID/Kind/Name headers.
	assertContains(t, stdout, "ID")
	assertContains(t, stdout, "Kind")
	assertContains(t, stdout, "Name")
}

func TestVerboseFlag(t *testing.T) {
	_, stderr, code := hey(t, "boxes", "-v", "--json")
	if code != 0 {
		t.Fatalf("hey boxes -v failed with exit %d", code)
	}
	// Verbose mode logs request details to stderr.
	if stderr == "" {
		t.Error("expected verbose output on stderr")
	}
}

func TestStatsFlag(t *testing.T) {
	stdout := heyOK(t, "boxes", "--stats", "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Meta == nil {
		t.Error("expected meta with --stats")
		return
	}
	if _, ok := resp.Meta["stats"]; !ok {
		t.Error("expected stats in meta")
	}
}
