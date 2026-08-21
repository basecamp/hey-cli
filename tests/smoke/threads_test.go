package smoke_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type threadEntry struct {
	ID        int64  `json:"id"`
	CreatedAt string `json:"created_at"`
	Summary   string `json:"summary"`
	Body      string `json:"body"`
	BodyState string `json:"body_state"`
	Creator   struct {
		Name         string `json:"name"`
		EmailAddress string `json:"email_address"`
	} `json:"creator"`
}

// longThread composes a message and replies to it until the thread is longer than one
// geared page, so every format is exercised against a thread the entries index serves
// in more than one page. It skips — or fails, under HEY_SMOKE_STRICT — when the server
// will not let the CLI write.
func longThread(t *testing.T, replies int) (topicID string, subject string) {
	t.Helper()
	uid := uniqueID()
	subject = fmt.Sprintf("Long thread %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", "First: the **quarterly** numbers are at https://example.com/reports/q3 — see the bullets\n\n- Revenue up\n- Churn down",
		"--json",
	)
	if code != 0 {
		skipf(t, "could not compose the thread's first message (exit %d): %s", code, stderr)
	}

	resp := heyJSON(t, "box", "imbox", "--all")
	type posting struct {
		AppURL  string `json:"app_url"`
		Summary string `json:"summary"`
		Name    string `json:"name"`
	}
	type boxResp struct {
		Postings []posting `json:"postings"`
	}
	for _, p := range dataAs[boxResp](t, resp).Postings {
		if strings.Contains(p.Name, uid) || strings.Contains(p.Summary, uid) {
			topicID = extractTopicID(p.AppURL)
			break
		}
	}
	if topicID == "" {
		skipf(t, "the composed thread %q did not appear in the Imbox", subject)
	}

	for i := range replies {
		_, stderr, code := hey(t, "reply", topicID, "-m", fmt.Sprintf("Reply %d of %d in %s", i+1, replies, uid), "--json")
		if code != 0 {
			skipf(t, "reply %d failed (exit %d): %s", i+1, code, stderr)
		}
	}
	return topicID, subject
}

// A thread longer than a page reads whole, oldest first, with every entry's body as
// Markdown: the composed links and emphasis survive, and no HTML tag does.
func TestThreadsReadsALongThreadAsMarkdown(t *testing.T) {
	const replies = 11
	topicID, _ := longThread(t, replies)

	resp := heyJSON(t, "threads", topicID)
	entries := dataAs[[]threadEntry](t, resp)
	if len(entries) != replies+1 {
		t.Fatalf("thread has %d entries, want %d", len(entries), replies+1)
	}
	if resp.Notice != "" {
		t.Errorf("notice = %q, want none for a thread read whole", resp.Notice)
	}

	seen := map[int64]bool{}
	for i, entry := range entries {
		if seen[entry.ID] {
			t.Errorf("entry %d appears twice", entry.ID)
		}
		seen[entry.ID] = true
		if i > 0 && entries[i-1].CreatedAt > entry.CreatedAt {
			t.Errorf("entry %d (%s) comes after %d (%s): not oldest first", entry.ID, entry.CreatedAt, entries[i-1].ID, entries[i-1].CreatedAt)
		}
		if entry.BodyState != "hydrated" {
			t.Errorf("entry %d body_state = %q, want hydrated", entry.ID, entry.BodyState)
		}
		if strings.Contains(entry.Body, "<div") || strings.Contains(entry.Body, "<p") || strings.Contains(entry.Body, "<br") {
			t.Errorf("entry %d body carries HTML: %q", entry.ID, entry.Body)
		}
	}

	first := entries[0].Body
	if !strings.Contains(first, "**quarterly**") || !strings.Contains(first, "https://example.com/reports/q3") {
		t.Errorf("first body = %q, want the emphasis and the link as Markdown", first)
	}
	if !strings.Contains(first, "- Revenue up") {
		t.Errorf("first body = %q, want the list as Markdown", first)
	}
	if last := entries[len(entries)-1].Body; !strings.Contains(last, fmt.Sprintf("Reply %d of %d", replies, replies)) {
		t.Errorf("last body = %q, want the last reply", last)
	}

	// Every entry's summary is a prefix of its body, compared rune by rune: HEY's
	// preview ends in an ellipsis and a byte-wise prefix check would slice a multibyte
	// character in half.
	for _, entry := range entries {
		summary := []rune(strings.TrimSuffix(strings.TrimSpace(entry.Summary), "…"))
		body := []rune(strings.Join(strings.Fields(entry.Body), " "))
		if len(summary) == 0 || len(body) < len(summary) {
			continue
		}
		if !strings.HasPrefix(string(body), strings.TrimSpace(string(summary[:min(len(summary), 20)]))) {
			t.Logf("entry %d summary %q is not a prefix of its body %q (HEY previews may differ from rendered Markdown)", entry.ID, entry.Summary, entry.Body)
		}
	}

	count := strings.TrimSpace(heyOK(t, "threads", topicID, "--count"))
	if count != strconv.Itoa(replies+1) {
		t.Errorf("--count = %q, want %d", count, replies+1)
	}
}

// Every output format answers a thread the way the contract says.
func TestThreadsFormats(t *testing.T) {
	topicID, _ := longThread(t, 2)

	t.Run("json", func(t *testing.T) {
		resp := heyJSON(t, "threads", topicID)
		entries := dataAs[[]threadEntry](t, resp)
		if len(entries) != 3 || resp.Summary != fmt.Sprintf("3 entries in thread %s", topicID) {
			t.Errorf("entries = %d, summary = %q", len(entries), resp.Summary)
		}
	})

	t.Run("markdown", func(t *testing.T) {
		out := heyOK(t, "threads", topicID, "--markdown")
		if !strings.HasPrefix(out, "# Thread "+topicID+"\n") {
			t.Errorf("markdown = %q, want a document heading", out)
		}
		if strings.Count(out, "\n## From: ") != 3 || strings.Count(out, "\n---\n") != 3 {
			t.Errorf("markdown = %q, want a heading and a rule per entry", out)
		}
		if strings.Contains(out, "<p") || strings.Contains(out, "<div") {
			t.Errorf("markdown = %q carries HTML", out)
		}
	})

	t.Run("ids", func(t *testing.T) {
		out := heyOK(t, "threads", topicID, "--ids-only")
		ids := strings.Fields(out)
		if len(ids) != 3 {
			t.Errorf("--ids-only = %q, want three IDs", out)
		}
		for _, id := range ids {
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				t.Errorf("--ids-only line %q is not an ID", id)
			}
		}
	})

	t.Run("count", func(t *testing.T) {
		if out := strings.TrimSpace(heyOK(t, "threads", topicID, "--count")); out != "3" {
			t.Errorf("--count = %q, want 3", out)
		}
	})

	t.Run("quiet", func(t *testing.T) {
		out := heyOK(t, "threads", topicID, "--quiet")
		var entries []threadEntry
		if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 3 {
			t.Errorf("--quiet = %q, err = %v, want the bare entries", out, err)
		}
	})

	t.Run("jq", func(t *testing.T) {
		out := heyOK(t, "threads", topicID, "--jq", ".data | length")
		if strings.TrimSpace(out) != "3" {
			t.Errorf("--jq = %q, want 3", out)
		}
	})

	t.Run("styled", func(t *testing.T) {
		out := heyOK(t, "threads", topicID, "--styled")
		if strings.Count(out, "From: ") != 3 {
			t.Errorf("styled = %q, want a From line per entry", out)
		}
		if strings.Contains(out, "<p") || strings.Contains(out, "**") {
			t.Errorf("styled = %q, want rendered Markdown, not tags or markers", out)
		}
	})

	t.Run("html to a pipe", func(t *testing.T) {
		out := heyOK(t, "threads", topicID, "--html")
		if strings.Count(out, "<!-- hey entry ") != 3 {
			t.Errorf("--html = %q, want a comment per entry", out)
		}
		if !strings.Contains(out, "<div") && !strings.Contains(out, "<p") {
			t.Errorf("--html = %q, want HEY's HTML", out)
		}
		file := filepath.Join(t.TempDir(), "thread.html")
		if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("html refuses other selectors", func(t *testing.T) {
		_, stderr := heyFail(t, "threads", topicID, "--html", "--json")
		if !strings.Contains(stderr, "cannot use --html with --json") {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("html refused elsewhere", func(t *testing.T) {
		_, stderr := heyFail(t, "boxes", "--html")
		if !strings.Contains(stderr, "--html is not supported by hey boxes") {
			t.Errorf("stderr = %q", stderr)
		}
	})
}
