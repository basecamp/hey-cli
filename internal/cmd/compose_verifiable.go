package cmd

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type verifiableReadbackChecks struct {
	Readable           bool `json:"readable"`
	MessageID          bool `json:"message_id"`
	DeliveryTopic      bool `json:"delivery_topic"`
	VerificationStatus bool `json:"verification_status"`
	Sender             bool `json:"sender"`
	Subject            bool `json:"subject"`
	To                 bool `json:"to"`
	CC                 bool `json:"cc"`
	BCCDisclosed       bool `json:"bcc_disclosed"`
	BCC                bool `json:"bcc"`
	Body               bool `json:"body"`
}

func (c verifiableReadbackChecks) exact() bool {
	return c.Readable && c.MessageID && c.DeliveryTopic && c.VerificationStatus && c.Sender && c.Subject &&
		c.To && c.CC && c.BCCDisclosed && c.BCC && c.Body
}

// composeVerifiably creates an unsent message first so the delivery has an identifier
// before it begins. The SDK contract defines CreateDraft's answer as the entry ID used
// by SendDraft and Messages.Get; SendDraft revises that same /messages/{id} resource in
// place. That gives an ambiguous send one safe reconciliation query: this ID, never a
// subject/time search and never a second send.
func composeVerifiably(ctx context.Context, client *hey.Client, sent composeSent) (composeResult, error) {
	senderID, err := actingSenderID(ctx, client, 0)
	if err != nil {
		return composeResult{}, notDispatched(err)
	}

	draft := hey.DraftContent{
		Subject: sent.Subject, Content: sent.Content,
		To: sent.To, CC: sent.CC, BCC: sent.BCC,
		ActingSenderID: senderID,
	}
	draftID, err := client.Messages().CreateDraft(ctx, draft)
	if err != nil {
		// No delivery request has happened. A missing draft handle may leave an unsent
		// draft behind, but it cannot have delivered this message.
		return composeResult{}, apierr.FromSDK(err)
	}
	if err := verifyCreatedDraft(ctx, client, draftID, sent); err != nil {
		return composeResult{}, notDispatched(err)
	}

	if sendErr := client.Messages().SendDraft(ctx, draftID, draft); sendErr != nil {
		classified := classifySendFailure(sendErr)
		if apierr.AsError(classified).Code != apierr.CodeAmbiguous {
			return composeResult{}, classified
		}
	}

	verification, readback := verifyComposedMessage(ctx, client, draftID, sent)
	result := composeResultFor(composeHandle{MessageID: draftID}, verification, readback)
	checks := verifiableReadbackChecks{Readable: readback != nil}
	if readback != nil {
		checks.MessageID = readback.Id == draftID
		checks.DeliveryTopic = deliveredTopicID(readback.Url) > 0
		checks.VerificationStatus = verification.Status == verificationVerified
		checks.Sender = readback.Sender.Id > 0 && readback.Sender.Id == senderID
		if verification.MatchesSent != nil {
			checks.Subject = verification.MatchesSent.Subject
			checks.Body = verification.MatchesSent.Body
		}
		if verification.Recipients != nil {
			recipients := verification.Recipients
			untruncated := !recipients.Truncated
			checks.To = untruncated && sameAddresses(sent.To, recipients.To)
			checks.CC = untruncated && sameAddresses(sent.CC, recipients.CC)
			checks.BCCDisclosed = recipients.BCCDisclosed
			checks.BCC = untruncated && recipients.BCCDisclosed && sameAddresses(sent.BCC, recipients.BCC)
		}
	}
	if checks.exact() {
		return result, nil
	}
	return composeResult{}, unknownVerifiableCompose(draftID, checks)
}

// verifyCreatedDraft proves the identifier returned by CreateDraft names the exact draft
// just saved before the one delivery request is allowed. The SDK returns only a numeric ID
// parsed from Location; GetEdit binds that ID back to a draft resource and its full content.
func verifyCreatedDraft(ctx context.Context, client *hey.Client, draftID int64, sent composeSent) error {
	draft, err := client.Messages().GetEdit(ctx, draftID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if draft == nil {
		return apierr.ErrAPI(0, "saved draft readback was empty; delivery was not attempted")
	}
	recipients := addressedFrom(draft.Addressed)
	exact := draft.Id == draftID &&
		draft.Subject == sent.Subject &&
		bodyDigest(canonicalBody(draft.Content)) == bodyDigest(canonicalBody(sent.Content)) &&
		!recipients.Truncated &&
		sameAddresses(sent.To, recipients.To) &&
		sameAddresses(sent.CC, recipients.CC) &&
		recipients.BCCDisclosed &&
		sameAddresses(sent.BCC, recipients.BCC)
	if !exact {
		return apierr.ErrAPI(0, "saved draft did not read back exactly; delivery was not attempted")
	}
	return nil
}

// deliveredTopicID accepts a topic only from the URL path. Query strings often carry a
// return_to=/topics/... navigation hint even while the resource itself is still a draft.
func deliveredTopicID(raw string) int64 {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	const prefix = "/topics/"
	path := parsed.EscapedPath()
	if !strings.HasPrefix(path, prefix) {
		return 0
	}
	rawID := strings.TrimPrefix(path, prefix)
	if rawID == "" || rawID[0] < '1' || rawID[0] > '9' {
		return 0
	}
	for _, digit := range rawID[1:] {
		if digit < '0' || digit > '9' {
			return 0
		}
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func unknownVerifiableCompose(draftID int64, checks verifiableReadbackChecks) error {
	err := apierr.ErrAmbiguousOutcome(
		"the message may have been sent, but its known message ID did not produce an exact readback",
		"Do not retry: reconcile message_id from this error with HEY; the one delivery request may already have succeeded.")
	err.Meta = map[string]any{
		"message_id":     draftID,
		"reconciliation": checks,
	}
	return err
}
