package cmd

import (
	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// maxRetainedRecipients is how many addresses of one kind are kept from a message HEY
// served. A message addressed to a mailing list carries as many recipients as somebody
// else decided to put on it, and every one of them is a name and an address this
// program would otherwise hold and print; the bound is what keeps a read of one message
// from retaining an address book. It is well past any list a person writes by hand.
const maxRetainedRecipients = 100

// addressedEnvelope is who a message reached, as HEY served it back: the To, CC and BCC
// lines as plain addresses. It is what lets a caller prove a message went where it was
// meant to and nowhere else, so it is read off the message HEY answered with rather
// than inferred from a position in a list or found in the body.
//
// BCCDisclosed is the honest part, and it answers "did HEY tell us the BCC line" rather
// than "was anybody on it". Those are different questions, and reading the second as the
// first left a caller unable to prove a message's exact destinations: an empty BCC HEY
// served — which is proof that nobody was blind-copied — had the same shape as one HEY
// withheld, which proves nothing at all.
//
// So it is presence, not population: true when the blindcopied field arrived, an
// explicitly empty array included, and false when it was omitted, null, or there was no
// addressing at all. A caller that must not accept an unexpected recipient reads it
// before treating an empty BCC as settled — false still means unknown.
type addressedEnvelope struct {
	To           []string `json:"to"`
	CC           []string `json:"cc"`
	BCC          []string `json:"bcc"`
	BCCDisclosed bool     `json:"bcc_disclosed"`
	// Truncated says a list was longer than maxRetainedRecipients and was cut to it.
	Truncated bool `json:"truncated,omitempty"`
}

// addressedFrom describes HEY's addressing in the CLI's shape, within the bound.
//
// Disclosure is read from the decoded slice's nil-ness, which is where the presence of
// the field survives: encoding/json leaves an omitted or null array nil and makes an
// explicit `[]` non-nil, generated.Message declares no unmarshaler of its own to flatten
// the two, and threadload's retained copies the slice header rather than rebuilding it.
// It is deliberately not read from len(bcc) — that would report a line HEY served as
// empty as withheld — nor from what the caller asked to send, which is not evidence of
// anything the server did. TestBlindcopiedPresenceSurvivesTheSDKDecode pins the
// invariant through a real HTTP response, since it is the kind that would otherwise
// break in silence.
func addressedFrom(addressed generated.Addressed) addressedEnvelope {
	to, toCut := boundedEmails(addressed.Directly)
	cc, ccCut := boundedEmails(addressed.Copied)
	bcc, bccCut := boundedEmails(addressed.Blindcopied)
	return addressedEnvelope{
		To:           to,
		CC:           cc,
		BCC:          bcc,
		BCCDisclosed: addressed.Blindcopied != nil,
		Truncated:    toCut || ccCut || bccCut,
	}
}

// boundedEmails answers the contacts' addresses, marking the envelope incomplete when a
// contact has no address or when the bound is reached. The addresses are HEY's own, so
// they are carried verbatim;
// whatever prints one sanitizes it there, as every other read of somebody else's text
// does.
func boundedEmails(contacts []generated.Contact) (emails []string, truncated bool) {
	emails = make([]string, 0, min(len(contacts), maxRetainedRecipients))
	for _, contact := range contacts {
		if contact.EmailAddress == "" {
			truncated = true
			continue
		}
		if len(emails) == maxRetainedRecipients {
			return emails, true
		}
		emails = append(emails, contact.EmailAddress)
	}
	return emails, truncated
}
