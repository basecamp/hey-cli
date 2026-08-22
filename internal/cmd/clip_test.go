package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

const clipsJSON = `[
	{"id":4,"content":"The launch moves to Wednesday.","entry_id":987,"topic":{"id":55,"name":"Launch planning","app_url":"https://app.hey.com/topics/55"},"created_at":"2026-08-22T02:03:04Z"},
	{"id":3,"content":"Keep the existing rollout window.","entry_id":876,"topic":{"id":44,"name":"Release notes","app_url":"https://app.hey.com/topics/44"},"created_at":"2026-08-21T01:02:03Z"}
]`

func TestClipsCommandListsClipsInEveryFormat(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/clips.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, clipsJSON)
	})

	response, err := runJSONCommand(t, handler, "clips")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "2 clips" || !strings.Contains(response.Notice, "newest clips page") {
		t.Errorf("summary = %q, notice = %q", response.Summary, response.Notice)
	}
	items := response.Data.([]any)
	first := items[0].(map[string]any)
	topic := first["topic"].(map[string]any)
	if len(items) != 2 || first["entry_id"] != float64(987) || topic["app_url"] == "" {
		t.Errorf("items = %#v", items)
	}

	ids, idsStderr, err := runFormattedCommandWithStderr(t, handler, []string{"--ids-only"}, "clips")
	if err != nil || ids != "4\n3\n" || !strings.Contains(idsStderr, "newest clips page") {
		t.Errorf("ids = %q, stderr = %q, err = %v", ids, idsStderr, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "clips")
	if err != nil || count != "2\n" {
		t.Errorf("count = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "clips")
	if err != nil || !strings.Contains(markdown, "| 4 | The launch moves to Wednesday\\. | 987 | 55 | Launch planning |") {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
	styled, styledStderr, err := runFormattedCommandWithStderr(t, handler, []string{"--styled"}, "clips")
	if err != nil || !strings.Contains(styled, "Launch planning (55)") || !strings.Contains(styled, "Entry") || !strings.Contains(styledStderr, "newest clips page") {
		t.Errorf("styled = %q, stderr = %q, err = %v", styled, styledStderr, err)
	}
}

func TestClipsMarkdownSurfacesWriteFailure(t *testing.T) {
	cmd := newClipsCommand().cmd
	cmd.SetOut(failingWriter{})
	if err := writeClipsMarkdown(cmd, []generated.Clip{{Id: 4, Content: "Keep this", EntryId: 987}}); err == nil {
		t.Fatal("expected the write failure")
	}
}

func TestClipsCommandPreservesEmptyList(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	})
	response, err := runJSONCommand(t, handler, "clips")
	if err != nil {
		t.Fatal(err)
	}
	if items := response.Data.([]any); len(items) != 0 || response.Summary != "0 clips" || response.Notice != "" {
		t.Errorf("response = %#v", response)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "clips")
	if err != nil || markdown != "(no results)\n" {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
	styled, err := runStyledCommand(t, handler, "clips")
	if err != nil || styled != "No clips found\n" {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
}

func TestClipsCommandSanitizesHumanOutput(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":4,"content":"[click](https://example.invalid)\u001b[31m","entry_id":987,"topic":{"id":55,"name":"Safe\u001b[31mRed"}}]`)
	})
	styled, err := runStyledCommand(t, handler, "clips")
	if err != nil || strings.Contains(styled, "\x1b[31m") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "clips")
	if err != nil || strings.Contains(markdown, "[click](") || !strings.Contains(markdown, `\[click\]`) {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
}

func TestClipCreateSendsEntryAndExactContent(t *testing.T) {
	content := "  The launch moves to Wednesday.\nPlease tell the team.  "
	handler := clipCreateHandler(t, 987, `<p>The launch moves to Wednesday.</p><p>Please tell the team.</p>`, func(r *http.Request) {
		if got := r.PostForm.Get("clip[entry_id]"); got != "987" {
			t.Errorf("entry_id = %q", got)
		}
		if got := r.PostForm.Get("clip[content]"); got != content {
			t.Errorf("content = %q", got)
		}
	})
	response, err := runJSONCommand(t, handler, "clip", "create", "987", "--content", content)
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "Clip from entry 987 created" || response.Data.(map[string]any)["entry_id"] != float64(987) {
		t.Errorf("response = %#v", response)
	}
}

func TestClipCreateMatchesBrowserSelectionText(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		content string
	}{
		{
			name:    "rich text and whitespace",
			source:  `<p>The <strong>launch</strong> moves&nbsp;to<br>Wednesday &amp; Thursday.</p>`,
			content: "The launch moves\r\n\tto Wednesday & Thursday.",
		},
		{
			name:    "adjacent inline elements",
			source:  `<p>quarter<strong>ly</strong> rollout</p>`,
			content: "quarterly rollout",
		},
		{
			name:    "selection across list items",
			source:  `<ul><li>Revenue up</li><li>Churn down</li></ul>`,
			content: "Revenue up\nChurn down",
		},
		{
			name:    "unicode line separator",
			source:  "<p>First\u2028Second</p>",
			content: "First Second",
		},
		{
			name:    "embedded inbound email body",
			source:  `<figure data-trix-attachment='{"contentType":"text/html","content":"<shadow-content><template><p>External confirmation: BLUE-42</p></template></shadow-content>"}'></figure>`,
			content: "External confirmation: BLUE-42",
		},
		{
			name:    "literal HTML-shaped text",
			source:  `<p>Keep &lt;strong&gt;literal&lt;/strong&gt; text.</p>`,
			content: `<strong>literal</strong>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := clipCreateHandler(t, 987, tt.source, func(r *http.Request) {
				if got := r.PostForm.Get("clip[content]"); got != tt.content {
					t.Errorf("stored content = %q, want exact input %q", got, tt.content)
				}
			})
			if _, err := runJSONCommand(t, handler, "clip", "create", "987", "--content", tt.content); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClipCreateRejectsTextOutsideTheEntry(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		content string
	}{
		{name: "unrelated text", source: `<p>The launch moves to Wednesday.</p>`, content: "An unrelated reminder"},
		{name: "case differs", source: `<p>Confirmation Code: BLUE-42</p>`, content: "confirmation code: BLUE-42"},
		{name: "markup is not selected text", source: `<p>The <strong>launch</strong> moved.</p>`, content: `<strong>launch</strong>`},
		{name: "script text is not selectable", source: `<p>Visible text</p><script>secret value</script>`, content: "secret value"},
		{name: "hidden text is not selectable", source: `<p>Visible text</p><p hidden>Hidden preheader</p>`, content: "Hidden preheader"},
		{name: "block boundaries remain distinct", source: `<section>Alpha</section><section>Beta</section>`, content: "AlphaBeta"},
		{name: "unparseable source fails closed", source: strings.Repeat("<div>", 1_000) + "not selectable" + strings.Repeat("</div>", 1_000), content: "not selectable"},
		{name: "summary is not entry content", source: ``, content: "Preview summary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/messages/987.json" {
					t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected mutation", http.StatusInternalServerError)
					return
				}
				writeClipSource(t, w, 987, tt.source, "Preview summary")
			})
			_, err := runJSONCommand(t, handler, "clip", "create", "987", "--content", tt.content)
			if err == nil || !strings.Contains(err.Error(), "--content does not match text in entry 987") {
				t.Fatalf("error = %v", err)
			}
			if requests != 1 {
				t.Errorf("requests = %d, want the source read without a clip mutation", requests)
			}
		})
	}
}

func TestClipCreateRequiresAnAvailableSourceMessage(t *testing.T) {
	t.Run("read failure", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/messages/987.json" {
				t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
			}
			http.NotFound(w, r)
		})
		if _, err := runJSONCommand(t, handler, "clip", "create", "987", "--content", "Keep this"); err == nil {
			t.Fatal("expected the source read failure")
		}
	})

	t.Run("empty response", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `null`)
		})
		_, err := runJSONCommand(t, handler, "clip", "create", "987", "--content", "Keep this")
		if err == nil || !strings.Contains(err.Error(), "--content does not match text in entry 987") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("source over validation limit", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/messages/987.json" {
				t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
			}
			writeClipSource(t, w, 987, strings.Repeat("x", maxClipSourceBytes+1), "")
		})
		_, err := runJSONCommand(t, handler, "clip", "create", "987", "--content", "x")
		if err == nil || !strings.Contains(err.Error(), "content exceeds the 1 MiB clip validation limit") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestClipDeleteUsesClipID(t *testing.T) {
	response, err := runJSONCommand(t, clipMutationHandler(t, http.MethodDelete, "/clips/44", nil), "clip", "delete", "44")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "Clip 44 deleted" || response.Data.(map[string]any)["id"] != float64(44) {
		t.Errorf("response = %#v", response)
	}
}

func TestClipCommandsUseTheSelectedAccount(t *testing.T) {
	var requested []string
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.Method+" "+r.URL.Path+" account="+r.URL.Query().Get("filtered_account_id"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/clips.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodGet && r.URL.Path == "/messages/987.json":
			writeClipSource(t, w, 987, "Keep this", "")
		case r.Method == http.MethodPost && r.URL.Path == "/clips":
			w.Header().Set("Location", "/clips")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodDelete && r.URL.Path == "/clips/44":
			w.Header().Set("Location", "/clips")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})

	for _, args := range [][]string{
		{"--account", "2", "clips"},
		{"--account", "2", "clip", "create", "987", "--content", "Keep this"},
		{"--account", "2", "clip", "delete", "44"},
	} {
		if output, err := runAccountsCLI(t, server, args...); err != nil {
			t.Fatalf("hey %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	if got := strings.Join(requested, "\n"); got != "GET /clips.json account=2\nGET /messages/987.json account=2\nPOST /clips account=2\nDELETE /clips/44 account=2" {
		t.Errorf("requests:\n%s", got)
	}
}

func TestClipCommandsValidateInput(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unexpected request")
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "create content", args: []string{"clip", "create", "987"}, want: "--content is required"},
		{name: "blank content", args: []string{"clip", "create", "987", "--content", "  "}, want: "--content is required"},
		{name: "oversized content", args: []string{"clip", "create", "987", "--content", strings.Repeat("x", maxClipContentBytes+1)}, want: "--content exceeds the 64 KiB clip limit"},
		{name: "invalid entry", args: []string{"clip", "create", "zero", "--content", "Keep this"}, want: "invalid entry ID: zero"},
		{name: "invalid clip", args: []string{"clip", "delete", "zero"}, want: "invalid clip ID: zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runJSONCommand(t, handler, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func clipCreateHandler(t *testing.T, entryID int64, source string, validate func(*http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/messages/987.json":
			writeClipSource(t, w, entryID, source, "")
		case r.Method == http.MethodPost && r.URL.Path == "/clips":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if validate != nil {
				validate(r)
			}
			w.Header().Set("Location", "/clips")
			w.WriteHeader(http.StatusFound)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func writeClipSource(t *testing.T, w http.ResponseWriter, entryID int64, content, summary string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"id": entryID, "content": content, "summary": summary}); err != nil {
		t.Fatal(err)
	}
}

func clipMutationHandler(t *testing.T, method, path string, validate func(*http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method || r.URL.Path != path {
			t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, method, path)
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if validate != nil {
			validate(r)
		}
		w.Header().Set("Location", "/clips")
		w.WriteHeader(http.StatusFound)
	})
}
