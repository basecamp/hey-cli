package mail

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

func TestNewEntry(t *testing.T) {
	created := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)

	entry := NewEntry(generated.Entry{
		Id: 501, Summary: "Thread summary", CreatedAt: created,
		AlternativeSenderName: "Support",
		Creator:               generated.Contact{Id: 42, Name: "Jane Dawson", EmailAddress: "jane@example.com"},
	}, generated.Message{
		Id: 501, Subject: "Thread subject", Content: `<p>Message body with a <a href="https://example.com/plan">plan</a></p>`,
	})

	if entry.ID != 501 || entry.Summary != "Thread summary" || entry.AlternativeSenderName != "Support" {
		t.Errorf("entry = %+v", entry)
	}
	if !entry.CreatedAt.Equal(created) {
		t.Errorf("created at = %s, want %s", entry.CreatedAt, created)
	}
	if entry.Creator.ID != 42 || entry.Creator.EmailAddress != "jane@example.com" {
		t.Errorf("creator = %+v", entry.Creator)
	}
	if entry.Body != "Message body with a [plan](https://example.com/plan)" {
		t.Errorf("body = %q, want Markdown", entry.Body)
	}
	if entry.BodyHTML != `<p>Message body with a <a href="https://example.com/plan">plan</a></p>` {
		t.Errorf("body HTML = %q, want the HTML HEY served", entry.BodyHTML)
	}
}

// An entry listed under a bundle carries neither its creator nor its own timestamp, and
// its subject is on the message rather than the entry, so the message is what answers.
func TestNewEntryFallsBackToTheMessage(t *testing.T) {
	created := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)

	entry := NewEntry(generated.Entry{Id: 501}, generated.Message{
		Id: 501, Subject: "Thread subject", CreatedAt: created,
		Creator: generated.Contact{Id: 42, Name: "Jane Dawson", EmailAddress: "jane@example.com"},
	})

	if entry.Summary != "Thread subject" {
		t.Errorf("summary = %q, want the message's subject", entry.Summary)
	}
	if !entry.CreatedAt.Equal(created) {
		t.Errorf("created at = %s, want the message's %s", entry.CreatedAt, created)
	}
	if entry.Creator.Name != "Jane Dawson" {
		t.Errorf("creator = %+v, want the message's", entry.Creator)
	}
}
