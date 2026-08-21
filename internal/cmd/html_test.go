package cmd

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// runCLIRaw drives the root command against a test server with exactly the arguments
// given, returning what went to stdout and to stderr separately.
func runCLIRaw(t *testing.T, server *httptest.Server, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--base-url", server.URL}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func stdoutTerminal(t *testing.T, isTerminal bool) {
	t.Helper()
	previous := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTerminal }
	t.Cleanup(func() { stdoutIsTerminal = previous })
}

func usageMessage(t *testing.T, err error) string {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierr.CodeUsage {
		t.Fatalf("error = %v, want a usage error", err)
	}
	return apiErr.Message
}

// --html writes a thread to a pipe as one HTML document: a head declaring the charset and
// naming the thread, then an <article> per entry, oldest first, holding the entry's
// original markup after a header naming the sender and the date. An entry without a
// body holds only its header, and says why in data-body-state.
func TestThreadsHTMLWritesEachEntryToAPipe(t *testing.T) {
	server, _ := threadEntriesServer(t,
		[][]int64{{12, 11}},
		map[int64]string{
			11: `<div>the first <b>word</b></div>`,
			12: "",
		})
	stdoutTerminal(t, false)

	stdout, stderr, err := runCLIRaw(t, server, "threads", "7", "--html")
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr %q)", err, stderr)
	}
	want := "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Thread 7</title>\n</head>\n<body>\n" +
		"<article id=\"entry-11\" data-entry-id=\"11\" data-created-at=\"2026-04-12T09:30\" data-body-state=\"hydrated\">\n" +
		"<header>From: Rick Sanchez — 2026-04-12T09:30</header>\n" +
		"<div>the first <b>word</b></div>\n" +
		"</article>\n" +
		"<article id=\"entry-12\" data-entry-id=\"12\" data-created-at=\"2026-04-13T09:30\" data-body-state=\"bodyless\">\n" +
		"<header>From: Rick Sanchez — 2026-04-13T09:30</header>\n" +
		"</article>\n" +
		"</body>\n</html>\n"
	if stdout != want {
		t.Errorf("stdout =\n%q\nwant\n%q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// The sender is whatever the entry says it is, so it is escaped wherever it lands —
// text and attribute alike — and stripped of controls first; the body is HEY's HTML and
// is written as it came, markup and all, since that is what --html is for.
func TestThreadsHTMLEscapesTheSenderAndKeepsTheBodyVerbatim(t *testing.T) {
	var out bytes.Buffer
	entries := []threadEntry{{
		ID:                    11,
		CreatedAt:             "2026-04-12T09:30",
		BodyState:             "hydrated",
		AlternativeSenderName: "Rick <b>\"Pickle\"</b> \x1b[31m& Co",
		BodyHTML:              `<div onclick="x()">the <b>word</b> &amp; more</div>`,
	}}
	if err := writeThreadHTML(&out, 7, entries, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantHeader := "<header>From: Rick &lt;b&gt;&#34;Pickle&#34;&lt;/b&gt; &amp; Co — 2026-04-12T09:30</header>\n"
	if !strings.Contains(out.String(), wantHeader) {
		t.Errorf("document =\n%s\nwant the header %q", out.String(), wantHeader)
	}
	if !strings.Contains(out.String(), "\n"+`<div onclick="x()">the <b>word</b> &amp; more</div>`+"\n</article>\n") {
		t.Errorf("document =\n%s\nwant the body verbatim", out.String())
	}
}

// A thread read in part ends its document with the notice in a comment, and says it on
// stderr too; without --allow-partial the refusal is the same as for every other format.
func TestThreadsHTMLCarriesThePartialNoticeInAComment(t *testing.T) {
	limits := threadload.DefaultLimits
	limits.MaxPages = 1
	withThreadLimits(t, limits)
	stdoutTerminal(t, false)

	server, _ := partialThreadServer(t, [][]int64{{13, 12}, {11}})
	stdout, stderr, err := runCLIRaw(t, server, "threads", "7", "--html", "--allow-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantEnd := "</article>\n<!-- notice: only the newest 2 entries were read; older ones exist, beyond the 1-page limit -->\n</body>\n</html>\n"
	if !strings.HasSuffix(stdout, wantEnd) {
		t.Errorf("stdout =\n%q\nwant it to end with the notice comment before </body>:\n%q", stdout, wantEnd)
	}
	if strings.Count(stdout, "<article ") != 2 {
		t.Errorf("stdout = %q, want the two entries that were read", stdout)
	}
	if !strings.Contains(stderr, "notice: only the newest 2 entries were read") {
		t.Errorf("stderr = %q, want the notice", stderr)
	}

	server, _ = partialThreadServer(t, [][]int64{{13, 12}, {11}})
	stdout, _, err = runCLIRaw(t, server, "threads", "7", "--html")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "read only in part") || !strings.Contains(apiErr.Hint, "--allow-partial") {
		t.Errorf("error = %v, want the partial-thread refusal naming --allow-partial", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing for a refused thread", stdout)
	}
}

// A notice cannot end the comment it is written in.
func TestThreadsHTMLNoticeCannotEndItsComment(t *testing.T) {
	var out bytes.Buffer
	if err := writeThreadHTML(&out, 7, nil, "limit --> reached"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "<!-- notice: limit - -> reached -->\n</body>") {
		t.Errorf("document = %q", out.String())
	}
}

func TestHTMLIsRefusedOnATerminalWithARedirectHint(t *testing.T) {
	server, _ := threadEntriesServer(t, nil, nil)
	stdoutTerminal(t, true)

	_, _, err := runCLIRaw(t, server, "threads", "7", "--html")
	if got := usageMessage(t, err); !strings.Contains(got, "terminal") {
		t.Errorf("message = %q, want the terminal named", got)
	}
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) && !strings.Contains(apiErr.Hint, "hey threads --html > out.html") {
		t.Errorf("hint = %q, want the redirect spelled out", apiErr.Hint)
	}
}

func TestHTMLConflictsWithEveryOtherOutputSelector(t *testing.T) {
	server, _ := threadEntriesServer(t, nil, nil)
	stdoutTerminal(t, false)

	for _, flag := range []string{"--json", "--markdown", "--quiet", "--ids-only", "--count", "--styled", "--agent", "--stats", "--jq=."} {
		_, _, err := runCLIRaw(t, server, "threads", "7", "--html", flag)
		name, _, _ := strings.Cut(flag, "=")
		if got := usageMessage(t, err); got != "cannot use --html with "+name {
			t.Errorf("%s: message = %q", flag, got)
		}
	}
}

// A command without HTML refuses the flag before it reads configuration or makes a
// request: the server here answers nothing, and no credentials are set.
func TestHTMLIsRefusedByOtherCommandsBeforeAnythingElse(t *testing.T) {
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	t.Setenv("HEY_TOKEN", "")
	stdoutTerminal(t, false)

	for _, test := range []struct {
		args []string
		path string
	}{
		{[]string{"box", "imbox"}, "hey box"},
		{[]string{"contacts", "list"}, "hey contacts list"},
		{[]string{"journal", "list"}, "hey journal list"},
		{[]string{"calendars"}, "hey calendars"},
	} {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append([]string{"--base-url", server.URL, "--html"}, test.args...))
		err := root.Execute()
		if got := usageMessage(t, err); got != "--html is not supported by "+test.path {
			t.Errorf("%v: message = %q", test.args, got)
		}
	}
}

func TestHTMLCommentsCannotBeEndedByWhatTheyHold(t *testing.T) {
	if got := htmlCommentSafe("Rick --> <script>\x1b[31m"); got != "Rick - -> <script>" {
		t.Errorf("htmlCommentSafe = %q", got)
	}
}

// A single body — a contact's note, a journal entry — is a fragment, not a document:
// the HTML as HEY served it, and nothing at all when there is none.
func TestNoteHTMLWritesNothingForAnEmptyNote(t *testing.T) {
	var out bytes.Buffer
	if err := writeNoteHTML(&out, ""); err != nil || out.Len() != 0 {
		t.Errorf("writeNoteHTML = %q, %v", out.String(), err)
	}
	if err := writeNoteHTML(&out, "<div>call back</div>"); err != nil || out.String() != "<div>call back</div>\n" {
		t.Errorf("writeNoteHTML = %q, %v", out.String(), err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestThreadHTMLReportsAWriteError(t *testing.T) {
	err := writeThreadHTML(failingWriter{}, 7, []threadEntry{{ID: 1, BodyHTML: "<p>x</p>"}}, "")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error = %v, want the write failure", err)
	}
}
