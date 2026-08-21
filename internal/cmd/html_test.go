package cmd

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
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

// --html writes the original HTML of a thread to a pipe: each entry's markup, oldest
// first, introduced by a comment naming the entry.
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
	want := "<!-- hey entry 11 from Rick Sanchez at 2026-04-12T09:30 -->\n<div>the first <b>word</b></div>\n" +
		"\n<!-- hey entry 12 from Rick Sanchez at 2026-04-13T09:30 -->\n<!-- no body: bodyless -->\n"
	if stdout != want {
		t.Errorf("stdout =\n%q\nwant\n%q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
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

func TestHTMLCommentsCannotBeEndedByASender(t *testing.T) {
	if got := htmlCommentSafe("Rick --> <script>\x1b[31m"); got != "Rick - -> <script>" {
		t.Errorf("htmlCommentSafe = %q", got)
	}
}

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
	err := writeThreadHTML(failingWriter{}, []threadEntry{{ID: 1, BodyHTML: "<p>x</p>"}})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error = %v, want the write failure", err)
	}
}
