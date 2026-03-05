package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriterOK_JSON(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf})

	data := []map[string]any{{"id": 1, "name": "test"}}
	err := w.OK(data, WithSummary("1 item"), WithBreadcrumbs(Breadcrumb{
		Action: "view", Command: "hey test 1", Description: "View item",
	}))
	if err != nil {
		t.Fatal(err)
	}

	var resp Response
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if resp.Summary != "1 item" {
		t.Errorf("expected summary '1 item', got %q", resp.Summary)
	}
	if len(resp.Breadcrumbs) != 1 {
		t.Errorf("expected 1 breadcrumb, got %d", len(resp.Breadcrumbs))
	}
}

func TestWriterOK_Quiet(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatQuiet, Stdout: &buf})

	data := map[string]string{"name": "test"}
	err := w.OK(data)
	if err != nil {
		t.Fatal(err)
	}

	// Quiet mode should output raw data without envelope
	if strings.Contains(buf.String(), `"ok"`) {
		t.Error("quiet mode should not include envelope")
	}
	if !strings.Contains(buf.String(), `"name"`) {
		t.Error("quiet mode should include raw data")
	}
}

func TestWriterOK_Count(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatCount, Stdout: &buf})

	data := []int{1, 2, 3, 4, 5}
	err := w.OK(data)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(buf.String()) != "5" {
		t.Errorf("expected '5', got %q", strings.TrimSpace(buf.String()))
	}
}

func TestWriterOK_IDsOnly(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatIDs, Stdout: &buf})

	data := []map[string]any{
		{"id": 10, "name": "a"},
		{"id": 20, "name": "b"},
	}
	err := w.OK(data)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || lines[0] != "10" || lines[1] != "20" {
		t.Errorf("expected '10\\n20', got %q", buf.String())
	}
}

func TestWriterErr_JSON(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stderr: &buf})

	w.Err(ErrNotFound("topic", "123"))

	var resp ErrorResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.OK {
		t.Error("expected ok=false")
	}
	if resp.Code != "not_found" {
		t.Errorf("expected code 'not_found', got %q", resp.Code)
	}
}

func TestWriterErr_Styled(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatStyled, Stderr: &buf})

	w.Err(ErrAuth("please log in"))

	if !strings.Contains(buf.String(), "Error: please log in") {
		t.Errorf("expected styled error, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Run: hey auth login") {
		t.Errorf("expected hint, got %q", buf.String())
	}
}

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{ErrUsage("bad"), ExitUsage},
		{ErrNotFound("x", "y"), ExitNotFound},
		{ErrAuth("no"), ExitAuth},
		{ErrForbidden("no"), ExitForbidden},
		{ErrRateLimit(0), ExitRateLimit},
		{ErrNetwork(nil), ExitNetwork},
		{ErrAPI(500, "oops"), ExitAPI},
		{ErrAmbiguous("x", nil), ExitAmbiguous},
	}

	for _, tt := range tests {
		got := ExitCodeFor(tt.err)
		if got != tt.want {
			t.Errorf("ExitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestFormatFromFlags(t *testing.T) {
	if f := FormatFromFlags(true, false, false, false, false, false, false); f != FormatJSON {
		t.Errorf("expected FormatJSON, got %d", f)
	}
	if f := FormatFromFlags(false, true, false, false, false, false, false); f != FormatQuiet {
		t.Errorf("expected FormatQuiet, got %d", f)
	}
	if f := FormatFromFlags(false, false, true, false, false, false, false); f != FormatIDs {
		t.Errorf("expected FormatIDs, got %d", f)
	}
	if f := FormatFromFlags(false, false, false, true, false, false, false); f != FormatCount {
		t.Errorf("expected FormatCount, got %d", f)
	}
	if f := FormatFromFlags(false, false, false, false, false, false, true); f != FormatJSON {
		t.Errorf("expected FormatJSON for --agent, got %d", f)
	}
	if f := FormatFromFlags(false, false, false, false, false, false, false); f != FormatAuto {
		t.Errorf("expected FormatAuto, got %d", f)
	}
}

func TestNormalizeJSONNumbers(t *testing.T) {
	data := []byte(`{"id": 1234567890123456789}`)
	v, err := NormalizeJSONNumbers(data)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	// Should preserve as json.Number, not float64
	if _, ok := m["id"].(json.Number); !ok {
		t.Errorf("expected json.Number, got %T", m["id"])
	}
}
