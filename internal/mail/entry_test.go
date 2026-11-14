package mail

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/threadload"
)

func TestNewEntryKeepsTheSelectedSender(t *testing.T) {
	entry := NewEntry(generated.Entry{
		Id: 501, Creator: generated.Contact{Id: 77, Name: "Personal", EmailAddress: "personal@example.org"},
	}, generated.Message{
		Sender: generated.Contact{Id: 88, Name: "Billing", EmailAddress: "billing@example.org"},
	})
	if entry.Creator.EmailAddress != "personal@example.org" || entry.Sender.EmailAddress != "billing@example.org" {
		t.Fatalf("creator = %+v, sender = %+v", entry.Creator, entry.Sender)
	}
}

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
	if entry.Body.String() != "Message body with a [plan](https://example.com/plan)" {
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

// The delivery address is the address HEY recorded, not the contact it resolved that
// address to. A message that was not read has neither.
func TestNewEntryKeepsTheEnvelopeApartFromTheResolvedContact(t *testing.T) {
	entry := NewEntry(generated.Entry{Id: 11}, generated.Message{
		Addressed: generated.Addressed{
			Directly:    []generated.Contact{{Id: 21, Name: "Elija\nSorensen", EmailAddress: "eli@example.com"}},
			Copied:      []generated.Contact{{Id: 22, Name: "Sam Rivera", EmailAddress: "sam@example.com"}},
			Blindcopied: []generated.Contact{{Id: 23, EmailAddress: "audit@example.com"}},
		},
		ReceivedVia: []generated.MessageReceivedVia{{
			EmailAddress: "eli+receipts@example.com",
			Contact:      &generated.Contact{Id: 21, Name: "Elija Sorensen", EmailAddress: "eli@example.com"},
		}, {
			EmailAddress: "invoices@example.org",
		}},
	})

	if len(entry.Recipients.To) != 1 {
		t.Errorf("to = %+v, want one recipient", entry.Recipients.To)
	} else if got, want := entry.Recipients.To[0], (Contact{ID: 21, Name: "Elija Sorensen", EmailAddress: "eli@example.com"}); got != want {
		t.Errorf("to = %+v, want %+v", got, want)
	}
	if len(entry.Recipients.Cc) != 1 || entry.Recipients.Cc[0].EmailAddress != "sam@example.com" {
		t.Errorf("cc = %+v", entry.Recipients.Cc)
	}
	if len(entry.Recipients.Bcc) != 1 || entry.Recipients.Bcc[0].Name != "" || entry.Recipients.Bcc[0].EmailAddress != "audit@example.com" {
		t.Errorf("bcc = %+v", entry.Recipients.Bcc)
	}
	wantVia := []string{"eli+receipts@example.com", "invoices@example.org"}
	if len(entry.ReceivedVia) != len(wantVia) || entry.ReceivedVia[0] != wantVia[0] || entry.ReceivedVia[1] != wantVia[1] {
		t.Errorf("received via = %q, want %q", entry.ReceivedVia, wantVia)
	}
}

func TestLoadedEntryWithoutAMessageHasNoEnvelope(t *testing.T) {
	entry := LoadedEntry(threadload.Entry{Entry: generated.Entry{Id: 11}, State: threadload.StateFailed})
	if len(entry.Recipients.To) != 0 || len(entry.Recipients.Cc) != 0 || len(entry.Recipients.Bcc) != 0 || len(entry.ReceivedVia) != 0 {
		t.Errorf("unread entry envelope = %+v via %q, want none", entry.Recipients, entry.ReceivedVia)
	}
}
