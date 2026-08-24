package smoke_test

import (
	"strconv"
	"strings"
	"testing"
)

type smokeSnippet struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	ContentHTML string `json:"content_html"`
}

func TestSnippetLifecycle(t *testing.T) {
	uid := uniqueID()
	name := "Scheduling reply " + uid
	content := "Tuesday afternoon works for me."
	_, stderr, code := hey(t, "snippet", "create", "--name", name, "--content", content, "--json")
	if code != 0 {
		skipf(t, "snippet create unavailable (exit %d): %s", code, stderr)
	}

	snippet := findSnippetByName(t, name)
	if snippet.ID == 0 || snippet.Content != content || !strings.Contains(snippet.ContentHTML, content) {
		t.Fatalf("created snippet = %+v", snippet)
	}
	id := strconv.FormatInt(snippet.ID, 10)
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			_, _, _ = hey(t, "snippet", "delete", id)
		}
	})

	updatedName := "Meeting availability " + uid
	updatedContent := "Wednesday morning works for me."
	_, stderr, code = hey(t, "snippet", "update", id, "--name", updatedName, "--content", updatedContent, "--json")
	if code != 0 {
		t.Fatalf("snippet update failed (exit %d): %s", code, stderr)
	}
	updated := findSnippetByName(t, updatedName)
	if updated.ID != snippet.ID || updated.Content != updatedContent || !strings.Contains(updated.ContentHTML, updatedContent) {
		t.Fatalf("updated snippet = %+v", updated)
	}

	page := browserPageText(t, baseURL+"/snippets")
	if !strings.Contains(page, updatedName) {
		t.Errorf("browser snippets page did not show %q", updatedName)
	}

	_, stderr, code = hey(t, "snippet", "delete", id, "--json")
	if code != 0 {
		t.Fatalf("snippet delete failed (exit %d): %s", code, stderr)
	}
	deleted = true
	for _, listed := range listSnippets(t) {
		if listed.ID == snippet.ID {
			t.Fatalf("deleted snippet %d is still listed", snippet.ID)
		}
	}
}

func TestSnippetOutputFormatsAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"snippet", "list", "--quiet"},
		{"snippet", "list", "--ids-only"},
		{"snippet", "list", "--count"},
		{"snippet", "list", "--markdown"},
		{"snippet", "list", "--styled"},
	} {
		_, stderr, code := hey(t, args...)
		if code != 0 {
			t.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
		}
	}

	heyFail(t, "snippet", "create", "--name", "Scheduling reply")
	heyFail(t, "snippet", "create", "--content", "Tuesday works for me.")
	heyFail(t, "snippet", "update", "44")
	heyFail(t, "snippet", "delete", "not-an-id")
}

func listSnippets(t *testing.T) []smokeSnippet {
	t.Helper()
	return dataAs[[]smokeSnippet](t, heyJSON(t, "snippet", "list"))
}

func findSnippetByName(t *testing.T, name string) smokeSnippet {
	t.Helper()
	for _, snippet := range listSnippets(t) {
		if snippet.Name == name {
			return snippet
		}
	}
	t.Fatalf("snippet %q not found", name)
	return smokeSnippet{}
}
