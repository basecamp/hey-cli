package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// --html is settled before anything else the root does, so the version short-circuit
// and the jq validation cannot get in ahead of it.
func TestHTMLIsValidatedBeforeVersionAndJQ(t *testing.T) {
	stdoutTerminal(t, false)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--version", "--html"}, "--html is not supported by hey"},
		{[]string{"thread", "read", "7", "--html", "--jq="}, "cannot use --html with --jq"},
	} {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(test.args)
		err := root.Execute()
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Message != test.want {
			t.Errorf("%v: error = %v, want %q", test.args, err, test.want)
		}
	}
}

func TestHTMLRequestedReadsTheRawArguments(t *testing.T) {
	for args, want := range map[string]bool{
		"threads 7 --bogus --html": true,
		"threads 7 --html=true":    true,
		"threads 7 --json":         false,
		"threads 7 -- --html":      false,
	} {
		if got := htmlRequested(strings.Fields(args)); got != want {
			t.Errorf("htmlRequested(%q) = %v, want %v", args, got, want)
		}
	}
}

// A day without an entry writes nothing under --html rather than a usage error.
func TestJournalReadHTMLWritesNothingForAnEmptyDay(t *testing.T) {
	server := journalServerWithReadBehavior(t, "204")
	t.Cleanup(server.Close)
	stdoutTerminal(t, false)

	stdout, stderr, err := runCLIRaw(t, server, "journal", "read", "2026-03-15", "--html")
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr %q)", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}

	server = journalServerWithReadBehavior(t, "200")
	t.Cleanup(server.Close)
	stdout, _, err = runCLIRaw(t, server, "journal", "read", "2026-03-15", "--html")
	if err != nil || !strings.Contains(stdout, "<div>Entry for 2026-03-15</div>") {
		t.Errorf("stdout = %q, err = %v, want the entry's HTML", stdout, err)
	}
}
