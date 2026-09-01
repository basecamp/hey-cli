package mail

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// replyPrefillClient answers GET /entries/12/replies/new.json with the given body, the
// way HEY serves a reply prefill.
func replyPrefillClient(t *testing.T, prefillJSON string) *hey.Client {
	t.Helper()
	return testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/entries/12/replies/new.json" {
			t.Errorf("read %s %s, want the entry's reply prefill", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, prefillJSON)
	})
}

func TestReplyPrefillFromServer(t *testing.T) {
	client := replyPrefillClient(t, `{
		"subject": "Re: Weekly sync", "content": "<div>quoted</div>", "is_reply": true,
		"sender": {"id": 215, "name": "Support", "email_address": "support@example.com"},
		"addressed": {
			"directly": [{"id": 31, "name": "Rick", "email_address": "rick@example.com"}, {"id": 32}],
			"copied": [{"id": 33, "email_address": "cc@example.com"}],
			"blindcopied": [{"id": 34, "email_address": "bcc@example.com"}]
		}
	}`)

	prefill, ok := ReplyPrefillFromServer(context.Background(), client, 12)
	if !ok {
		t.Fatal("an addressed prefill answers; no fallback is needed")
	}
	if prefill.Subject != "Re: Weekly sync" {
		t.Errorf("subject = %q, want the prefilled one", prefill.Subject)
	}
	if prefill.ActingSenderID != 215 {
		t.Errorf("acting sender = %d, want the prefill's 215", prefill.ActingSenderID)
	}
	// The addressless contact is dropped; the rest ride verbatim. The quoted content
	// is not carried at all: HEY appends it at delivery, and echoing it back would
	// double the quote.
	want := ReplyRecipients{
		To:  []string{"rick@example.com"},
		CC:  []string{"cc@example.com"},
		BCC: []string{"bcc@example.com"},
	}
	if !reflect.DeepEqual(prefill.Addressed, want) {
		t.Errorf("addressed = %+v, want %+v", prefill.Addressed, want)
	}
}

// The subject and sender are answered even when the recipients are not: on a thread
// with yourself, everyone HEY excludes is everyone there is, and only the recipients
// need the caller's local fallback.
func TestReplyPrefillFromServerWithoutRecipients(t *testing.T) {
	client := replyPrefillClient(t, `{"subject": "Re: Weekly sync",
		"sender": {"id": 215, "email_address": "support@example.com"}, "addressed": {}}`)

	prefill, ok := ReplyPrefillFromServer(context.Background(), client, 12)
	if ok {
		t.Fatal("a recipientless prefill sends the caller to its local fallback")
	}
	if prefill.Subject != "Re: Weekly sync" || prefill.ActingSenderID != 215 {
		t.Errorf("subject = %q, sender = %d — both survive an empty recipient list",
			prefill.Subject, prefill.ActingSenderID)
	}
	if !reflect.DeepEqual(prefill.Addressed, ReplyRecipients{}) {
		t.Errorf("addressed = %+v, want none", prefill.Addressed)
	}
}

// A read that fails answers nothing: subject, sender and recipients all fall back.
func TestReplyPrefillFromServerUnreachable(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	})

	prefill, ok := ReplyPrefillFromServer(context.Background(), client, 12)
	if ok || !reflect.DeepEqual(prefill, ReplyPrefill{}) {
		t.Errorf("prefill = %+v, ok = %v — an unreachable prefill answers nothing", prefill, ok)
	}
}
