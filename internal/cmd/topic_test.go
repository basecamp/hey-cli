package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// threadEntriesReads is what a thread's server was asked for.
type threadEntriesReads struct {
	mu       sync.Mutex
	entries  int
	messages int
}

func (r *threadEntriesReads) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries, r.messages
}

// threadEntriesServer answers a thread's entry pages and each entry's message. HEY serves
// the entry index newest first, a page at a time, so the pages here are given in that
// order.
//
// The index is paginated by geared_pagination, and this answers like it: the page is an
// opaque cursor out of the previous page's Link header, the last page carries no such
// header, and anything else — a page number, say — is answered with the first page all
// over again.
func threadEntriesServer(t *testing.T, pages [][]int64, bodies map[int64]string) (*httptest.Server, *threadEntriesReads) {
	t.Helper()
	reads := &threadEntriesReads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/topics/7/entries.json":
			reads.mu.Lock()
			reads.entries++
			reads.mu.Unlock()
			if len(pages) == 0 {
				fmt.Fprint(w, `[]`)
				return
			}
			index := threadEntriesPageIndex(len(pages), r.URL.Query().Get("page"))
			if index+1 < len(pages) {
				w.Header().Set("Link", fmt.Sprintf(`<http://%s/topics/7/entries.json?page=%s>; rel="next"`,
					r.Host, threadEntriesCursor(index+1)))
			}
			entries := make([]string, 0, len(pages[index]))
			for _, id := range pages[index] {
				entries = append(entries, fmt.Sprintf(
					`{"id":%d,"kind":"message","summary":"summary %d","created_at":"2026-04-%02dT09:30:00Z","creator":{"id":3,"name":"Rick Sanchez","email_address":"rick@example.com"}}`,
					id, id, 1+id%28))
			}
			fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			reads.mu.Lock()
			reads.messages++
			reads.mu.Unlock()
			var id int64
			_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/messages/"), ".json"), "%d", &id)
			body, ok := bodies[id]
			if !ok {
				t.Errorf("no body set up for message %d", id)
			}
			payload, _ := json.Marshal(map[string]any{"id": id, "content": body})
			_, _ = w.Write(payload)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, reads
}

func threadEntriesCursor(index int) string {
	return fmt.Sprintf("eyJwYWdlIjo%d", index)
}

func threadEntriesPageIndex(pages int, cursor string) int {
	for index := range pages {
		if threadEntriesCursor(index) == cursor {
			return index
		}
	}
	return 0
}

// readThreadEntries reads thread 7, the one every server here serves, with its bodies
// the way `hey threads` does.
func readThreadEntries(ctx context.Context) ([]threadEntry, error) {
	thread, err := loadThread(ctx, 7, true)
	if err != nil {
		return nil, err
	}
	return threadEntries(thread), nil
}

// A thread reads oldest first, however many pages HEY serves it in.
func TestEntriesInThreadReadsEveryPageOldestFirst(t *testing.T) {
	server, reads := threadEntriesServer(t,
		[][]int64{{13, 12}, {11}},
		map[int64]string{
			11: "<div>the first word</div>",
			12: "<div>the reply</div>",
			13: "<div>the last word</div>",
		})
	withSDKPointedAt(t, server)

	entries, err := readThreadEntries(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("read %d entries, want 3", len(entries))
	}
	for index, want := range []int64{11, 12, 13} {
		if entries[index].ID != want {
			t.Errorf("entry %d = %d, want %d", index, entries[index].ID, want)
		}
	}
	if entries[0].Body != "the first word" {
		t.Errorf("body = %q, want the oldest entry's", entries[0].Body)
	}
	if entries[0].CreatedAt != "2026-04-12T09:30" {
		t.Errorf("created_at = %q", entries[0].CreatedAt)
	}
	if entries[0].Creator.EmailAddress != "rick@example.com" {
		t.Errorf("creator = %+v", entries[0].Creator)
	}
	if entryReads, messageReads := reads.counts(); entryReads != 2 || messageReads != 3 {
		t.Errorf("read %d entry pages and %d messages, want 2 and 3", entryReads, messageReads)
	}
}

// A thread that fits on one page is read once. The page is a cursor, not a number, so a
// second read carrying "2" is answered with the first page again — asking for it costs
// another round trip and doubles the thread.
func TestEntriesInThreadReadsATwoEntryThreadOnce(t *testing.T) {
	server, reads := threadEntriesServer(t,
		[][]int64{{12, 11}},
		map[int64]string{
			11: "<div>the first word</div>",
			12: "<div>the reply</div>",
		})
	withSDKPointedAt(t, server)

	entries, err := readThreadEntries(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("read %d entries, want 2", len(entries))
	}
	if entries[0].ID != 11 || entries[1].ID != 12 {
		t.Errorf("entries = %d and %d, want 11 and 12", entries[0].ID, entries[1].ID)
	}
	if entryReads, messageReads := reads.counts(); entryReads != 1 || messageReads != 2 {
		t.Errorf("read %d entry pages and %d messages, want 1 and 2", entryReads, messageReads)
	}
}

// A thread longer than a page is read by following the cursor, so every entry arrives
// exactly once and in reading order.
func TestEntriesInThreadFollowsTheCursorThroughALongThread(t *testing.T) {
	newest := []int64{22, 21, 20, 19, 18, 17, 16, 15, 14, 13}
	oldest := []int64{12, 11}
	bodies := map[int64]string{}
	for _, id := range append(append([]int64{}, newest...), oldest...) {
		bodies[id] = fmt.Sprintf("<div>entry %d</div>", id)
	}
	server, reads := threadEntriesServer(t, [][]int64{newest, oldest}, bodies)
	withSDKPointedAt(t, server)

	entries, err := readThreadEntries(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 12 {
		t.Fatalf("read %d entries, want 12", len(entries))
	}
	for index := range entries {
		if want := int64(11 + index); entries[index].ID != want {
			t.Errorf("entry %d = %d, want %d", index, entries[index].ID, want)
		}
	}
	if entryReads, messageReads := reads.counts(); entryReads != 2 || messageReads != 12 {
		t.Errorf("read %d entry pages and %d messages, want 2 and 12", entryReads, messageReads)
	}
}

// A thread with nothing in it is a not-found rather than an empty success, so an agent
// is told the thread ID was wrong instead of that the thread is empty.
func TestEntriesInThreadWithoutEntries(t *testing.T) {
	server, _ := threadEntriesServer(t, nil, nil)
	withSDKPointedAt(t, server)

	if _, err := readThreadEntries(context.Background()); err == nil {
		t.Fatal("expected an error for a thread with no entries")
	}
}

// Bodies are Markdown, converted once here, and BodyHTML keeps HEY's original for
// --html.
func TestEntriesInThreadConvertsBodiesToMarkdown(t *testing.T) {
	const trix = `<h1>Quarterly planning</h1><p>See the <a href="https://example.com/plan">plan</a>.</p>`
	server, _ := threadEntriesServer(t, [][]int64{{11}}, map[int64]string{11: trix})
	withSDKPointedAt(t, server)

	entries, err := readThreadEntries(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(entries[0].Body, "# Quarterly planning") {
		t.Errorf("body = %q, want Markdown", entries[0].Body)
	}
	if !strings.Contains(entries[0].Body, "https://example.com/plan") {
		t.Errorf("body = %q, want the link to keep its URL", entries[0].Body)
	}
	if entries[0].BodyHTML != trix {
		t.Errorf("body_html = %q, want HEY's original", entries[0].BodyHTML)
	}
}

// An inbound HTML email arrives wrapped in a single trix attachment with no filename.
// That is the body, not a file, so the entry must carry it rather than falling back to
// HEY's truncated summary.
func TestEntriesInThreadReadsAnInboundEmailsEmbeddedBody(t *testing.T) {
	const inbound = `<div><figure data-trix-attachment='{"contentType":"text/html","content":"<shadow-content><template><p>Your invoice is ready.</p></template></shadow-content>","data":{}}'></figure></div>`
	server, _ := threadEntriesServer(t, [][]int64{{11}}, map[int64]string{11: inbound})
	withSDKPointedAt(t, server)

	entries, err := readThreadEntries(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(entries[0].Body, "Your invoice is ready.") {
		t.Errorf("body = %q, want the embedded email", entries[0].Body)
	}
}

func TestThreadEntrySender(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		entry threadEntry
		want  string
	}{
		{
			name:  "an alternative sender name wins",
			entry: threadEntry{AlternativeSenderName: "Support", Creator: threadContact{Name: "Rick Sanchez"}},
			want:  "Support",
		},
		{
			name:  "otherwise the creator's name",
			entry: threadEntry{Creator: threadContact{Name: "Rick Sanchez", EmailAddress: "rick@example.com"}},
			want:  "Rick Sanchez",
		},
		{
			name:  "and their address when they have no name",
			entry: threadEntry{Creator: threadContact{EmailAddress: "rick@example.com"}},
			want:  "rick@example.com",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := threadEntrySender(testCase.entry); got != testCase.want {
				t.Errorf("sender = %q, want %q", got, testCase.want)
			}
		})
	}
}
