package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// internalEntriesView is a mail view over a HEY for Domains thread whose newest entries
// are internal: Dana wrote in (500), Marcus answered (501), then Priya left a note (502)
// and Marcus shared the thread with Owen (503). Asking HEY how to reply to or forward
// either of the last two fails the test — neither was ever emailed.
func internalEntriesView(t *testing.T, topicEntries string) *mailView {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity.json":
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"default":true}]}`)
		case "/topics/100.json":
			fmt.Fprintf(w, `{"id":100,"account_id":9,"name":"Onboarding pilot for Northwind","entries":%s}`, topicEntries)
		case "/topics/100/entries.json":
			fmt.Fprint(w, `[]`)
		case "/entries/501/replies/new.json":
			fmt.Fprint(w, `{"subject":"Re: Onboarding pilot for Northwind","addressed":{"directly":[{"id":301,"name":"Dana Whitaker","email_address":"dana@example.org"}]}}`)
		case "/entries/501/forwards/new.json":
			fmt.Fprint(w, `{"subject":"Fwd: Onboarding pilot for Northwind","content":"<div>Happy to set that up.</div>"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	sdk := hey.NewClient(&hey.Config{BaseURL: srv.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.rootSDK = sdk
	vc.sdk = sdk
	vc.ctx = context.Background()
	return newMailView(vc)
}

const internalThreadEntries = `[
	{"id":500,"kind":"message","creator":{"id":301,"name":"Dana Whitaker","email_address":"dana@example.org"},"summary":"Could we try the onboarding pilot?"},
	{"id":501,"kind":"message","creator":{"id":302,"name":"Marcus Lee","email_address":"marcus@example.com"},"summary":"Happy to set that up."},
	{"id":502,"kind":"comment","creator":{"id":303,"name":"Priya Raman","email_address":"priya@example.com"},"summary":"Lead with ticket deflection."},
	{"id":503,"kind":"access_notice","creator":{"id":302,"name":"Marcus Lee","email_address":"marcus@example.com"},"summary":"Owen, can you own the kickoff agenda?"}
]`

func TestReplyContextAnswersTheLastEmailedMessageNotANote(t *testing.T) {
	v := internalEntriesView(t, internalThreadEntries)

	loaded := runCmd(v.loadReplyContext(100, "Onboarding pilot for Northwind"))
	ctxMsg, ok := loaded.(replyContextLoadedMsg)
	if !ok || ctxMsg.err != nil {
		t.Fatalf("reply command returned %#v", loaded)
	}
	if ctxMsg.entryID != 501 {
		t.Errorf("entry = %d, want Marcus's message (501)", ctxMsg.entryID)
	}
	if want := []string{"dana@example.org"}; !slices.Equal(ctxMsg.to, want) {
		t.Errorf("to = %v, want %v", ctxMsg.to, want)
	}
	if ctxMsg.subject != "Re: Onboarding pilot for Northwind" {
		t.Errorf("subject = %q", ctxMsg.subject)
	}
}

func TestForwardContextForwardsTheLastEmailedMessageNotANote(t *testing.T) {
	v := internalEntriesView(t, internalThreadEntries)

	loaded := runCmd(v.loadForwardContext(100, "Onboarding pilot for Northwind"))
	ctxMsg, ok := loaded.(forwardContextLoadedMsg)
	if !ok || ctxMsg.err != nil {
		t.Fatalf("forward command returned %#v", loaded)
	}
	if ctxMsg.content != "<div>Happy to set that up.</div>" {
		t.Errorf("content = %q, want Marcus's message", ctxMsg.content)
	}
}

func TestReplyAndForwardRefuseAThreadWithoutAnEmailedMessage(t *testing.T) {
	onlyInternal := `[
		{"id":502,"kind":"comment","creator":{"id":303,"name":"Priya Raman","email_address":"priya@example.com"}},
		{"id":503,"kind":"access_notice","creator":{"id":302,"name":"Marcus Lee","email_address":"marcus@example.com"}}
	]`
	v := internalEntriesView(t, onlyInternal)

	reply, _ := runCmd(v.loadReplyContext(100, "Onboarding pilot for Northwind")).(replyContextLoadedMsg)
	if reply.err == nil || !strings.Contains(reply.err.Error(), "no emailed message") {
		t.Errorf("reply error = %v, want a refusal naming the missing message", reply.err)
	}
	forward, _ := runCmd(v.loadForwardContext(100, "Onboarding pilot for Northwind")).(forwardContextLoadedMsg)
	if forward.err == nil || !strings.Contains(forward.err.Error(), "no emailed message") {
		t.Errorf("forward error = %v, want a refusal naming the missing message", forward.err)
	}
}

// The thread view heads a note and a share notice by what they are, so neither reads as
// mail that went out.
func TestThreadViewLabelsNotesAndShareNotices(t *testing.T) {
	v := newMailView(testVC())
	v.Resize(100, 40)
	created := time.Date(2026, 9, 14, 15, 30, 0, 0, time.UTC)
	entries := []mail.Entry{
		{ID: 501, Kind: mail.EntryKindMessage, CreatedAt: created, Creator: mail.Contact{Name: "Marcus Lee"},
			Body: htmlutil.ToMarkdown("<div>Happy to set that up.</div>"), BodyState: string(threadload.StateHydrated)},
		{ID: 502, Kind: mail.EntryKindComment, CreatedAt: created, Creator: mail.Contact{Name: "Priya Raman"},
			Body: htmlutil.ToMarkdown("<div>Lead with ticket deflection.</div>"), BodyState: string(threadload.StateHydrated)},
		{ID: 503, Kind: mail.EntryKindAccessNotice, CreatedAt: created, Creator: mail.Contact{Name: "Marcus Lee"},
			Body: htmlutil.ToMarkdown("<div>Owen, can you own the kickoff agenda?</div>"), BodyState: string(threadload.StateHydrated)},
	}

	rendered, _ := v.renderEntries(entries)
	shown := ansi.Strip(rendered)

	for _, want := range []string{
		"Marcus Lee  ",
		"Note by Priya Raman",
		"internal note, not emailed",
		"Marcus Lee shared this thread",
		"share notice, not emailed",
		"Lead with ticket deflection.",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("thread view lacks %q:\n%s", want, shown)
		}
	}
	if strings.Count(shown, "not emailed") != 2 {
		t.Errorf("only the note and the share notice are internal:\n%s", shown)
	}
}
