package mail

import (
	"context"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// ReplyRecipients is who a reply goes out to, in HEY's three kinds of addressing.
type ReplyRecipients struct {
	To  []string
	CC  []string
	BCC []string
}

// ReplyPrefill is how a reply starts out, as HEY prefills it: the "Re: …" subject it
// goes out under, the sender it goes out as, and who it goes out to. The prefill's
// quoted content is deliberately not carried: a reply's content is the writer's body
// alone — the server appends the quoted original at delivery (auto_quoting defaults
// on), so echoing the prefill's quote back would double it.
type ReplyPrefill struct {
	Subject        string
	ActingSenderID int64
	Addressed      ReplyRecipients
}

// ReplyPrefillFromServer asks HEY how a reply to the entry starts out
// (GET /entries/{id}/replies/new): the "Re: …" subject the reply carries; the sender
// it goes out as — resolved from the entry's own to and from addresses, so a thread on
// a shared or alternate address answers as that address, not the account default, and
// named only when it differs from the acting user; and its recipients — the entry's
// sender moved onto the To line and the acting user's own addresses, aliases and
// catch-alls excluded — the exclusion no client can compute locally, and the reason
// a reply used to be able to CC its writer back to themselves. A false answer sends
// the caller to its local fallback: a failed read needs one, and so does an empty
// recipient list — on a thread with yourself, everyone HEY excludes is everyone there
// is, and the local list is what keeps that reply addressable. The subject and sender
// are answered even when the recipients are not — only they need the fallback, not
// what HEY already supplied.
func ReplyPrefillFromServer(ctx context.Context, client *hey.Client, entryID int64) (ReplyPrefill, bool) {
	prefilled, err := client.Entries().NewReply(ctx, entryID)
	if err != nil || prefilled == nil {
		return ReplyPrefill{}, false
	}
	prefill := ReplyPrefill{
		Subject:        prefilled.Subject,
		ActingSenderID: prefilled.Sender.Id,
		Addressed: ReplyRecipients{
			To:  contactEmails(prefilled.Addressed.Directly),
			CC:  contactEmails(prefilled.Addressed.Copied),
			BCC: contactEmails(prefilled.Addressed.Blindcopied),
		},
	}
	if len(prefill.Addressed.To)+len(prefill.Addressed.CC)+len(prefill.Addressed.BCC) == 0 {
		prefill.Addressed = ReplyRecipients{}
		return prefill, false
	}
	return prefill, true
}

// contactEmails answers the contacts' email addresses verbatim, dropping blanks: the
// prefill's lists are HEY's own computation, not input to clean up.
func contactEmails(contacts []generated.Contact) []string {
	var emails []string
	for _, contact := range contacts {
		if contact.EmailAddress != "" {
			emails = append(emails, contact.EmailAddress)
		}
	}
	return emails
}
