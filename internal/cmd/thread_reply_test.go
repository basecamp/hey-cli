package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

const (
	topicWithRecipients = `<span class="entry__full-recipients">
		<a title="jane@example.com">Jane</a>
		CC: <a title="cc@example.com">Cee</a>
	</span>`
	topicEntries = `<div data-entry-id="11"></div><div data-entry-id="12"></div>`
)

// threadReplyServer answers the two topic pages resolveThreadReply reads.
func threadReplyServer(t *testing.T, topicHTML, entriesHTML string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if strings.HasSuffix(r.URL.Path, "/entries") {
			fmt.Fprint(w, entriesHTML)
			return
		}
		fmt.Fprint(w, topicHTML)
	}))
	t.Cleanup(server.Close)
	return server
}

// withSDKPointedAt builds the package-level client the commands use, aimed at a test
// server, and puts the old one back afterwards.
func withSDKPointedAt(t *testing.T, server *httptest.Server) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")

	previous := sdk
	t.Cleanup(func() { sdk = previous })
	initSDK(nil, server.URL)
}

func TestResolveThreadReply(t *testing.T) {
	withSDKPointedAt(t, threadReplyServer(t, topicWithRecipients, topicEntries))

	target, err := resolveThreadReply(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// HEY replies to the last entry on the thread, not the first.
	if target.EntryID != 12 {
		t.Errorf("entry = %d, want the last one (12)", target.EntryID)
	}
	if len(target.Addressed.To) != 1 || target.Addressed.To[0] != "jane@example.com" {
		t.Errorf("to = %v", target.Addressed.To)
	}
	if len(target.Addressed.CC) != 1 || target.Addressed.CC[0] != "cc@example.com" {
		t.Errorf("cc = %v", target.Addressed.CC)
	}
}

// An unaddressed reply is saved as a draft rather than sent, so a thread we cannot read
// recipients from is refused before anything is written.
func TestResolveThreadReplyWithoutRecipients(t *testing.T) {
	withSDKPointedAt(t, threadReplyServer(t, `<html><body>no recipients here</body></html>`, topicEntries))

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestResolveThreadReplyWithoutEntries(t *testing.T) {
	withSDKPointedAt(t, threadReplyServer(t, topicWithRecipients, `<html><body></body></html>`))

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}
