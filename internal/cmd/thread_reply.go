package cmd

import (
	"context"
	"fmt"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

// threadReplyTarget is what a reply to a thread needs: the entry it answers, and the
// recipients that entry was addressed to. HEY saves an unaddressed reply as a draft, so
// the recipients are not optional.
type threadReplyTarget struct {
	EntryID   int64
	AccountID int64
	Addressed *htmlutil.TopicAddressed
}

// resolveThreadReply returns the thread's latest entry, linked account, and addressed
// recipients. The typed topic carries entry and account data; the rendered topic header
// carries the recipient groups HEY expects on a reply.
func resolveThreadReply(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	topic, err := sdk.Topics().Get(ctx, threadID)
	if err != nil {
		return nil, convertSDKError(err)
	}
	if topic == nil || len(topic.Entries) == 0 {
		return nil, output.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}

	topicResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d", threadID))
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
	}, nil
}
