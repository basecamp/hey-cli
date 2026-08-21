package mail

import (
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// Entry is one message in a thread. CreatedAt is the time HEY served, not a string of it,
// for the reason Posting gives: a date formatted and parsed back is a date a reader east
// of UTC reads as yesterday's.
//
// Body is Markdown, converted once here at the edge, and BodyHTML keeps the HTML HEY
// served so the image and attachment extractors still have the attributes Markdown drops.
type Entry struct {
	ID                    int64
	CreatedAt             time.Time
	Creator               Contact
	AlternativeSenderName string
	Summary               string
	Body                  string
	BodyHTML              string
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

	return Entry{
		ID:                    entry.Id,
		CreatedAt:             createdAt,
		Creator:               contactOf(creator),
		AlternativeSenderName: terminal.SanitizeLine(entry.AlternativeSenderName),
		Summary:               terminal.SanitizeLine(summary),
		Body:                  htmlutil.ToMarkdown(message.Content),
		BodyHTML:              message.Content,
	}
}
