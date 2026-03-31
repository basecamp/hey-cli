package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/client"
	"github.com/basecamp/hey-cli/internal/output"
)

// TestBareHeyShowsHelpWithoutTTY verifies that bare `hey` falls back to help
// when either stdin or stdout is not a terminal (e.g. `hey | head`, cron,
// CI pipelines). In test context neither fd is a TTY, covering both gates.
func TestBareHeyShowsHelpWithoutTTY(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{})

	if err := root.Execute(); err != nil {
		t.Fatalf("bare hey: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "USAGE") {
		t.Errorf("expected help output with USAGE, got:\n%s", out)
	}
	if !strings.Contains(out, "hey <command>") {
		t.Errorf("expected help to mention 'hey <command>', got:\n%s", out)
	}
}

func TestWriteOKIncludesStats(t *testing.T) {
	oldWriter, oldClient, oldStats := writer, apiClient, statsFlag
	defer func() { writer, apiClient, statsFlag = oldWriter, oldClient, oldStats }()

	var buf bytes.Buffer
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &buf})
	apiClient = client.New("https://example.com", nil)
	statsFlag = true

	if err := writeOK(map[string]string{"hello": "world"}, output.WithSummary("test")); err != nil {
		t.Fatalf("writeOK: %v", err)
	}

	var resp output.Response
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !resp.OK {
		t.Error("expected ok=true")
	}
	if resp.Summary != "test" {
		t.Errorf("summary = %q, want %q", resp.Summary, "test")
	}
	if resp.Meta == nil {
		t.Fatal("expected meta to be present")
	}
	if _, ok := resp.Meta["stats"]; !ok {
		t.Error("expected meta.stats to be present when --stats is set")
	}
}

func TestWriteOKOmitsStatsWhenFlagOff(t *testing.T) {
	oldWriter, oldClient, oldStats := writer, apiClient, statsFlag
	defer func() { writer, apiClient, statsFlag = oldWriter, oldClient, oldStats }()

	var buf bytes.Buffer
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &buf})
	apiClient = client.New("https://example.com", nil)
	statsFlag = false

	if err := writeOK(map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("writeOK: %v", err)
	}

	var resp output.Response
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Meta != nil {
		if _, ok := resp.Meta["stats"]; ok {
			t.Error("expected meta.stats to be absent when --stats is off")
		}
	}
}
