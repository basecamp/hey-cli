package mail

import (
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// Entry is one message in a thread. CreatedAt is the time HEY served, not a string of it,
// for the reason Posting gives: a date formatted and parsed back is a date a reader east
// of UTC reads as yesterday's.
//
// Body is Markdown, converted once here at the edge, and BodyHTML keeps the HTML HEY
// served so the image and attachment extractors still have the attributes Markdown drops.
//
// Recipients and ReceivedVia come from the message the way the body does, so both are
// empty when that message was not read. ReceivedVia is the exact account address an
// inbound message arrived through, including a plus-tag or catch-all alias, and not the
// contact HEY resolved that address to. Mail you sent has none.
type Entry struct {
	ID                    int64
	CreatedAt             time.Time
	Creator               Contact
	Sender                Contact
	AlternativeSenderName string
	// Kind is HEY's Entry#kind: a message, or a note (comment) or share notice
	// (access_notice) that was never emailed. See InternalEntryLabel.
	Kind        string
	Recipients  Recipients
	ReceivedVia []string
	Summary     string
	Body        htmlutil.Markdown
	BodyHTML    string
	// BodyState is what became of the body when the entry came through threadload:
	// hydrated, bodyless, over_limit or failed. Empty for an entry read another way.
	BodyState string
}

// LoadedEntry describes an entry threadload read: the message when there is one, and
// what became of the body either way.
func LoadedEntry(loaded threadload.Entry) Entry {
	if loaded.Message == nil {
		entry := NewEntry(loaded.Entry, generated.Message{})
		entry.BodyState = string(loaded.State)
		return entry
	}
	entry := NewEntry(loaded.Entry, *loaded.Message)
	entry.BodyState = string(loaded.State)
	return entry
}

// NewEntry describes a thread's entry against the message HEY served for it. A topic
// lists its entries and each message is read on its own, and the two disagree about what
// they carry: an entry read as part of a bundle has no creator and no timestamp of its
// own, and a message read directly has a subject where the entry has a summary.
func NewEntry(entry generated.Entry, message generated.Message) Entry {
	creator := entry.Creator
	if creator.Id == 0 {
		creator = message.Creator
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = message.CreatedAt
	}
	summary := entry.Summary
	if summary == "" {
		summary = message.Subject
	}
	recipients, receivedVia := Envelope(message)

	return Entry{
		ID:                    entry.Id,
		CreatedAt:             createdAt,
		Creator:               contactOf(creator),
		Sender:                contactOf(message.Sender),
		AlternativeSenderName: terminal.SanitizeLine(entry.AlternativeSenderName),
		Kind:                  entry.Kind,
		Recipients:            recipients,
		ReceivedVia:           receivedVia,
		Summary:               terminal.SanitizeLine(summary),
		Body:                  htmlutil.ToMarkdown(message.Content),
		BodyHTML:              message.Content,
	}
}

// Recipients is a message's visible envelope: the To, Cc and Bcc lines HEY recorded.
type Recipients struct {
	To  []Contact
	Cc  []Contact
	Bcc []Contact
}

// Envelope reads who a message was addressed to and the exact account addresses it
// arrived through. A delivery address is kept as HEY wrote it. The contact HEY resolved
// that address to is a different fact, and it is not what arrived. Both results are
// empty when HEY served none.
func Envelope(message generated.Message) (Recipients, []string) {
	return Recipients{
		To:  contactsOf(message.Addressed.Directly),
		Cc:  contactsOf(message.Addressed.Copied),
		Bcc: contactsOf(message.Addressed.Blindcopied),
	}, receivedViaOf(message.ReceivedVia)
}

func contactsOf(contacts []generated.Contact) []Contact {
	if len(contacts) == 0 {
		return nil
	}
	described := make([]Contact, len(contacts))
	for i, contact := range contacts {
		described[i] = contactOf(contact)
	}
	return described
}

func receivedViaOf(deliveries []generated.MessageReceivedVia) []string {
	if len(deliveries) == 0 {
		return nil
	}
	addresses := make([]string, 0, len(deliveries))
	for _, delivery := range deliveries {
		address := terminal.SanitizeLine(delivery.EmailAddress)
		if address == "" {
			continue
		}
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		return nil
	}
	return addresses
}
