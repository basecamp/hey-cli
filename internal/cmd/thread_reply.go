package cmd

import (
	"context"
	"fmt"
	"strings"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
)

// replyRecipients is who a reply goes out to, in HEY's three kinds of addressing.
type replyRecipients = mail.ReplyRecipients

// threadReplyTarget carries the entry a reply answers, its subject, sender and
// recipients, and an immutable client bound to the thread's mail account. Recipients
// can be unresolved when the body-free path cannot get HEY's prefill; only a complete
// replacement envelope can safely address that target. The subject is not optional
// either: HEY never derives one,
// so a reply sent without it saves drafts that read "No subject" in Drafts.
// ActingSenderID is the identity the reply goes out as: the sender HEY resolved for
// the thread, which on a shared or alternate address is not the account default; zero
// (the prefill named none, or was unreachable) leaves the SDK on the account default.
type threadReplyTarget struct {
	EntryID            int64
	AccountID          int64
	ActingSenderID     int64
	Subject            string
	Sender             mail.ReplySender
	Addressed          replyRecipients
	RecipientsResolved bool
	client             *hey.Client
}

// resolveThreadReply returns the thread's latest entry, linked account, and the
// recipients a reply to that entry goes to. If HEY's reply prefill is unavailable,
// it reads the message for the most complete fallback metadata.
func resolveThreadReply(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	return resolveThreadReplyTarget(ctx, threadID, true)
}

// resolveThreadReplyWithoutMessage resolves a reply from the topic entry summary and
// HEY's reply prefill only. Dry runs must not hydrate a body, and a replacement envelope
// does not need the original recipients.
func resolveThreadReplyWithoutMessage(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	return resolveThreadReplyTarget(ctx, threadID, false)
}

func resolveThreadReplyTarget(ctx context.Context, threadID int64, readMessageFallback bool) (*threadReplyTarget, error) {
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
	entry := topic.Entries[len(topic.Entries)-1]
	entryID := entry.Id

	target := &threadReplyTarget{
		EntryID:   entryID,
		AccountID: topic.AccountId,
		client:    threadSDK,
	}
	prefill, ok := mail.ReplyPrefillFromServer(ctx, threadSDK, entryID)
	target.ActingSenderID = prefill.ActingSenderID
	target.Subject = prefill.Subject
	target.Sender = prefill.Sender
	if ok {
		target.Addressed = prefill.Addressed
		target.RecipientsResolved = true
		return target, nil
	}

	if !readMessageFallback {
		if target.Subject == "" {
			target.Subject = replySubject(entry.Subject)
			if target.Subject == "" {
				target.Subject = replySubject(topic.Name)
			}
		}
		return target, nil
	}

	message, err := threadSDK.Messages().Get(ctx, entryID)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if message == nil {
		return nil, apierr.ErrNotFound("message", fmt.Sprintf("%d", entryID))
	}

	// The prefill's subject survives the fallback. Otherwise the message is the source
	// of truth; entry and topic names are the last body-free fallback.
	if target.Subject == "" {
		target.Subject = replySubject(message.Subject)
		if target.Subject == "" {
			target.Subject = replySubject(entry.Subject)
		}
		if target.Subject == "" {
			target.Subject = replySubject(topic.Name)
		}
	}
	target.Addressed = recipientsForReplyTo(*message)
	target.RecipientsResolved = true
	return target, nil
}

func replyHasRecipients(addressed replyRecipients) bool {
	return len(addressed.To)+len(addressed.CC)+len(addressed.BCC) > 0
}

// applyReplyRecipientOverrides merges explicit recipient lines into HEY's prefill, or
// replaces the prefill whole. An explicitly named address moves to the requested line
// rather than being sent twice. Conflicting explicit lines are refused because there
// is no honest way to decide which one the caller intended.
func applyReplyRecipientOverrides(addressed, overrides replyRecipients, replace bool) (replyRecipients, error) {
	overrides, err := distinctReplyRecipients(overrides)
	if err != nil {
		return replyRecipients{}, err
	}
	if !replyHasRecipients(overrides) {
		if replace {
			return replyRecipients{}, apierr.ErrUsage("--replace-recipients requires at least one --to, --cc or --bcc address")
		}
		return addressed, nil
	}
	if replace {
		return overrides, nil
	}

	explicit := make(map[string]bool)
	for _, line := range [][]string{overrides.To, overrides.CC, overrides.BCC} {
		for _, address := range line {
			explicit[strings.ToLower(address)] = true
		}
	}
	seen := make(map[string]bool)
	merged := replyRecipients{
		To:  keepReplyRecipients(addressed.To, explicit, seen),
		CC:  keepReplyRecipients(addressed.CC, explicit, seen),
		BCC: keepReplyRecipients(addressed.BCC, explicit, seen),
	}
	merged.To = append(merged.To, overrides.To...)
	merged.CC = append(merged.CC, overrides.CC...)
	merged.BCC = append(merged.BCC, overrides.BCC...)
	return merged, nil
}

func distinctReplyRecipients(addressed replyRecipients) (replyRecipients, error) {
	seen := make(map[string]string)
	distinct := replyRecipients{}
	lines := []struct {
		name      string
		addresses []string
		target    *[]string
	}{
		{name: "to", addresses: addressed.To, target: &distinct.To},
		{name: "cc", addresses: addressed.CC, target: &distinct.CC},
		{name: "bcc", addresses: addressed.BCC, target: &distinct.BCC},
	}
	for _, line := range lines {
		for _, address := range line.addresses {
			address = strings.TrimSpace(address)
			if address == "" {
				continue
			}
			key := strings.ToLower(address)
			if previous, found := seen[key]; found {
				if previous != line.name {
					return replyRecipients{}, apierr.ErrUsage(fmt.Sprintf("recipient %s was specified for both --%s and --%s", address, previous, line.name))
				}
				continue
			}
			seen[key] = line.name
			*line.target = append(*line.target, address)
		}
	}
	return distinct, nil
}

func keepReplyRecipients(addresses []string, excluded, seen map[string]bool) []string {
	var kept []string
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		key := strings.ToLower(address)
		if address == "" || excluded[key] || seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, address)
	}
	return kept
}

func replySenderForPreview(ctx context.Context, target *threadReplyTarget) (mail.ReplySender, error) {
	if target.Sender.ID > 0 && target.Sender.EmailAddress != "" {
		return target.Sender, nil
	}

	identity, err := rootSDK.Identity().GetIdentity(ctx)
	if err != nil {
		return mail.ReplySender{}, apierr.FromSDK(err)
	}
	if identity == nil {
		return mail.ReplySender{}, apierr.ErrAPI(0, "HEY returned no identity data")
	}

	var fallback *generated.Sender
	for i := range identity.Senders {
		sender := &identity.Senders[i]
		if sender.AccountId != target.AccountID {
			continue
		}
		if target.ActingSenderID > 0 && sender.Id == target.ActingSenderID {
			return previewReplySender(*sender), nil
		}
		if fallback == nil || sender.Default {
			fallback = sender
		}
	}
	if target.ActingSenderID == 0 && fallback != nil {
		return previewReplySender(*fallback), nil
	}
	return mail.ReplySender{}, apierr.ErrNotFound("sender for account", fmt.Sprintf("%d", target.AccountID))
}

func previewReplySender(sender generated.Sender) mail.ReplySender {
	return mail.ReplySender{ID: sender.Id, Name: sender.Name, EmailAddress: sender.EmailAddress}
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
	return recipientsForReplyAddressed(message.Addressed, sender)
}

func recipientsForReplyAddressed(addressed generated.Addressed, sender string) replyRecipients {
	recipients := replyRecipients{
		To:  addressesOf(addressed.Directly, sender),
		CC:  addressesOf(addressed.Copied, sender),
		BCC: addressesOf(addressed.Blindcopied, sender),
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
