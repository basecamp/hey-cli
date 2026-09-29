package mail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// Only what HEY emails can be replied to: haystack's Message, Announcement and
// SignUp::Message answer replyable? true, every other entryable false.
func TestReplyableEntry(t *testing.T) {
	for kind, want := range map[string]bool{
		"message":         true,
		"announcement":    true,
		"sign_up_message": true,
		"":                true,
		"comment":         false,
		"access_notice":   false,
		"auto_response":   false,
		"delivery_error":  false,
	} {
		if got := ReplyableEntry(kind); got != want {
			t.Errorf("ReplyableEntry(%q) = %t, want %t", kind, got, want)
		}
	}
}

func TestInternalEntryLabel(t *testing.T) {
	heading, tag, internal := InternalEntryLabel("comment", "Priya Raman")
	if !internal || heading != "Note by Priya Raman" || tag != "internal note, not emailed" {
		t.Errorf("comment = %q %q %t", heading, tag, internal)
	}
	heading, tag, internal = InternalEntryLabel("access_notice", "Marcus Lee")
	if !internal || heading != "Marcus Lee shared this thread" || tag != "share notice, not emailed" {
		t.Errorf("access notice = %q %q %t", heading, tag, internal)
	}
	for _, kind := range []string{"message", "announcement", "auto_response", ""} {
		if _, _, internal := InternalEntryLabel(kind, "Dana Whitaker"); internal {
			t.Errorf("%q is labelled internal", kind)
		}
	}
}

// HEY takes the last replyable entry by id, whatever sits after it on the page.
func TestReplyTargetSkipsNotesOnTheTopicsPage(t *testing.T) {
	topic := &generated.Topic{Id: 7, Entries: []generated.Entry{
		{Id: 11, Kind: "message"},
		{Id: 12, Kind: "message"},
		{Id: 13, Kind: "comment"},
		{Id: 14, Kind: "access_notice"},
	}}

	entry, err := ReplyTarget(context.Background(), nil, topic)
	if err != nil || entry.Id != 12 {
		t.Errorf("entry = %d, %v, want 12", entry.Id, err)
	}
}

// A topic whose own page is all notes is searched down the entry index, newest first,
// page by page, and a thread with nothing replyable in it is refused.
func TestReplyTargetWalksTheEntryIndex(t *testing.T) {
	pages := map[string]string{
		"":      `[{"id":40,"kind":"comment"},{"id":39,"kind":"comment"}]`,
		"older": `[{"id":38,"kind":"access_notice"},{"id":12,"kind":"message"},{"id":11,"kind":"message"}]`,
	}
	var reads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		reads = append(reads, page)
		if page == "" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/topics/7/entries.json?page=older>; rel="next"`, r.Host))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, pages[page])
	}))
	t.Cleanup(server.Close)
	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))

	topic := &generated.Topic{Id: 7, Entries: []generated.Entry{{Id: 39, Kind: "comment"}, {Id: 40, Kind: "comment"}}}
	entry, err := ReplyTarget(context.Background(), client, topic)
	if err != nil || entry.Id != 12 {
		t.Errorf("entry = %d, %v, want 12", entry.Id, err)
	}
	if strings.Join(reads, ",") != ",older" {
		t.Errorf("pages read = %q, want the first and then the older one", reads)
	}

	pages["older"] = `[{"id":38,"kind":"access_notice"}]`
	if _, err := ReplyTarget(context.Background(), client, topic); !errors.Is(err, ErrNoReplyableEntry) {
		t.Errorf("error = %v, want ErrNoReplyableEntry", err)
	}
}

// The thread view labels a note from the kind its entry carries.
func TestNewEntryKeepsTheKind(t *testing.T) {
	if entry := NewEntry(generated.Entry{Id: 13, Kind: "comment"}, generated.Message{}); entry.Kind != "comment" {
		t.Errorf("kind = %q, want comment", entry.Kind)
	}
}
