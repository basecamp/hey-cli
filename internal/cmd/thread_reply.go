package cmd

import (
	"context"
	"fmt"
	"strings"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// replyRecipients is who a reply goes out to, in HEY's three kinds of addressing.
type replyRecipients struct {
	To  []string
	CC  []string
	BCC []string
}

// threadReplyTarget carries the entry a reply answers, its subject, sender and
// recipients, and an immutable client bound to the thread's mail account. HEY saves an
// unaddressed reply as a draft, so the recipients are not optional. The subject is not
// optional either: HEY never derives one, so a reply sent without it saves drafts that
// read "No subject" in Drafts. ActingSenderID is the identity the reply goes out as —
// the sender HEY resolved for the thread, which on a shared or alternate address is
// not the account default; zero (the prefill named none, or was unreachable) leaves
// the SDK on the account default.
type threadReplyTarget struct {
	EntryID        int64
	AccountID      int64
	ActingSenderID int64
	Subject        string
	Addressed      replyRecipients
	client         *hey.Client
}

// resolveThreadReply returns the thread's latest entry, linked account, and the
// recipients a reply to that entry goes to.
func resolveThreadReply(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	topic, err := rootSDK.Topics().Get(ctx, threadID)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if topic == nil || len(topic.Entries) == 0 {
		return nil, apierr.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}

	threadSDK, err := clientForResourceAccount(ctx, topic.AccountId)
	if err != nil {
		return nil, err
	}
	entryID := topic.Entries[len(topic.Entries)-1].Id

	target := &threadReplyTarget{
		EntryID:   entryID,
		AccountID: topic.AccountId,
		client:    threadSDK,
	}
	prefill, ok := replyPrefillFromServer(ctx, threadSDK, entryID)
	target.ActingSenderID = prefill.ActingSenderID
	target.Subject = prefill.Subject
	if ok {
		target.Addressed = prefill.Addressed
		return target, nil
	}

	message, err := threadSDK.Messages().Get(ctx, entryID)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if message == nil {
		return nil, apierr.ErrNotFound("message", fmt.Sprintf("%d", entryID))
	}

	addressed := recipientsForReplyTo(*message)
	if len(addressed.To) == 0 && len(addressed.CC) == 0 && len(addressed.BCC) == 0 {
		return nil, apierr.ErrUsage("could not determine thread recipients")
	}

	// The prefill's subject survives an empty recipient list: only the recipients
	// needed the local computation.
	if target.Subject == "" {
		target.Subject = replySubject(message.Subject)
	}
	target.Addressed = addressed
	return target, nil
}

// replyPrefill is how a reply starts out, as HEY prefills it: the "Re: …" subject it
// goes out under, the sender it goes out as, and who it goes out to.
type replyPrefill struct {
	Subject        string
	ActingSenderID int64
	Addressed      replyRecipients
}

// replyPrefillFromServer asks HEY how a reply to the entry starts out
// (GET /entries/{id}/replies/new): the "Re: …" subject the reply carries; the sender
// it goes out as — resolved from the entry's own to and from addresses, so a thread on
// a shared or alternate address answers as that address, not the account default, and
// named only when it differs from the acting user; and its recipients — the entry's
// sender moved onto the To line and the acting user's own addresses, aliases and
// catch-alls excluded — the exclusion this CLI cannot compute locally, and the reason
// a reply used to be able to CC its writer back to themselves. A failed read falls
// back to the local computation, and so does an empty answer: on a thread with
// yourself, everyone HEY excludes is everyone there is, and the local list is what
// keeps that reply addressable. The subject and sender are answered even when the
// recipients are not — only they need the fallback, not what HEY already supplied.
func replyPrefillFromServer(ctx context.Context, client *hey.Client, entryID int64) (replyPrefill, bool) {
	prefilled, err := client.Entries().NewReply(ctx, entryID)
	if err != nil || prefilled == nil {
		return replyPrefill{}, false
	}
	prefill := replyPrefill{
		Subject:        prefilled.Subject,
		ActingSenderID: prefilled.Sender.Id,
		Addressed: replyRecipients{
			To:  addressEmails(prefilled.Addressed.Directly),
			CC:  addressEmails(prefilled.Addressed.Copied),
			BCC: addressEmails(prefilled.Addressed.Blindcopied),
		},
	}
	if len(prefill.Addressed.To)+len(prefill.Addressed.CC)+len(prefill.Addressed.BCC) == 0 {
		prefill.Addressed = replyRecipients{}
		return prefill, false
	}
	return prefill, true
}

// replySubject answers the subject a reply to the given subject carries, the way HEY
// derives it in Entry::Replyable#reply_subject: a "Re: " prefix, without doubling one
// already there in any casing. An empty subject stays empty rather than becoming a
// bare "Re:".
func replySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}

	rest := subject
	if len(rest) >= 3 && strings.EqualFold(rest[:3], "Re:") {
		rest = strings.TrimPrefix(rest[3:], " ")
	}
	return strings.TrimRight("Re: "+rest, " ")
}

// recipientsForReplyTo answers who a reply to this message goes to: the message's own
// recipients, with whoever sent it moved onto the To line. That is what HEY does in
// Entry::Addressed#participating_contacts_in_reply_by_kind, so a reply reaches the
// person who wrote the message as well as everyone they wrote to.
func recipientsForReplyTo(message generated.Message) replyRecipients {
	sender := message.Sender.EmailAddress
	if sender == "" {
		sender = message.Creator.EmailAddress
	}

	recipients := replyRecipients{
		To:  addressesOf(message.Addressed.Directly, sender),
		CC:  addressesOf(message.Addressed.Copied, sender),
		BCC: addressesOf(message.Addressed.Blindcopied, sender),
	}
	if sender != "" {
		recipients.To = append(recipients.To, sender)
	}
	return recipients
}

// addressesOf answers the contacts' email addresses, dropping blanks, repeats, and the
// one address HEY addresses directly instead.
func addressesOf(contacts []generated.Contact, excluding string) []string {
	seen := map[string]bool{strings.ToLower(excluding): true}
	var addresses []string
	for _, contact := range contacts {
		address := strings.TrimSpace(contact.EmailAddress)
		key := strings.ToLower(address)
		if address != "" && !seen[key] {
			seen[key] = true
			addresses = append(addresses, address)
		}
	}
	return addresses
}
