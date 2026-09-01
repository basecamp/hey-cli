package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// threadCommentServer answers the fixture topic 42 (account 9) and records what a
// comment post carried. GET /imbox fails the test outright: a comment redirects to
// /imbox and the CLI must not follow it.
func threadCommentServer(t *testing.T) (http.Handler, *sentComment) {
	t.Helper()
	sent := &sentComment{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/topics/42.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"account_id":9}`))
		case r.Method == http.MethodPost && r.URL.Path == "/topics/42/comments":
			if got := r.URL.Query().Get("account_id"); got != "9" {
				t.Errorf("account_id = %q, want 9", got)
			}
			if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
				t.Errorf("content-type = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "*/*" {
				t.Errorf("accept = %q", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			sent.Content = r.PostForm.Get("comment[content]")
			sent.Called = true
			w.Header().Set("Location", "/imbox")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/imbox" || r.URL.Path == "/imbox.json":
			t.Fatal("comment redirect was followed")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	return handler, sent
}

type sentComment struct {
	Called  bool
	Content string
}

func TestThreadCommentPostsPlainTextNote(t *testing.T) {
	handler, sent := threadCommentServer(t)
	response, err := runJSONCommand(t, handler, "thread", "comment", "42", "-m", "Following up with accounting on this.")
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Called {
		t.Fatal("expected a comment to be posted")
	}
	if sent.Content != "Following up with accounting on this." {
		t.Errorf("content = %q", sent.Content)
	}
	if response.Summary != "Comment added to thread 42" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Data.(map[string]any)["thread_id"] != float64(42) {
		t.Errorf("data = %#v", response.Data)
	}
}

func TestThreadCommentSendsMarkdownLookingTextVerbatim(t *testing.T) {
	handler, sent := threadCommentServer(t)
	_, err := runJSONCommand(t, handler, "thread", "comment", "42", "-m", "**Not** converted, _as-is_.")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Content != "**Not** converted, _as-is_." {
		t.Errorf("content = %q, want the Markdown-looking text sent verbatim", sent.Content)
	}
}

func TestThreadCommentStyledOutput(t *testing.T) {
	handler, sent := threadCommentServer(t)
	styled, err := runStyledCommand(t, handler, "thread", "comment", "42", "-m", "Noted.")
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Called {
		t.Fatal("expected a comment to be posted")
	}
	if !strings.Contains(styled, "Comment added to thread 42") {
		t.Errorf("styled = %q", styled)
	}
}

func TestThreadCommentValidatesInput(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing message", args: []string{"thread", "comment", "42"}, want: "--message is required"},
		{name: "empty message", args: []string{"thread", "comment", "42", "-m", "   "}, want: "--message is required"},
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
