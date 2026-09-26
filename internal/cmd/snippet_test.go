package cmd

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

const snippetsJSON = `[
	{"id":3,"name":"Office hours","content":"Monday through Thursday","content_html":"<div class=\"trix-content\">Monday through Thursday</div>","updated_at":"2026-08-22T01:02:03Z"},
	{"id":4,"name":"Scheduling reply","content":"Does Tuesday work?","content_html":"<div class=\"trix-content\">Does Tuesday work?</div>","updated_at":"2026-08-22T02:03:04Z"}
]`

func TestSnippetsCommandListsSnippetsInEveryFormat(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/snippets.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, snippetsJSON)
	})

	response, err := runJSONCommand(t, handler, "snippet", "list")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "2 snippets" {
		t.Errorf("summary = %q", response.Summary)
	}
	items := response.Data.([]any)
	if len(items) != 2 || items[0].(map[string]any)["content_html"] == "" {
		t.Errorf("items = %#v", items)
	}

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "snippet", "list")
	if err != nil || ids != "3\n4\n" {
		t.Errorf("ids = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "snippet", "list")
	if err != nil || count != "2\n" {
		t.Errorf("count = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "snippet", "list")
	if err != nil || !strings.Contains(markdown, "| 3 | Office hours | Monday through Thursday |") {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
	styled, err := runStyledCommand(t, handler, "snippet", "list")
	if err != nil || !strings.Contains(styled, "Office hours") || !strings.Contains(styled, "Updated") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
}

func TestSnippetsMarkdownSurfacesWriteFailure(t *testing.T) {
	cmd := newSnippetListCommand().cmd
	cmd.SetOut(failingWriter{})
	if err := writeSnippetsMarkdown(cmd, []generated.Snippet{{Id: 3, Name: "Greeting", Content: "Hello"}}); err == nil {
		t.Fatal("expected the write failure")
	}
}

func TestSnippetsCommandPreservesEmptyList(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}), "snippet", "list")
	if err != nil {
		t.Fatal(err)
	}
	if items := response.Data.([]any); len(items) != 0 || response.Summary != "0 snippets" {
		t.Errorf("response = %#v", response)
	}
	markdown, err := runFormattedCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}), []string{"--markdown"}, "snippet", "list")
	if err != nil || markdown != "(no results)\n" {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
}

func TestSnippetsCommandSanitizesHumanOutput(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":3,"name":"Safe\u001b[31mRed","content":"[click](https://example.invalid)"}]`)
	})
	styled, err := runStyledCommand(t, handler, "snippet", "list")
	if err != nil || strings.Contains(styled, "\x1b[31m") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "snippet", "list")
	if err != nil || strings.Contains(markdown, "[click](") || !strings.Contains(markdown, `\[click\]`) {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
}

func TestSnippetCreateSendsNameAndContent(t *testing.T) {
	response, err := runJSONCommand(t, snippetMutationHandler(t, http.MethodPost, "/snippets", func(r *http.Request) {
		if got := r.PostForm.Get("snippet[name]"); got != "Scheduling reply" {
			t.Errorf("name = %q", got)
		}
		if got := r.PostForm.Get("snippet[content]"); got != "<p>Does Tuesday work?</p>" {
			t.Errorf("content = %q", got)
		}
	}), "snippet", "create", "--name", "Scheduling reply", "--content", "Does Tuesday work?")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != `Snippet "Scheduling reply" created` || response.Data.(map[string]any)["name"] != "Scheduling reply" {
		t.Errorf("response = %#v", response)
	}
}

func TestSnippetCreateConvertsMarkdownContent(t *testing.T) {
	_, err := runJSONCommand(t, snippetMutationHandler(t, http.MethodPost, "/snippets", func(r *http.Request) {
		if got := r.PostForm.Get("snippet[content]"); got != "<p>Does <strong>Tuesday</strong> work?</p>" {
			t.Errorf("content = %q", got)
		}
	}), "snippet", "create", "--name", "Scheduling reply", "--content", "Does **Tuesday** work?")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnippetCreateSendsRawHTMLVerbatim(t *testing.T) {
	_, err := runJSONCommand(t, snippetMutationHandler(t, http.MethodPost, "/snippets", func(r *http.Request) {
		if got := r.PostForm.Get("snippet[content]"); got != "<div>Office hours are <strong>Monday through Thursday</strong>.</div>" {
			t.Errorf("content = %q", got)
		}
	}), "snippet", "create", "--name", "Office hours", "--content-html", "<div>Office hours are <strong>Monday through Thursday</strong>.</div>")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnippetUpdateConvertsMarkdownContent(t *testing.T) {
	_, err := runJSONCommand(t, snippetMutationHandler(t, http.MethodPatch, "/snippets/44", func(r *http.Request) {
		if got := r.PostForm.Get("snippet[content]"); got != "<p><em>Wednesday</em> works for me.</p>" {
			t.Errorf("content = %q", got)
		}
	}), "snippet", "update", "44", "--content", "*Wednesday* works for me.")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnippetUpdateSendsOnlyChangedFields(t *testing.T) {
	response, err := runJSONCommand(t, snippetMutationHandler(t, http.MethodPatch, "/snippets/44", func(r *http.Request) {
		if got := r.PostForm.Get("snippet[name]"); got != "Scheduling" {
			t.Errorf("name = %q", got)
		}
		if r.PostForm.Has("snippet[content]") {
			t.Errorf("unexpected content: %q", r.PostForm.Get("snippet[content]"))
		}
	}), "snippet", "update", "44", "--name", "Scheduling")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "Snippet 44 updated" || response.Data.(map[string]any)["id"] != float64(44) {
		t.Errorf("response = %#v", response)
	}
}

func TestSnippetDeleteUsesSnippetID(t *testing.T) {
	response, err := runJSONCommand(t, snippetMutationHandler(t, http.MethodDelete, "/snippets/44", nil), "snippet", "delete", "44")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "Snippet 44 deleted" || response.Data.(map[string]any)["id"] != float64(44) {
		t.Errorf("response = %#v", response)
	}
}

func TestSnippetCommandsValidateInput(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unexpected request")
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "create name", args: []string{"snippet", "create", "--content", "Hello"}, want: "--name is required"},
		{name: "create content", args: []string{"snippet", "create", "--name", "Greeting"}, want: "--content is required"},
		{name: "update fields", args: []string{"snippet", "update", "44"}, want: "provide --name, --content or --content-html"},
		{name: "exclusive content", args: []string{"snippet", "create", "--name", "Greeting", "--content", "Hello", "--content-html", "<p>Hello</p>"}, want: "none of the others can be"},
		{name: "empty update", args: []string{"snippet", "update", "44", "--content", "  "}, want: "--content cannot be empty"},
		{name: "invalid id", args: []string{"snippet", "delete", "zero"}, want: "invalid snippet ID: zero"},
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

func snippetMutationHandler(t *testing.T, method, path string, validate func(*http.Request)) http.Handler {
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
		w.Header().Set("Location", "/snippets")
		w.WriteHeader(http.StatusFound)
	})
}

// snippetStore stands in for HEY's snippets: it keeps what was written and lists it the
// way _snippet.jbuilder does, with content_html inside Action Text's layout.
type snippetStore struct {
	mu       sync.Mutex
	snippets map[int64]string
	nextID   int64
	writes   []string
}

func newSnippetStore(t *testing.T) (http.Handler, *snippetStore) {
	t.Helper()
	store := &snippetStore{snippets: map[int64]string{44: "<div>Office hours are <strong>Monday through Thursday</strong>.</div>"}, nextID: 45}
	return http.HandlerFunc(store.serve), store
}

func (s *snippetStore) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/snippets.json":
		listed := make([]generated.Snippet, 0, len(s.snippets))
		for _, id := range slices.Sorted(maps.Keys(s.snippets)) {
			listed = append(listed, generated.Snippet{
				Id:          id,
				Name:        "Office hours",
				Content:     htmlutil.ToText(s.snippets[id]),
				ContentHtml: "<div class=\"trix-content\">\n  " + s.snippets[id] + "\n</div>\n",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listed)
	case r.Method == http.MethodPost && r.URL.Path == "/snippets":
		s.save(w, r, s.nextID)
		s.nextID++
	case r.Method == http.MethodPatch && r.URL.Path == "/snippets/44":
		s.save(w, r, 44)
	default:
		http.NotFound(w, r)
	}
}

func (s *snippetStore) save(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	content := r.PostForm.Get("snippet[content]")
	s.writes = append(s.writes, content)
	s.snippets[id] = content
	w.Header().Set("Location", "/snippets")
	w.WriteHeader(http.StatusFound)
}

func (s *snippetStore) snapshot() (snippets map[int64]string, writes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.snippets), append([]string(nil), s.writes...)
}

func (s *snippetStore) newest() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Max(slices.Collect(maps.Keys(s.snippets)))
}

func listedSnippetHTML(t *testing.T, handler http.Handler, id int64) string {
	t.Helper()
	response, err := runJSONCommand(t, handler, "snippet", "list")
	if err != nil {
		t.Fatalf("snippet list: %v", err)
	}
	for _, item := range response.Data.([]any) {
		snippet := item.(map[string]any)
		if snippet["id"] == float64(id) {
			return snippet["content_html"].(string)
		}
	}
	t.Fatalf("snippet %d not listed in %#v", id, response.Data)
	return ""
}

// content_html is served inside HEY's editor wrapper, and writing it back used to store
// the wrapper too, so the snippet sank one div deeper on every round trip. Each trip reads
// the newest snippet: the one updated, or the copy just created from the one before it.
func TestSnippetHTMLRoundTripDoesNotNest(t *testing.T) {
	for name, args := range map[string][]string{
		"update": {"snippet", "update", "44"},
		"create": {"snippet", "create", "--name", "Office hours"},
	} {
		t.Run(name, func(t *testing.T) {
			handler, store := newSnippetStore(t)
			for range 3 {
				content := listedSnippetHTML(t, handler, store.newest())
				if _, err := runJSONCommand(t, handler, append(args, "--content-html", content+"<div>Closed on Fridays.</div>")...); err != nil {
					t.Fatal(err)
				}
			}
			newest := store.newest()
			snippets, writes := store.snapshot()
			for _, written := range writes {
				if strings.Contains(written, "trix-content") {
					t.Errorf("wrote %q, want HEY's wrapper taken off", written)
				}
			}
			if last := snippets[newest]; strings.Count(last, "Closed on Fridays.") != 3 || !strings.Contains(last, "<strong>Monday through Thursday</strong>") {
				t.Errorf("snippet %d = %q, want the content and every addition", newest, last)
			}
			if listed := listedSnippetHTML(t, handler, newest); strings.Count(listed, "trix-content") != 1 {
				t.Errorf("content_html = %q, want one wrapper", listed)
			}
		})
	}
}
