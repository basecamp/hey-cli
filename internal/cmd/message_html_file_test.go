package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMessageHTMLFileReadsExactContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.html")
	want := "<h1>Quarterly results</h1>\n<p>Revenue grew <strong>12%</strong>.</p>\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readMessageHTMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("content = %q, want exact file bytes %q", got, want)
	}
}

func TestReadMessageHTMLFileRejectsInvalidPaths(t *testing.T) {
	invalidUTF8 := filepath.Join(t.TempDir(), "invalid-utf8.html")
	if err := os.WriteFile(invalidUTF8, []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "empty path", path: "", want: "requires a path"},
		{name: "missing file", path: filepath.Join(t.TempDir(), "missing.html"), want: "could not inspect HTML message file"},
		{name: "directory", path: t.TempDir(), want: "is not a regular file"},
		{name: "invalid UTF-8", path: invalidUTF8, want: "is not valid UTF-8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readMessageHTMLFile(test.path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want to contain %q", err, test.want)
			}
		})
	}
}

func TestMessageHTMLFileIsExclusiveWithEveryOtherMessageSource(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "compose Markdown", args: []string{"compose", "--thread-id", "7", "-m", "Hello", "--message-html-file", "message.html"}},
		{name: "compose inline HTML", args: []string{"compose", "--thread-id", "7", "--message-html", "<p>Hello</p>", "--message-html-file", "message.html"}},
		{name: "reply Markdown", args: []string{"reply", "7", "-m", "Hello", "--message-html-file", "message.html"}},
		{name: "reply inline HTML", args: []string{"reply", "7", "--message-html", "<p>Hello</p>", "--message-html-file", "message.html"}},
		{name: "forward Markdown", args: []string{"forward", "7", "--to", "alex@example.com", "-m", "Hello", "--message-html-file", "message.html"}},
		{name: "forward inline HTML", args: []string{"forward", "7", "--to", "alex@example.com", "--message-html", "<p>Hello</p>", "--message-html-file", "message.html"}},
		{name: "bulk reply Markdown", args: []string{"bulk-reply", "send", "7", "-m", "Hello", "--message-html-file", "message.html"}},
		{name: "bulk reply inline HTML", args: []string{"bulk-reply", "send", "7", "--message-html", "<p>Hello</p>", "--message-html-file", "message.html"}},
		{name: "draft edit Markdown", args: []string{"draft", "edit", "7", "-m", "Hello", "--message-html-file", "message.html"}},
		{name: "draft edit inline HTML", args: []string{"draft", "edit", "7", "--message-html", "<p>Hello</p>", "--message-html-file", "message.html"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runCLI(t, server, test.args...)
			if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
				t.Fatalf("error = %v, want mutually exclusive flags refused", err)
			}
		})
	}
	if requests != 0 {
		t.Errorf("mutually exclusive input made %d server requests, want 0", requests)
	}
}
