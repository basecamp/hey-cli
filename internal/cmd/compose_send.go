package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// A send is posted here rather than through MessagesService.Create and
// EntriesService.CreateReply because both of those return an error and nothing else:
// the response — its status, its Location header, its body — is dropped on the floor,
// and with it the only thing that names the message that was just created. A caller
// that has to prove what it sent cannot work from "no error"; see composeHandle.
//
// The request bodies are the SDK's own generated wire types, so the shape of the
// request stays defined in one place and this is a response-preserving wrapper rather
// than a second definition of the API. The transport is the SDK's too — the same auth,
// the same account scope, the same response cap — and POST is never retried there, so
// nothing here can deliver a message twice.
//
// The durable home for this is a CreateWithResult on the SDK's own services. Until
// there is one, keep these two functions the only place hey-cli builds a send by hand.

// sendMessage starts a new thread and answers the response HEY gave.
func sendMessage(ctx context.Context, client *hey.Client, subject, content string, to, cc, bcc []string) (*hey.Response, error) {
	senderID, err := actingSenderID(ctx, client, 0)
	if err != nil {
		return nil, notDispatched(err)
	}
	body := generated.CreateMessageRequestContent{
		ActingSenderId: senderID,
		Message:        generated.MessagePayload{Subject: subject, Content: content},
		Entry: &generated.MessageEntryPayload{
			Addressed: &generated.MessageAddressed{Directly: to, Copied: cc, Blindcopied: bcc},
		},
	}
	return client.PostMutation(ctx, "/messages.json", body)
}

// sendReply answers an entry and answers the response HEY gave. The acting sender is
// the one the thread resolved to, since a thread on a shared or alternate address does
// not go out as the account default.
func sendReply(ctx context.Context, client *hey.Client, entryID, actingSender int64, subject, content string, to, cc, bcc []string) (*hey.Response, error) {
	senderID, err := actingSenderID(ctx, client, actingSender)
	if err != nil {
		return nil, notDispatched(err)
	}
	body := generated.CreateReplyRequestContent{
		ActingSenderId: senderID,
		Message:        generated.ReplyMessagePayload{Subject: subject, Content: content},
		Entry: &generated.MessageEntryPayload{
			Addressed: &generated.MessageAddressed{Directly: to, Copied: cc, Blindcopied: bcc},
		},
	}
	return client.PostMutation(ctx, fmt.Sprintf("/entries/%d/replies.json", entryID), body)
}

// actingSenderID resolves the identity a send goes out as, the way the SDK's services
// do: a sender the caller chose stands, and zero falls back to the account's default.
func actingSenderID(ctx context.Context, client *hey.Client, chosen int64) (int64, error) {
	if chosen != 0 {
		return chosen, nil
	}
	return client.DefaultSenderID(ctx)
}

// --- Classifying how a send failed ---

// errNotDispatched marks a failure that happened before the send's own request was put
// on the wire — resolving the acting sender, which is a read. Nothing was sent, so such
// a failure keeps whatever taxonomy it came with instead of becoming an ambiguous
// outcome. It is a marker rather than a replacement: the original error travels inside
// it, so errors.As still finds the *hey.Error underneath.
var errNotDispatched = errors.New("the send was not dispatched")

func notDispatched(err error) error {
	return fmt.Errorf("%w: %w", errNotDispatched, err)
}

// classifySendFailure decides what a failed send means to whoever has to act on it.
//
// A send is not idempotent and this endpoint carries no idempotency key, so the only
// question that matters is whether the failure proves HEY did not act. Two outcomes
// prove it: the request was never dispatched, or HEY answered with a status that is
// itself a refusal. Everything else — a connection that died with the request already
// on it, a 5xx, an answer that could not be read, an error nothing here recognises —
// leaves a message that may already be in somebody's inbox.
//
// So the ambiguous case is the default, not a special case. The failure mode of getting
// this backwards is a caller reading `network` off a send HEY completed, retrying, and
// delivering twice; the failure mode of getting it wrong this way is a caller going to
// look at a thread that turns out to be empty. Only one of those is recoverable.
//
// This is deliberately not applied to reads. `hey thread read` failing on the network
// is a network failure: reading again costs nothing and delivers nothing. The
// classification belongs to the two send call sites in compose.go and nowhere else.
//
// Nothing here retries. The SDK does not retry a POST either, except once after a 401
// it refreshed credentials for — and a 401 is HEY declining to act at all, so that
// retry cannot duplicate a delivery.
func classifySendFailure(err error) error {
	if err == nil {
		return nil
	}
	// Never on the wire, so whatever went wrong is still just what went wrong.
	if errors.Is(err, errNotDispatched) {
		return apierr.FromSDK(err)
	}
	if rejected(err) {
		return apierr.FromSDK(err)
	}
	return apierr.ErrAmbiguousOutcome(
		fmt.Sprintf("the message may have been sent: %s", indeterminateReason(err)),
		"Read the thread back before sending again — this endpoint has no idempotency key, so a retry may deliver the message twice.")
}

// rejected reports whether the failure is HEY declining the request, which is the one
// thing that proves no message was created.
//
// It works from the code and the status rather than from the sentence, and it is an
// allowlist: a failure that does not match is ambiguous. The SDK does not set an HTTP
// status on every refusal it builds — ErrAuth and ErrNotFound carry none — so the code
// is checked first and the status second, and a bare error (a response that could not
// be read, say) matches neither.
func rejected(err error) bool {
	var sdkErr *hey.Error
	if !errors.As(err, &sdkErr) {
		return false
	}
	switch sdkErr.Code {
	case hey.CodeUsage, hey.CodeAuth, hey.CodeForbidden, hey.CodeNotFound,
		hey.CodeRateLimit, hey.CodeValidation, hey.CodeConflict:
		// Each of these is HEY refusing before it acts: a malformed request, a
		// credential it would not take, a scope it would not allow, a route or entry
		// that is not there, a limit it turned the request away at, contents it would
		// not accept, or a state it conflicts with.
		return true
	}
	// Anything else HEY put a 4xx on is the same kind of answer: the request was bad
	// and was not carried out. A 5xx is not — see the mutation contract in the SDK,
	// which is why it does not retry one either.
	return sdkErr.HTTPStatus >= 400 && sdkErr.HTTPStatus <= 499
}

// indeterminateReason says which way the send became unknowable, in the CLI's own
// words. It never quotes the server: what is useful here is which of the three shapes
// this was, and a reader who wants the detail has the thread to go and look at.
func indeterminateReason(err error) string {
	var sdkErr *hey.Error
	switch {
	case errors.Is(err, hey.ErrResponseTooLarge):
		return "HEY answered, but its answer was past the size this reads and could not be taken in"
	case errors.As(err, &sdkErr) && sdkErr.HTTPStatus >= 500:
		return fmt.Sprintf("HEY answered %d, which does not say whether it acted on the request", sdkErr.HTTPStatus)
	case errors.As(err, &sdkErr) && sdkErr.Code == hey.CodeNetwork:
		return "the connection failed with the request already sent, so HEY's answer never arrived"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "the request was given up on before HEY answered"
	default:
		return "HEY's answer could not be read, so what it did with the request is unknown"
	}
}
