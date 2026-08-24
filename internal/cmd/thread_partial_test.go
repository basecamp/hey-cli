package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// partialThreadServer serves thread 7 with pages of entries and answers the messages
// named in missing with 404, which the SDK does not retry, so a body that cannot be
// read fails fast.
func partialThreadServer(t *testing.T, pages [][]int64, missing ...int64) (*httptest.Server, *threadEntriesReads) {
	t.Helper()
	gone := map[int64]bool{}
	for _, id := range missing {
		gone[id] = true
	}
	reads := &threadEntriesReads{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/topics/7/entries.json":
			mu.Lock()
			reads.entries++
			mu.Unlock()
			index := threadEntriesPageIndex(len(pages), r.URL.Query().Get("page"))
			if index+1 < len(pages) {
				w.Header().Set("Link", fmt.Sprintf(`<http://%s/topics/7/entries.json?page=%s>; rel="next"`, r.Host, threadEntriesCursor(index+1)))
			}
			entries := make([]string, 0, len(pages[index]))
			for _, id := range pages[index] {
				entries = append(entries, fmt.Sprintf(`{"id":%d,"kind":"message","summary":"preview %d","creator":{"id":3,"name":"Rick Sanchez","email_address":"rick@example.com"}}`, id, id))
			}
			fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			mu.Lock()
			reads.messages++
			mu.Unlock()
			var id int64
			_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/messages/"), ".json"), "%d", &id)
			if gone[id] {
				http.Error(w, `{"error":"gone"}`, http.StatusNotFound)
				return
			}
			payload, _ := json.Marshal(map[string]any{"id": id, "content": fmt.Sprintf("<p>body %d</p>", id)})
			_, _ = w.Write(payload)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, reads
}

func withThreadLimits(t *testing.T, limits threadload.Limits) {
	t.Helper()
	previous := threadLimits
	threadLimits = limits
	t.Cleanup(func() { threadLimits = previous })
}

// decodedEntry is a thread entry as --json carries it, read back with a plain string
// body: only ToMarkdown can make an htmlutil.Markdown, so the decoded form is not one.
type decodedEntry struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	BodyState string `json:"body_state"`
}

type threadResponse struct {
	OK     bool           `json:"ok"`
	Data   []decodedEntry `json:"data"`
	Notice string         `json:"notice"`
}

func decodeThread(t *testing.T, stdout string) threadResponse {
	t.Helper()
	var response threadResponse
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	return response
}

// --json carries a body only for an entry that has one: a body that was not read is
// omitted rather than written as "", which is the contract a decoder reading the key's
// presence depends on. decodedEntry cannot tell the two apart, so this reads the raw
// objects.
func TestThreadsJSONOmitsABodyItDidNotRead(t *testing.T) {
	server, _ := partialThreadServer(t, [][]int64{{13, 12, 11}}, 12)
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7", "--allow-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var response struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if len(response.Data) != 3 {
		t.Fatalf("got %d entries, want 3", len(response.Data))
	}
	for _, entry := range response.Data {
		body, present := entry["body"]
		switch entry["id"] {
		case float64(12):
			if present {
				t.Errorf("entry 12 carries body %#v, want the key omitted for a body that was not read", body)
			}
		default:
			if text, ok := body.(string); !ok || text == "" {
				t.Errorf("entry %v carries body %#v, want a Markdown string", entry["id"], body)
			}
		}
	}
}

func partialError(t *testing.T, err error) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierr.CodeAPI || !strings.Contains(apiErr.Message, "read only in part") {
		t.Fatalf("error = %v, want the partial-thread refusal", err)
	}
	if !strings.Contains(apiErr.Hint, "--allow-partial") {
		t.Errorf("hint = %q, want the flag named", apiErr.Hint)
	}
}

// A body that could not be read makes the thread partial. Without --allow-partial that
// is a refusal with nothing on stdout; with it, the thread comes with a notice and the
// entry says its body failed rather than passing a preview off as the message.
func TestThreadsRefusesAPartialThreadUnlessAllowed(t *testing.T) {
	server, _ := partialThreadServer(t, [][]int64{{13, 12, 11}}, 12)
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	partialError(t, err)
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing on a refusal", stdout)
	}

	stdout, _, err = runCLIRaw(t, server, "--json", "thread", "read", "7", "--allow-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	response := decodeThread(t, stdout)
	if !response.OK || len(response.Data) != 3 {
		t.Fatalf("response = %+v", response)
	}
	if !strings.Contains(response.Notice, "1 of 3 bodies could not be read (failed)") {
		t.Errorf("notice = %q", response.Notice)
	}
	for _, entry := range response.Data {
		switch entry.ID {
		case 12:
			if entry.BodyState != "failed" || entry.Body != "" {
				t.Errorf("entry 12 = %+v, want failed with no body", entry)
			}
		default:
			if entry.BodyState != "hydrated" || entry.Body == "" {
				t.Errorf("entry %d = %+v, want hydrated", entry.ID, entry)
			}
		}
	}
}

func TestThreadsStyledMarksAnUnreadBodyAndNeverPreviewsIt(t *testing.T) {
	server, _ := partialThreadServer(t, [][]int64{{12, 11}}, 12)
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--styled", "thread", "read", "7", "--allow-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "(body not read: failed)") {
		t.Errorf("stdout = %q, want the omission marked", stdout)
	}
	if strings.Contains(stdout, "preview 12") {
		t.Errorf("stdout = %q, must not show the summary as if it were the body", stdout)
	}
	if !strings.Contains(stdout, "notice: 1 of 2 bodies could not be read") {
		t.Errorf("stdout = %q, want the notice", stdout)
	}
}

// A count or a list of IDs reads the index and no message: a body that would fail to
// read cannot make them partial.
func TestThreadsCountAndIDsDoNotReadBodies(t *testing.T) {
	for _, flag := range []string{"--count", "--ids-only"} {
		server, reads := partialThreadServer(t, [][]int64{{13, 12, 11}}, 12)
		stdoutTerminal(t, false)

		stdout, _, err := runCLIRaw(t, server, flag, "thread", "read", "7")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", flag, err)
		}
		if _, messages := reads.counts(); messages != 0 {
			t.Errorf("%s: read %d messages, want none", flag, messages)
		}
		want := "3\n"
		if flag == "--ids-only" {
			want = "11\n12\n13\n"
		}
		if stdout != want {
			t.Errorf("%s: stdout = %q, want %q", flag, stdout, want)
		}
	}
}

// An index past the page limit is partial in every format, including the ones that read
// no bodies; the data-only formats carry the notice on stderr.
func TestThreadsIndexTruncationIsPartialInEveryFormat(t *testing.T) {
	limits := threadload.DefaultLimits
	limits.MaxPages = 1
	withThreadLimits(t, limits)
	stdoutTerminal(t, false)

	for _, flag := range []string{"--json", "--count", "--ids-only"} {
		server, _ := partialThreadServer(t, [][]int64{{13, 12}, {11}})
		_, _, err := runCLIRaw(t, server, flag, "thread", "read", "7")
		partialError(t, err)

		stdout, stderr, err := runCLIRaw(t, server, flag, "thread", "read", "7", "--allow-partial")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", flag, err)
		}
		switch flag {
		case "--json":
			if response := decodeThread(t, stdout); len(response.Data) != 2 || !strings.Contains(response.Notice, "only the newest 2 entries were read") {
				t.Errorf("response = %+v", response)
			}
		case "--count":
			if stdout != "2\n" || !strings.Contains(stderr, "notice: only the newest 2 entries were read") {
				t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
			}
		case "--ids-only":
			if stdout != "12\n13\n" || !strings.Contains(stderr, "notice:") {
				t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
			}
		}
	}
}

func TestThreadsMarksEntriesOverTheRequestLimit(t *testing.T) {
	limits := threadload.DefaultLimits
	limits.MaxMessageRequests = 2
	withThreadLimits(t, limits)
	server, reads := partialThreadServer(t, [][]int64{{13, 12, 11}})
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7", "--allow-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	response := decodeThread(t, stdout)
	if !strings.Contains(response.Notice, "1 of 3 bodies could not be read (over the read limits)") {
		t.Errorf("notice = %q", response.Notice)
	}
	// Admission is newest first, so the oldest entry is the one over the limit.
	if response.Data[0].ID != 11 || response.Data[0].BodyState != "over_limit" {
		t.Errorf("oldest entry = %+v, want over_limit", response.Data[0])
	}
	if _, messages := reads.counts(); messages != 2 {
		t.Errorf("read %d messages, want 2", messages)
	}
}

// Attachments live in message HTML, so every attachments format reads bodies, and a
// body that could not be read is a partial listing under the same rule.
func TestAttachmentsReadBodiesInEveryFormatAndRefusePartialUnlessAllowed(t *testing.T) {
	for _, flag := range []string{"--json", "--count", "--ids-only"} {
		server, reads := partialThreadServer(t, [][]int64{{12, 11}}, 12)
		stdoutTerminal(t, false)

		_, _, err := runCLIRaw(t, server, flag, "attachment", "list", "7")
		partialError(t, err)
		if _, messages := reads.counts(); messages == 0 {
			t.Errorf("%s: read no messages, want the bodies read", flag)
		}

		stdout, stderr, err := runCLIRaw(t, server, flag, "attachment", "list", "7", "--allow-partial")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", flag, err)
		}
		if flag == "--json" {
			var response struct{ Notice string }
			if err := json.Unmarshal([]byte(stdout), &response); err != nil || !strings.Contains(response.Notice, "1 of 2 bodies could not be read") {
				t.Errorf("%s: stdout = %q, err = %v", flag, stdout, err)
			}
		} else if !strings.Contains(stderr, "notice: 1 of 2 bodies could not be read") {
			t.Errorf("%s: stderr = %q, want the notice", flag, stderr)
		}
	}
}

// The formats whose output cannot carry a notice get it on stderr — --quiet and --html
// included, not only the count and the IDs.
func TestThreadsPartialNoticeReachesStderrForQuietAndHTML(t *testing.T) {
	limits := threadload.DefaultLimits
	limits.MaxPages = 1
	withThreadLimits(t, limits)
	stdoutTerminal(t, false)

	for _, flag := range []string{"--quiet", "--html"} {
		server, _ := partialThreadServer(t, [][]int64{{13, 12}, {11}})
		stdout, stderr, err := runCLIRaw(t, server, flag, "thread", "read", "7", "--allow-partial")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", flag, err)
		}
		if !strings.Contains(stderr, "notice: only the newest 2 entries were read") {
			t.Errorf("%s: stderr = %q, want the notice", flag, stderr)
		}
		if stdout == "" {
			t.Errorf("%s: stdout empty, want the partial output", flag)
		}
	}
}

// A body that was read and rendered to nothing is an empty body, not an unread one.
func TestThreadsStyledCallsAnEmptyHydratedBodyEmpty(t *testing.T) {
	server, _ := threadEntriesServer(t, [][]int64{{11}}, map[int64]string{11: "<p><br></p>"})
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--styled", "thread", "read", "7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "(empty body)") || strings.Contains(stdout, "not read") {
		t.Errorf("stdout = %q", stdout)
	}
}

// A server error is systemic: the fan-out stops and the command fails rather than
// reporting a partial thread over hundreds of failing requests.
func TestThreadsStopOnAServerError(t *testing.T) {
	reads := &threadEntriesReads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/7/entries.json":
			fmt.Fprint(w, `[{"id":13,"kind":"message"},{"id":12,"kind":"message"},{"id":11,"kind":"message"}]`)
		default:
			reads.mu.Lock()
			reads.messages++
			reads.mu.Unlock()
			http.Error(w, `{"error":"down"}`, http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)
	limits := threadload.DefaultLimits
	limits.Concurrency = 1
	withThreadLimits(t, limits)
	stdoutTerminal(t, false)

	_, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7", "--allow-partial")
	if !errors.Is(err, threadload.ErrSystemic) {
		t.Fatalf("error = %v, want the systemic error even with --allow-partial", err)
	}
}

// A message the transport refuses as too large is over_limit for that entry alone:
// read once, not retried, and not the command's error.
func TestThreadsMarkAnOversizedMessageOverLimit(t *testing.T) {
	reads := &threadEntriesReads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/7/entries.json":
			fmt.Fprint(w, `[{"id":12,"kind":"message"},{"id":11,"kind":"message"}]`)
		case "/messages/12.json":
			reads.mu.Lock()
			reads.messages++
			reads.mu.Unlock()
			w.Header().Set("Content-Length", strconv.FormatInt(hey.DefaultMaxResponseBodyBytes+1, 10))
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":12,"content":"`)
		default:
			fmt.Fprint(w, `{"id":11,"content":"<p>fits</p>"}`)
		}
	}))
	t.Cleanup(server.Close)
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7", "--allow-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	response := decodeThread(t, stdout)
	if response.Data[1].ID != 12 || response.Data[1].BodyState != "over_limit" || response.Data[0].BodyState != "hydrated" {
		t.Errorf("response = %+v", response.Data)
	}
	if _, messages := reads.counts(); messages != 1 {
		t.Errorf("read the oversized message %d times, want once", messages)
	}
}
