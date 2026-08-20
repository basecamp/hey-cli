package cmd

import (
	"context"
	"fmt"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

// threadReplyTarget carries the entry a reply answers, its recipients, and an immutable
// client bound to the thread's mail account. HEY saves an unaddressed reply as a draft,
// so the recipients are not optional.
type threadReplyTarget struct {
	EntryID   int64
	AccountID int64
	Addressed *htmlutil.TopicAddressed
	client    *hey.Client
}

// resolveThreadReply returns the thread's latest entry, linked account, and addressed
// recipients. The typed topic carries entry and account data; the rendered topic header
// carries the recipient groups HEY expects on a reply.
func resolveThreadReply(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	topic, err := rootSDK.Topics().Get(ctx, threadID)
	if err != nil {
		return nil, convertSDKError(err)
	}
	if topic == nil || len(topic.Entries) == 0 {
		return nil, output.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}

	threadSDK, err := clientForResourceAccount(ctx, topic.AccountId)
	if err != nil {
		return nil, err
	}
	topicResp, err := threadSDK.GetHTML(ctx, fmt.Sprintf("/topics/%d", threadID))
	if err != nil {
		return nil, convertSDKError(err)
	}
	addressed := htmlutil.ParseTopicAddressed(string(topicResp.Data))
	if len(addressed.To) == 0 && len(addressed.CC) == 0 && len(addressed.BCC) == 0 {
		return nil, output.ErrUsage("could not determine thread recipients")
	}

	return &threadReplyTarget{
		EntryID:   topic.Entries[len(topic.Entries)-1].Id,
		AccountID: topic.AccountId,
		Addressed: addressed,
		client:    threadSDK,
	}, nil
}
