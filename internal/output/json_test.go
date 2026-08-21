package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// encoding/json writes the C1 controls raw; a terminal reading the envelope sees an
// 8-bit CSI. Escaping them leaves the decoded string identical.
func TestWriterEscapesC1ControlsInEveryJSONFormat(t *testing.T) {
	name := "Ryan\u009b31mSinger"
	for _, test := range []struct {
		name   string
		format Format
		filter string
	}{
		{"json", FormatJSON, ""},
		{"quiet", FormatQuiet, ""},
		{"jq object", FormatJSON, ".data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := New(Options{Format: test.format, Stdout: &buf, JQFilter: test.filter})
			if err := w.OK(map[string]any{"name": name}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "\u009b") {
				t.Errorf("output = %q carries a raw C1 control", buf.String())
			}
			if !strings.Contains(buf.String(), `\u009b`) {
				t.Errorf("output = %q, want the control escaped", buf.String())
			}
			var decoded map[string]any
			if test.format == FormatJSON && test.filter == "" {
				var resp struct{ Data map[string]any }
				if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				decoded = resp.Data
			} else if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if decoded["name"] != name {
				t.Errorf("decoded name = %q, want the original %q", decoded["name"], name)
			}
		})
	}
}

func TestWriterErrEscapesC1Controls(t *testing.T) {
	var stderr bytes.Buffer
	w := New(Options{Format: FormatJSON, Stderr: &stderr})
	w.Err(apierr.ErrUsage("bad label \u009b31m"))
	if strings.Contains(stderr.String(), "\u009b") || !strings.Contains(stderr.String(), `\u009b`) {
		t.Errorf("stderr = %q, want the control escaped", stderr.String())
	}
}

// An error printed as text — on a terminal, or on stderr beside an --html redirect —
// may carry a server's words, and is sanitized like any other terminal text.
func TestWriterErrSanitizesTextErrors(t *testing.T) {
	for _, format := range []Format{FormatStyled, FormatHTML} {
		var stderr bytes.Buffer
		w := New(Options{Format: format, Stderr: &stderr})
		w.Err(apierr.ErrUsageHint("bad \x1b[31mlabel", "try \x1b]0;x\x07again"))
		if got := stderr.String(); got != "Error: bad label\ntry again\n" {
			t.Errorf("format %d: stderr = %q", format, got)
		}
	}
}

// A jq string result on a pipe is the value itself, as `jq -r` writes it.
func TestWriterJQStringResultIsRawOnAPipe(t *testing.T) {
	var buf bytes.Buffer
	w := New(Options{Format: FormatJSON, Stdout: &buf, JQFilter: ".data.name"})
	if err := w.OK(map[string]any{"name": "tab\there"}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "tab\there\n" {
		t.Errorf("output = %q, want the raw value", buf.String())
	}
}

func TestMarshalJSONEscapesOnlyC1(t *testing.T) {
	data, err := MarshalJSON(map[string]string{"name": "Café \u0085 ☕ \u2028 \u00a0"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"name":"Café \u0085 ☕ \u2028 `+"\u00a0"+`"}`; got != want {
		t.Errorf("MarshalJSON = %s, want %s", got, want)
	}
}
