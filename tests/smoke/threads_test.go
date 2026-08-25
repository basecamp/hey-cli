package smoke_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
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

// firstMessage is the Markdown the long thread starts with. compose sends -m as
// Markdown, so in the thread the emphasis and the list survive as structure, and the
// bare URL stays a literal the reader can follow.
const firstMessage = "First: the **quarterly** numbers are at https://example.com/reports/q3 — see the bullets\n\n- Revenue up\n- Churn down"

// longThread composes a message and replies to it until the thread is longer than one
// geared page, so every format is exercised against a thread the entries index serves
// in more than one page. It skips — or fails, under HEY_SMOKE_STRICT — when the server
// will not let the CLI write, and trashes the thread when the test is done.
func longThread(t *testing.T, replies int) (topicID string, subject string) {
	t.Helper()
	uid := uniqueID()
	subject = fmt.Sprintf("Long thread %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", firstMessage,
		"--json",
	)
	if code != 0 {
		skipf(t, "could not compose the thread's first message (exit %d): %s", code, stderr)
	}
	// Delivery to the Imbox is asynchronous; wait for the posting the way the other
	// mutation tests do. The cleanup is registered once the posting is known to exist,
	// so a thread that never arrived does not turn a skip into a cleanup failure.
	if _, err := waitForPostingIDBySubject(t, subject); err != nil {
		skipf(t, "the composed thread %q did not appear in the Imbox: %v", subject, err)
	}
	t.Cleanup(func() { cleanupThreadBySubject(t, subject) })
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
		skipf(t, "the composed thread %q has no topic in the Imbox", subject)
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
// Markdown: the composed text survives as prose, its URL intact, and no HTML tag does.
func TestThreadsReadsALongThreadAsMarkdown(t *testing.T) {
	const replies = 11
	topicID, _ := longThread(t, replies)

	resp := heyJSON(t, "thread", "read", topicID)
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

	// The message went in as Markdown, so it comes back as Markdown: the emphasis
	// and the list are structure, the URL is the literal it was, and the em dash —
	// a multibyte character — survives intact.
	first := entries[0].Body
	for _, want := range []string{"**quarterly**", "https://example.com/reports/q3", "— see the bullets", "- Revenue up", "- Churn down"} {
		if !strings.Contains(first, want) {
			t.Errorf("first body = %q, want %q in it", first, want)
		}
	}
	if strings.Contains(first, `\*\*quarterly\*\*`) {
		t.Errorf("first body = %q, want emphasis, not escaped asterisks", first)
	}
	if last := entries[len(entries)-1].Body; !strings.Contains(last, fmt.Sprintf("Reply %d of %d", replies, replies)) {
		t.Errorf("last body = %q, want the last reply", last)
	}
	for _, entry := range entries {
		if !utf8.ValidString(entry.Summary) || !utf8.ValidString(entry.Body) {
			t.Errorf("entry %d carries invalid UTF-8: summary %q body %q", entry.ID, entry.Summary, entry.Body)
		}
	}

	count := strings.TrimSpace(heyOK(t, "thread", "read", topicID, "--count"))
	if count != strconv.Itoa(replies+1) {
		t.Errorf("--count = %q, want %d", count, replies+1)
	}
}

// Every output format answers the same thread — one longer than a geared page — the
// way the contract says.
func TestThreadsFormats(t *testing.T) {
	const replies = 11
	const entries = replies + 1
	topicID, _ := longThread(t, replies)

	t.Run("json", func(t *testing.T) {
		resp := heyJSON(t, "thread", "read", topicID)
		got := dataAs[[]threadEntry](t, resp)
		if len(got) != entries || resp.Summary != fmt.Sprintf("%d entries in thread %s", entries, topicID) {
			t.Errorf("entries = %d, summary = %q", len(got), resp.Summary)
		}
	})

	t.Run("markdown", func(t *testing.T) {
		out := heyOK(t, "thread", "read", topicID, "--markdown")
		if !strings.HasPrefix(out, "# Thread "+topicID+"\n") {
			t.Errorf("markdown = %q, want a document heading", out)
		}
		if strings.Count(out, "\n## From: ") != entries || strings.Count(out, "\n---\n") != entries {
			t.Errorf("markdown = %q, want a heading and a rule per entry", out)
		}
		if strings.Contains(out, "<p") || strings.Contains(out, "<div") {
			t.Errorf("markdown = %q carries HTML", out)
		}
	})

	t.Run("ids", func(t *testing.T) {
		out := heyOK(t, "thread", "read", topicID, "--ids-only")
		ids := strings.Fields(out)
		if len(ids) != entries {
			t.Errorf("--ids-only = %q, want %d IDs", out, entries)
		}
		for _, id := range ids {
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				t.Errorf("--ids-only line %q is not an ID", id)
			}
		}
	})

	t.Run("count", func(t *testing.T) {
		if out := strings.TrimSpace(heyOK(t, "thread", "read", topicID, "--count")); out != strconv.Itoa(entries) {
			t.Errorf("--count = %q, want %d", out, entries)
		}
	})

	t.Run("quiet", func(t *testing.T) {
		out := heyOK(t, "thread", "read", topicID, "--quiet")
		var got []threadEntry
		if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != entries {
			t.Errorf("--quiet = %q, err = %v, want the bare entries", out, err)
		}
	})

	t.Run("jq", func(t *testing.T) {
		out := heyOK(t, "thread", "read", topicID, "--jq", ".data | length")
		if strings.TrimSpace(out) != strconv.Itoa(entries) {
			t.Errorf("--jq = %q, want %d", out, entries)
		}
	})

	t.Run("styled", func(t *testing.T) {
		out := heyOK(t, "thread", "read", topicID, "--styled")
		if strings.Count(out, "From: ") != entries {
			t.Errorf("styled = %q, want a From line per entry", out)
		}
		// The composed text had literal asterisks, which render as the asterisks they
		// are; what must not appear is markup — a tag, or a backslash escape left
		// unrendered.
		if strings.Contains(out, "<p") || strings.Contains(out, `\*`) {
			t.Errorf("styled = %q, want rendered Markdown, not tags or unrendered escapes", out)
		}
	})

	t.Run("html to a pipe", func(t *testing.T) {
		out := heyOK(t, "thread", "read", topicID, "--html")
		if !strings.HasPrefix(out, "<!doctype html>\n") || !strings.Contains(out, `<meta charset="utf-8">`) || !strings.HasSuffix(out, "</body>\n</html>\n") {
			t.Errorf("--html = %q, want an HTML document declaring its charset", out)
		}
		if strings.Count(out, "<article ") != entries {
			t.Errorf("--html = %q, want an <article> per entry", out)
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
		_, stderr := heyFail(t, "thread", "read", topicID, "--html", "--json")
		if !strings.Contains(stderr, "cannot use --html with --json") {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("html refused elsewhere", func(t *testing.T) {
		_, stderr := heyFail(t, "box", "list", "--html")
		if !strings.Contains(stderr, "--html is not supported by hey box list") {
			t.Errorf("stderr = %q", stderr)
		}
	})
}
