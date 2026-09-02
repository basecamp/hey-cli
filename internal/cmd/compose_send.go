package cmd

import (
	"context"
	"fmt"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
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
		return nil, err
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
		return nil, err
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
