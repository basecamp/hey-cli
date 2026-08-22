package cmd

import (
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
	response, err := runJSONCommand(t, clipMutationHandler(t, http.MethodPost, "/clips", func(r *http.Request) {
		if got := r.PostForm.Get("clip[entry_id]"); got != "987" {
			t.Errorf("entry_id = %q", got)
		}
		if got := r.PostForm.Get("clip[content]"); got != content {
			t.Errorf("content = %q", got)
		}
	}), "clip", "create", "987", "--content", content)
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != "Clip from entry 987 created" || response.Data.(map[string]any)["entry_id"] != float64(987) {
		t.Errorf("response = %#v", response)
	}
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
	if got := strings.Join(requested, "\n"); got != "GET /clips.json account=2\nPOST /clips account=2\nDELETE /clips/44 account=2" {
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
