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
// BCCDisclosed is the honest part. HEY does not distinguish "there was no BCC" from "we
// are not telling you the BCC" — both arrive as no blindcopied recipients at all — so
// BCC being empty is never proof that nobody was blind-copied. BCCDisclosed is true only
// when HEY actually served addresses, and a caller that must not accept an unexpected
// recipient reads it before treating an empty BCC as settled.
type addressedEnvelope struct {
	To           []string `json:"to"`
	CC           []string `json:"cc"`
	BCC          []string `json:"bcc"`
	BCCDisclosed bool     `json:"bcc_disclosed"`
	// Truncated says a list was longer than maxRetainedRecipients and was cut to it.
	Truncated bool `json:"truncated,omitempty"`
}

// addressedFrom describes HEY's addressing in the CLI's shape, within the bound.
func addressedFrom(addressed generated.Addressed) addressedEnvelope {
	to, toCut := boundedEmails(addressed.Directly)
	cc, ccCut := boundedEmails(addressed.Copied)
	bcc, bccCut := boundedEmails(addressed.Blindcopied)
	return addressedEnvelope{
		To:           to,
		CC:           cc,
		BCC:          bcc,
		BCCDisclosed: len(bcc) > 0,
		Truncated:    toCut || ccCut || bccCut,
	}
}

// boundedEmails answers the contacts' addresses, dropping blanks and stopping at
// maxRetainedRecipients. The addresses are HEY's own, so they are carried verbatim;
// whatever prints one sanitizes it there, as every other read of somebody else's text
// does.
func boundedEmails(contacts []generated.Contact) (emails []string, truncated bool) {
	emails = make([]string, 0, min(len(contacts), maxRetainedRecipients))
	for _, contact := range contacts {
		if contact.EmailAddress == "" {
			continue
		}
		if len(emails) == maxRetainedRecipients {
			return emails, true
		}
		emails = append(emails, contact.EmailAddress)
	}
	return emails, false
}
