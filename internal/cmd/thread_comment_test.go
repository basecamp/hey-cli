package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// sentComment is what the fixture server saw. The server goroutine writes it and the test
// reads it after the round trip; the race detector does not count the socket as
// synchronization, so it is guarded.
type sentComment struct {
	mu          sync.Mutex
	calls       int
	contentType string
	content     string
	hasContent  bool
	audience    int
}

func (s *sentComment) snapshot() (calls int, contentType, content string, hasContent bool, audience int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.contentType, s.content, s.hasContent, s.audience
}

const threadCommentNoteJSON = `{"id":901,"kind":"comment","topic_id":42,"content":"<p>ok</p>",` +
	`"visible_to":[{"id":7,"name":"Ana Pereira","email_address":"ana@example.com"},{"id":8,"name":"Bo","email_address":"bo@example.com"}],` +
	`"collection_only":false}`

// threadCommentServer answers the JSON endpoints HEY serves notes on, and fails anything
// else — the form endpoint (/topics/42/comments without .json) included.
func threadCommentServer(t *testing.T, createStatus int, createBody string) (http.Handler, *sentComment) {
	t.Helper()
	sent := &sentComment{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/topics/42/comments.json":
			var body struct {
				Comment struct {
					Content *string `json:"content"`
				} `json:"comment"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			sent.mu.Lock()
			sent.calls++
			sent.contentType = r.Header.Get("Content-Type")
			if body.Comment.Content != nil {
				sent.hasContent = true
				sent.content = *body.Comment.Content
			}
			sent.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(createStatus)
			_, _ = w.Write([]byte(createBody))
		case r.Method == http.MethodGet && r.URL.Path == "/topics/42/comments/new.json":
			sent.mu.Lock()
			sent.audience++
			sent.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"visible_to":[{"id":7,"name":"Ana Pereira","email_address":"ana@example.com"}],"collection_only":false}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	return handler, sent
}

func TestThreadCommentPostsToTheJSONEndpoint(t *testing.T) {
	handler, sent := threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	response, err := runJSONCommand(t, handler, "thread", "comment", "42", "-m", "Following up with accounting on this.")
	if err != nil {
		t.Fatal(err)
	}
	calls, contentType, content, hasContent, _ := sent.snapshot()
	if calls != 1 {
		t.Fatalf("POST /topics/42/comments.json calls = %d, want 1", calls)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("content-type = %q, want application/json", contentType)
	}
	if !hasContent || content != "<p>Following up with accounting on this.</p>" {
		t.Errorf("comment.content = %q (present %v)", content, hasContent)
	}
	if response.Summary != "Note 901 added to thread 42" {
		t.Errorf("summary = %q", response.Summary)
	}
	data := response.Data.(map[string]any)
	if data["id"] != float64(901) || data["thread_id"] != float64(42) || data["collection_only"] != false {
		t.Errorf("data = %#v", data)
	}
	visibleTo, ok := data["visible_to"].([]any)
	if !ok || len(visibleTo) != 2 || visibleTo[0].(map[string]any)["email_address"] != "ana@example.com" {
		t.Errorf("visible_to = %#v", data["visible_to"])
	}
}

func TestThreadCommentEscapesPlainText(t *testing.T) {
	handler, sent := threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	if _, err := runJSONCommand(t, handler, "thread", "comment", "42", "-m", "Tom & Jerry say 1 < 2"); err != nil {
		t.Fatal(err)
	}
	_, _, content, _, _ := sent.snapshot()
	if content != "<p>Tom &amp; Jerry say 1 &lt; 2</p>" {
		t.Errorf("content = %q, want the & and < escaped", content)
	}
}

func TestThreadCommentConvertsMarkdownAndPassesHTML(t *testing.T) {
	handler, sent := threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	if _, err := runJSONCommand(t, handler, "thread", "comment", "42", "-m", "**Paid** in full"); err != nil {
		t.Fatal(err)
	}
	if _, _, content, _, _ := sent.snapshot(); content != "<p><strong>Paid</strong> in full</p>" {
		t.Errorf("markdown content = %q", content)
	}

	handler, sent = threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	if _, err := runJSONCommand(t, handler, "thread", "comment", "42", "--message-html", "<div>As <em>typed</em></div>"); err != nil {
		t.Fatal(err)
	}
	if _, _, content, _, _ := sent.snapshot(); content != "<div>As <em>typed</em></div>" {
		t.Errorf("html content = %q", content)
	}
}

func TestThreadCommentStyledOutputNamesTheAudience(t *testing.T) {
	handler, _ := threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	styled, err := runStyledCommand(t, handler, "thread", "comment", "42", "-m", "Noted.")
	if err != nil {
		t.Fatal(err)
	}
	want := "Note 901 added to thread 42, visible to you and Ana Pereira <ana@example.com>, Bo <bo@example.com>."
	if !strings.Contains(styled, want) {
		t.Errorf("styled = %q, want %q", styled, want)
	}
}

func TestThreadCommentNoteToSelf(t *testing.T) {
	handler, _ := threadCommentServer(t, http.StatusCreated, `{"id":902,"kind":"comment","topic_id":42,"visible_to":[]}`)
	styled, err := runStyledCommand(t, handler, "thread", "comment", "42", "-m", "Noted.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled, "Note 902 added to thread 42, visible only to you.") {
		t.Errorf("styled = %q", styled)
	}

	handler, _ = threadCommentServer(t, http.StatusCreated, `{"id":902,"kind":"comment","topic_id":42,"visible_to":[]}`)
	response, err := runJSONCommand(t, handler, "thread", "comment", "42", "-m", "Noted.")
	if err != nil {
		t.Fatal(err)
	}
	if visibleTo, ok := response.Data.(map[string]any)["visible_to"].([]any); !ok || len(visibleTo) != 0 {
		t.Errorf("visible_to = %#v, want []", response.Data.(map[string]any)["visible_to"])
	}
}

func TestThreadCommentDryRunShowsTheAudienceWithoutPosting(t *testing.T) {
	handler, sent := threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	response, err := runJSONCommand(t, handler, "thread", "comment", "42", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	calls, _, _, _, audience := sent.snapshot()
	if calls != 0 || audience != 1 {
		t.Fatalf("create calls = %d, audience calls = %d; want 0 and 1", calls, audience)
	}
	data := response.Data.(map[string]any)
	if data["dry_run"] != true || data["id"] != nil {
		t.Errorf("data = %#v", data)
	}
	visibleTo, ok := data["visible_to"].([]any)
	if !ok || len(visibleTo) != 1 || visibleTo[0].(map[string]any)["name"] != "Ana Pereira" {
		t.Errorf("visible_to = %#v", data["visible_to"])
	}

	handler, sent = threadCommentServer(t, http.StatusCreated, threadCommentNoteJSON)
	styled, err := runStyledCommand(t, handler, "thread", "comment", "42", "--dry-run", "-m", "not sent")
	if err != nil {
		t.Fatal(err)
	}
	if calls, _, _, _, _ := sent.snapshot(); calls != 0 {
		t.Errorf("a dry run with -m posted %d notes", calls)
	}
	if !strings.Contains(styled, "A note on thread 42 would be visible to you and Ana Pereira <ana@example.com>.") {
		t.Errorf("styled = %q", styled)
	}
}

func TestThreadCommentRefusesBlankBeforeSending(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing message", args: []string{"thread", "comment", "42"}, want: "a note is required"},
		{name: "blank message", args: []string{"thread", "comment", "42", "-m", "  \n "}, want: "a note is required"},
		{name: "blank html", args: []string{"thread", "comment", "42", "--message-html", " "}, want: "a note is required"},
		{name: "both flags", args: []string{"thread", "comment", "42", "-m", "a", "--message-html", "<p>b</p>"}, want: "none of the others can be"},
		{name: "invalid thread id", args: []string{"thread", "comment", "zero", "-m", "hi"}, want: "invalid thread ID: zero"},
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

func TestThreadCommentSurfacesHEYsValidationError(t *testing.T) {
	handler, _ := threadCommentServer(t, http.StatusUnprocessableEntity, `{"error":"Content can't be blank"}`)
	_, err := runJSONCommand(t, handler, "thread", "comment", "42", "--message-html", "<div></div>")
	if err == nil {
		t.Fatal("a 422 from HEY was reported as success")
	}
}
