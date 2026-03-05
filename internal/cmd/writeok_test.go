package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/basecamp/hey-cli/internal/client"
	"github.com/basecamp/hey-cli/internal/output"
)

func TestWriteOKIncludesStats(t *testing.T) {
	var buf bytes.Buffer
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &buf})
	apiClient = client.New("https://example.com", nil)
	statsFlag = true
	defer func() { statsFlag = false }()

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
