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

// `omitempty` drops a nil interface but never a typed nil slice, so an empty listing
// used to marshal as `"data": null` and the documented `--jq '.data[]'` recipe failed
// with "cannot iterate over: null".
func TestWriterOK_EmptyListMarshalsAsAnArray(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf})

	var recordings []map[string]any
	if err := w.OK(recordings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"data": []`) {
		t.Errorf("output = %q, want an empty array", buf.String())
	}
}

func TestWriterOK_NoDataIsStillOmitted(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf})

	if err := w.OK(nil, WithSummary("Journal entry for 2026-01-31 saved")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"data"`) {
		t.Errorf("output = %q, want no data field", buf.String())
	}
}

// A mutation the API answered with nothing hands OK a typed nil pointer, which
// `omitempty` keeps: it is a non-nil interface. Left alone it reports "data": null.
func TestWriterOK_TypedNilDataIsOmitted(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf})

	type recording struct {
		ID int64 `json:"id"`
	}
	var missing *recording
	if err := w.OK(missing, WithSummary("Todo created")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"data"`) {
		t.Errorf("output = %q, want no data field", buf.String())
	}
}

func TestWriterOK_EmptyListJQIteratesToNothing(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data[]"})

	var recordings []map[string]any
	if err := w.OK(recordings); err != nil {
		t.Fatalf("iterating an empty listing failed: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("output = %q, want nothing", buf.String())
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

func TestWriterOK_JQFiltersEnvelope(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data.name"})

	if err := w.OK(map[string]any{"name": "Jane"}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "Jane\n" {
		t.Errorf("expected scalar text, got %q", got)
	}
}

func TestWriterOK_JQFiltersQuietData(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatQuiet, Stdout: &buf, JQFilter: ".[0].id"})

	if err := w.OK([]map[string]any{{"id": 42}}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "42\n" {
		t.Errorf("expected data-only result, got %q", got)
	}
}

func TestWriterOK_JQFormatsObjectsAndArrays(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data"})

	if err := w.OK(map[string]any{"names": []string{"Jane", "Lin"}}); err != nil {
		t.Fatal(err)
	}
	const expected = "{\n  \"names\": [\n    \"Jane\",\n    \"Lin\"\n  ]\n}\n"
	if got := buf.String(); got != expected {
		t.Errorf("expected formatted JSON:\n%s\ngot:\n%s", expected, got)
	}
}

func TestWriterOK_JQWritesMultipleAndEmptyResults(t *testing.T) {
	t.Run("multiple", func(t *testing.T) {
		var buf bytes.Buffer
		w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data[].name"})
		if err := w.OK([]map[string]any{{"name": "Jane"}, {"name": "Lin"}}); err != nil {
			t.Fatal(err)
		}
		if got := buf.String(); got != "Jane\nLin\n" {
			t.Errorf("unexpected results: %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		var buf bytes.Buffer
		w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data[] | select(.active)"})
		if err := w.OK([]map[string]any{{"active": false}}); err != nil {
			t.Fatal(err)
		}
		if got := buf.String(); got != "" {
			t.Errorf("expected no output, got %q", got)
		}
	})
}

func TestWriterOK_JQPreservesLargeIntegers(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data.id"})

	if err := w.OK(map[string]any{"id": json.Number("1234567890123456789")}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "1234567890123456789\n" {
		t.Errorf("integer precision changed: %q", got)
	}
}

func TestWriterOK_JQUsesEnvironmentVariables(t *testing.T) {
	t.Setenv("HEY_JQ_TEST_NAME", "Jane")
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: "env.HEY_JQ_TEST_NAME"})

	if err := w.OK(nil); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "Jane\n" {
		t.Errorf("expected environment value, got %q", got)
	}
}

func TestWriterOK_JQReportsExpressionErrors(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		w := New(Options{Format: FormatJSON, Stdout: &bytes.Buffer{}, JQFilter: ".[invalid"})
		err := w.OK(nil)
		if err == nil || !strings.Contains(err.Error(), "invalid --jq expression") {
			t.Fatalf("expected validation error, got %v", err)
		}
		if AsError(err).Code != "usage" {
			t.Errorf("expected usage code, got %q", AsError(err).Code)
		}
	})

	t.Run("runtime", func(t *testing.T) {
		w := New(Options{Format: FormatJSON, Stdout: &bytes.Buffer{}, JQFilter: ".data.id[]"})
		err := w.OK(map[string]any{"id": 42})
		if err == nil || !strings.Contains(err.Error(), "jq filter error") {
			t.Fatalf("expected runtime error, got %v", err)
		}
		if AsError(err).Code != "usage" {
			t.Errorf("expected usage code, got %q", AsError(err).Code)
		}
	})
}

func TestWriterErr_JQKeepsErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stderr: &buf, JQFilter: ".data.id"})

	w.Err(ErrNotFound("topic", "123"))

	var resp ErrorResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
	if resp.Code != "not_found" {
		t.Errorf("expected unfiltered error, got %#v", resp)
	}
}

func TestWriterJQSanitizesTerminalResults(t *testing.T) {
	const unsafe = "safe\x1b]8;;https://example.com\x07link\x1b]8;;\x07\u009b31m!"

	t.Run("string", func(t *testing.T) {
		var buf bytes.Buffer
		w := New(Options{Stdout: &buf})
		if err := w.writeJQResult(unsafe, true); err != nil {
			t.Fatal(err)
		}
		if got := buf.String(); got != "safelink31m!\n" {
			t.Errorf("unexpected sanitized string: %q", got)
		}
	})

	t.Run("compound", func(t *testing.T) {
		var buf bytes.Buffer
		w := New(Options{Stdout: &buf})
		if err := w.writeJQResult(map[string]any{unsafe: []any{unsafe}}, true); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(buf.String(), "\x1b\u009b") {
			t.Errorf("terminal controls remain in compound output: %q", buf.String())
		}
		if !strings.Contains(buf.String(), `"safelink31m!"`) {
			t.Errorf("sanitized value missing: %q", buf.String())
		}
	})

	t.Run("key collisions preserve every field", func(t *testing.T) {
		var buf bytes.Buffer
		w := New(Options{Stdout: &buf})
		input := map[string]any{
			"title":            "clean",
			"\\u001b[31mtitle": "literal",
			"\x1b[31mtitle":    "escaped",
		}
		if err := w.writeJQResult(input, true); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result) != len(input) {
			t.Fatalf("sanitization dropped a field: %#v", result)
		}
		if result["title"] != "clean" || result["\\u001b[31mtitle"] != "literal" {
			t.Errorf("sanitization replaced a clean field: %#v", result)
		}
		for key := range result {
			if strings.ContainsRune(key, '\x1b') {
				t.Errorf("terminal escape remains in key %q", key)
			}
		}
	})

	t.Run("pipe preserves bytes", func(t *testing.T) {
		var buf bytes.Buffer
		w := New(Options{Stdout: &buf})
		if err := w.writeJQResult(unsafe, false); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSuffix(buf.String(), "\n"); got != unsafe {
			t.Errorf("piped bytes changed: %q", got)
		}
	})
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

func TestWriterOK_MarkdownIncludesOptionalFieldsFromLaterRows(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatMarkdown, Stdout: &buf})

	data := []map[string]any{
		{"name": "Archived thread"},
		{"id": 20, "name": "Active thread"},
	}
	if err := w.OK(data); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "| id | name |") {
		t.Errorf("markdown header omitted optional id: %q", output)
	}
	if !strings.Contains(output, "| 20 | Active thread |") {
		t.Errorf("markdown rows omitted later id: %q", output)
	}
}

func TestWriterOK_MarkdownSanitizesTerminalControlsAndLayout(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatMarkdown, Stdout: &buf})

	data := []map[string]any{{"name": "Receipts\x1b]2;owned\a\nArchive|2026\tQ3 " + `Path\|Receipts`}}
	if err := w.OK(data); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if strings.Contains(output, "\x1b") || strings.Contains(output, "\a") || strings.Contains(output, "\nArchive") {
		t.Errorf("unsafe controls reached markdown output: %q", output)
	}
	if !strings.Contains(output, `<br>Archive\|2026 Q3 Path\\\|Receipts`) {
		t.Errorf("sanitized markdown value missing from %q", output)
	}
}

func TestEscapeMarkdownTablePipesPreservesBackslashes(t *testing.T) {
	for backslashes := range 4 {
		input := strings.Repeat(`\`, backslashes) + "|"
		want := strings.Repeat(`\`, 2*backslashes+1) + "|"
		if got := escapeMarkdownTablePipes(input); got != want {
			t.Errorf("escapeMarkdownTablePipes(%q) = %q, want %q", input, got, want)
		}
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
