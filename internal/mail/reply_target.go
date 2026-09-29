package mail

import (
	"context"
	"errors"
	"fmt"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// The kinds of entry HEY serves in a thread, as Entry#kind names them: the entryable's
// class, underscored.
const (
	EntryKindMessage       = "message"
	EntryKindAnnouncement  = "announcement"
	EntryKindSignUpMessage = "sign_up_message"
	EntryKindComment       = "comment"
	EntryKindAccessNotice  = "access_notice"
)

// maxReplyTargetPages bounds how far back ReplyTarget walks a thread's entries looking
// for a message: a thread whose newest few hundred entries are all notes is refused
// rather than read to the end.
const maxReplyTargetPages = 20

// ErrNoReplyableEntry is ReplyTarget's refusal: nothing in the thread was ever emailed,
// so there is nothing a reply or a forward could answer.
var ErrNoReplyableEntry = errors.New("no emailed message to reply to")

// ReplyableEntry reports whether HEY lets a reply or a forward answer an entry of this
// kind: haystack's Entry#replyable?, which asks the entryable, and only a Message, an
// Announcement and a SignUp::Message say yes. A note (comment), a share notice
// (access_notice), an auto response and a delivery error are never emailed and are
// never replied to. An empty kind is an entry shaped before HEY served one, and is taken
// to be the message it then always was.
func ReplyableEntry(kind string) bool {
	switch kind {
	case "", EntryKindMessage, EntryKindAnnouncement, EntryKindSignUpMessage:
		return true
	default:
		return false
	}
}

// InternalEntryLabel answers how text output names an entry that stays inside HEY — a
// note or a share notice, visible to everyone with access to the thread and never
// emailed — so it is not read as a message that went out: a heading naming the author
// in place of a From line, and a tag saying what it is. The author is placed as given,
// so a caller hands it over already made safe for where it is going. internal is false
// for every other kind.
func InternalEntryLabel(kind, author string) (heading, tag string, internal bool) {
	switch kind {
	case EntryKindComment:
		return "Note by " + author, "internal note, not emailed", true
	case EntryKindAccessNotice:
		return author + " shared this thread", "share notice, not emailed", true
	default:
		return "", "", false
	}
}

// ReplyTarget answers the entry a reply to the topic — or a forward of it — answers:
// HEY's Topic#last_replyable_entry, the newest replyable entry, never a note or a share
// notice posted after it. The topic's own first page is looked at first; only a page
// with nothing replyable on it sends the search down the entry index, newest first,
// through client, which must be bound to the thread's account. HEY picks by id among
// the replyable entries, so a page does too.
func ReplyTarget(ctx context.Context, client *hey.Client, topic *generated.Topic) (generated.Entry, error) {
	if entry, ok := lastReplyableEntry(topic.Entries); ok {
		return entry, nil
	}

	cursor := ""
	for range maxReplyTargetPages {
		page, err := client.Topics().GetEntriesPage(ctx, topic.Id, cursor)
		if err != nil {
			return generated.Entry{}, err
		}
		if page == nil || len(page.Entries) == 0 {
			return generated.Entry{}, ErrNoReplyableEntry
		}
		if entry, ok := lastReplyableEntry(page.Entries); ok {
			return entry, nil
		}
		if page.NextPage == "" {
			return generated.Entry{}, ErrNoReplyableEntry
		}
		cursor = page.NextPage
	}
	return generated.Entry{}, fmt.Errorf("%w in its newest %d pages of entries", ErrNoReplyableEntry, maxReplyTargetPages)
}

func lastReplyableEntry(entries []generated.Entry) (generated.Entry, bool) {
	var last generated.Entry
	found := false
	for _, entry := range entries {
		if ReplyableEntry(entry.Kind) && (!found || entry.Id > last.Id) {
			last, found = entry, true
		}
	}
	return last, found
}
