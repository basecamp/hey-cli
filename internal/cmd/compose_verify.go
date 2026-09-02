package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
)

// maxVerifiedBodyBytes is the most Markdown a verification carries inline. A body past
// it is left out and compared by digest instead: half a body invites a comparison that
// looks like it succeeded, and the digest — which is over the whole of it either way —
// answers the only question a verifier is really asking.
const maxVerifiedBodyBytes = 64 << 10

// The statuses a verification reports. They are three different instructions to a
// caller, which is the whole reason they are separate words.
const (
	// verificationVerified: the message was read back and everything comparable about
	// it matches what was asked for.
	verificationVerified = "verified"
	// verificationMismatch: the message was read back and something differs. The
	// message exists — this is never a reason to send again.
	verificationMismatch = "mismatch"
	// verificationUnverified: the message could not be read back. It may well exist,
	// so this is not a failed send either; Reason says what stopped the read.
	verificationUnverified = "unverified"
)

// composeSent is what the caller asked to be sent, kept so the readback has something
// to be compared against.
type composeSent struct {
	Subject string
	// Content is the Trix HTML that actually went on the wire, attachments included.
	Content string
	To      []string
	CC      []string
	BCC     []string
}

// composeVerification is what reading the message back showed. It is deliberately
// bounded: the recipient lists stop at maxRetainedRecipients and the body at
// maxVerifiedBodyBytes, so a verification is a fixed-size statement about a message
// rather than a copy of whatever the server chose to serve.
type composeVerification struct {
	Status string `json:"status"`
	Method string `json:"method"`
	// Reason is set only when Status is unverified, and says what stopped the read.
	Reason string `json:"reason,omitempty"`

	Subject    string             `json:"subject,omitempty"`
	Sender     *threadContact     `json:"sender,omitempty"`
	Recipients *addressedEnvelope `json:"recipients,omitempty"`

	// BodyMarkdown is the stored body as canonical Markdown — the same conversion
	// `hey thread read` publishes, so the two agree byte for byte.
	BodyMarkdown htmlutil.Markdown `json:"body_markdown,omitzero"`
	// BodyTruncated says the body was past maxVerifiedBodyBytes and was left out;
	// BodyMarkdownSHA256 still covers the whole of it.
	BodyTruncated bool `json:"body_truncated,omitempty"`
	// BodyMarkdownSHA256 is over the canonical Markdown, not over HEY's HTML: HTML is
	// the server's to reformat, Markdown is what both ends can agree on.
	BodyMarkdownSHA256 string `json:"body_markdown_sha256,omitempty"`

	MatchesSent *composeMatches `json:"matches_sent,omitempty"`
}

// composeMatches compares what came back with what was sent, one answer per thing a
// sender cares about.
type composeMatches struct {
	Subject bool `json:"subject"`
	Body    bool `json:"body"`
	// Recipients holds when the To and CC lines are exactly the ones asked for and
	// every BCC address served back was asked for too. An undisclosed BCC does not
	// break it — HEY commonly serves none — but a recipient nobody asked for does.
	Recipients bool `json:"recipients"`
}

// verifyComposedMessage reads the created message back and says what that showed.
//
// It is the readback half of the send contract: `hey compose` reports what HEY stored,
// not what it was handed, so a caller can prove the identity of the message it just
// created instead of trusting a status code. The read is `Messages().Get` — the same
// request `hey thread read` makes per entry — so what it reports and what a later
// thread read reports are the same conversion of the same record.
//
// It also answers the message itself, which is where a topic id and an app URL come
// from when the send's own response named neither.
func verifyComposedMessage(ctx context.Context, client *hey.Client, messageID int64, sent composeSent) (composeVerification, *generated.Message) {
	if messageID == 0 {
		return composeVerification{
			Status: verificationUnverified,
			Method: "none",
			Reason: "the send named a thread but no message, so there is no message to read back",
		}, nil
	}

	message, err := client.Messages().Get(ctx, messageID)
	if err != nil {
		return composeVerification{
			Status: verificationUnverified,
			Method: "message_read",
			Reason: "the message could not be read back",
		}, nil
	}
	if message == nil {
		return composeVerification{
			Status: verificationUnverified,
			Method: "message_read",
			Reason: "the message read back was empty",
		}, nil
	}

	recipients := addressedFrom(message.Addressed)
	body, truncated, digest := verifiedBody(message.Content)
	matches := composeMatches{
		Subject:    message.Subject == sent.Subject,
		Body:       digest == bodyDigest(canonicalBody(sent.Content)),
		Recipients: recipientsMatch(sent, recipients),
	}

	status := verificationVerified
	if !matches.Subject || !matches.Body || !matches.Recipients {
		status = verificationMismatch
	}

	return composeVerification{
		Status:     status,
		Method:     "message_read",
		Subject:    message.Subject,
		Sender:     senderOf(message),
		Recipients: &recipients,

		BodyMarkdown:       body,
		BodyTruncated:      truncated,
		BodyMarkdownSHA256: digest,

		MatchesSent: &matches,
	}, message
}

// senderOf is the identity the message went out as, falling back to whoever wrote it
// when HEY names no separate sender.
func senderOf(message *generated.Message) *threadContact {
	contact := message.Sender
	if contact.EmailAddress == "" && contact.Name == "" {
		contact = message.Creator
	}
	if contact.EmailAddress == "" && contact.Name == "" && contact.Id == 0 {
		return nil
	}
	return &threadContact{ID: contact.Id, Name: contact.Name, EmailAddress: contact.EmailAddress}
}

// verifiedBody converts a stored body to canonical Markdown, within the inline bound.
// The digest is over the whole of it whether or not the Markdown itself is carried, so
// a body too large to publish can still be compared.
func verifiedBody(content string) (body htmlutil.Markdown, truncated bool, digest string) {
	markdown := htmlutil.ToMarkdown(content)
	digest = bodyDigest(markdown.String())
	if len(markdown.String()) > maxVerifiedBodyBytes {
		return htmlutil.Markdown{}, true, digest
	}
	return markdown, false, digest
}

// canonicalBody is the sent body in the same form the readback is measured in, so the
// two are compared as Markdown rather than as HTML. HEY is free to reformat the markup
// it stores; what it may not do is change what the message says.
func canonicalBody(content string) string {
	return htmlutil.ToMarkdown(content).String()
}

// bodyDigest is SHA-256 over the canonical Markdown, hex-encoded.
func bodyDigest(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

// recipientsMatch reports whether the message reached exactly who it was meant to.
//
// To and CC must be the sets that were asked for, in any order. BCC is one-sided: HEY
// serves no blindcopied line back for a message it delivered, and an empty list is not
// proof that nobody was blind-copied, so a BCC that came back must have been asked for
// but one that did not come back costs nothing. What that leaves refused is the case
// worth refusing — a recipient nobody asked for.
func recipientsMatch(sent composeSent, got addressedEnvelope) bool {
	return sameAddresses(sent.To, got.To) &&
		sameAddresses(sent.CC, got.CC) &&
		addressSubset(got.BCC, sent.BCC)
}

func sameAddresses(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	return addressSubset(got, want)
}

func addressSubset(got, want []string) bool {
	allowed := make(map[string]struct{}, len(want))
	for _, address := range want {
		allowed[normalizeAddress(address)] = struct{}{}
	}
	for _, address := range got {
		if _, ok := allowed[normalizeAddress(address)]; !ok {
			return false
		}
	}
	return true
}

// normalizeAddress folds case, which is how every mail host in practice treats an
// address, so a message that came back as Alice@example.com is not reported as having
// reached somebody else.
func normalizeAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// composeResult is the machine contract a send answers with: the handle, and what
// reading the message back showed. `sent` is always true here — a send that was not
// accepted is an error, and one that was accepted but named nothing is an ambiguous
// error, never this.
type composeResult struct {
	Sent         bool                `json:"sent"`
	MessageID    int64               `json:"message_id,omitempty"`
	TopicID      int64               `json:"topic_id,omitempty"`
	AppURL       string              `json:"app_url,omitempty"`
	Verification composeVerification `json:"verification"`
}

// composeResultFor assembles the answer from the handle the send named and the message
// that was read back. The readback fills in a thread id and an app URL the send's own
// response did not carry — a message names its thread in its URL — and never overrides
// one it did.
func composeResultFor(handle composeHandle, verification composeVerification, message *generated.Message) composeResult {
	result := composeResult{
		Sent:         true,
		MessageID:    handle.MessageID,
		TopicID:      handle.TopicID,
		AppURL:       handle.AppURL,
		Verification: verification,
	}
	if message != nil {
		if result.AppURL == "" {
			result.AppURL = message.Url
		}
		if result.TopicID == 0 {
			result.TopicID = mail.TopicIDIn(message.Url)
		}
	}
	return result
}
