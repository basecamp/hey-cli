package mail

import (
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// ReplyRecipientsFor answers who a reply to this message goes to when HEY's own prefill
// is out of reach: the message's own recipients, with whoever sent it moved onto the To
// line. That is what HEY does in Entry::Addressed#participating_contacts_in_reply_by_kind,
// so a reply reaches the person who wrote the message as well as everyone they wrote to.
// The people it blind copied stay off the reply, as HEY leaves them off, unless they are
// all the audience there is: see keepsBlindCopied.
func ReplyRecipientsFor(message generated.Message) ReplyRecipients {
	sender := message.Sender.EmailAddress
	if sender == "" {
		sender = message.Creator.EmailAddress
	}
	return replyRecipientsAddressed(message.Addressed, sender)
}

func replyRecipientsAddressed(addressed generated.Addressed, sender string) ReplyRecipients {
	recipients := ReplyRecipients{
		To: addressesOf(addressed.Directly, sender),
		CC: addressesOf(addressed.Copied, sender),
	}
	if keepsBlindCopied(addressed, sender) {
		recipients.BCC = addressesOf(addressed.Blindcopied, sender)
	}
	if sender != "" {
		recipients.To = append(recipients.To, sender)
	}
	return recipients
}

// keepsBlindCopied is HEY's rule (haystack#9169) for whether a reply keeps the people the
// answered message blind copied: only when they are the whole audience, because blind
// copying is often "this once" and a follow-up shouldn't reach them unseen. Nobody is left
// to address once the sender, who is the replier when they answer their own message
// (the fallback cannot tell otherwise), is set aside, as with "To: me, Bcc: the list"
// or a thread of only blind copied people.
func keepsBlindCopied(addressed generated.Addressed, sender string) bool {
	visible := append(append([]generated.Contact{}, addressed.Directly...), addressed.Copied...)
	return len(addressesOf(visible, sender)) == 0
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
