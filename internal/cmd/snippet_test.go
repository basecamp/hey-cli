package cmd

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
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

	response, err := runJSONCommand(t, handler, "snippets")
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

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "snippets")
	if err != nil || ids != "3\n4\n" {
		t.Errorf("ids = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "snippets")
	if err != nil || count != "2\n" {
		t.Errorf("count = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "snippets")
	if err != nil || !strings.Contains(markdown, "| 3 | Office hours | Monday through Thursday |") {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
	styled, err := runStyledCommand(t, handler, "snippets")
	if err != nil || !strings.Contains(styled, "Office hours") || !strings.Contains(styled, "Updated") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
}

func TestSnippetsMarkdownSurfacesWriteFailure(t *testing.T) {
	cmd := newSnippetsCommand().cmd
	cmd.SetOut(failingWriter{})
	if err := writeSnippetsMarkdown(cmd, []generated.Snippet{{Id: 3, Name: "Greeting", Content: "Hello"}}); err == nil {
		t.Fatal("expected the write failure")
	}
}

func TestSnippetsCommandPreservesEmptyList(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}), "snippets")
	if err != nil {
		t.Fatal(err)
	}
	if items := response.Data.([]any); len(items) != 0 || response.Summary != "0 snippets" {
		t.Errorf("response = %#v", response)
	}
	markdown, err := runFormattedCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}), []string{"--markdown"}, "snippets")
	if err != nil || markdown != "(no results)\n" {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
}

func TestSnippetsCommandSanitizesHumanOutput(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":3,"name":"Safe\u001b[31mRed","content":"[click](https://example.invalid)"}]`)
	})
	styled, err := runStyledCommand(t, handler, "snippets")
	if err != nil || strings.Contains(styled, "\x1b[31m") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "snippets")
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
