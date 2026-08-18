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
	Addressed *htmlutil.TopicAddressed
}

// resolveThreadReply works out what replying to a thread means: the last entry on it, and
// who that entry went to.
//
// It still reads the topic's HTML pages. The SDK gained typed reads for both in v0.4.0 —
// Topics.Get carries the topic's entries — and this is the one place to change when the
// CLI moves onto them.
func resolveThreadReply(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	topicResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d", threadID))
	if err != nil {
		return nil, convertSDKError(err)
	}
	addressed := htmlutil.ParseTopicAddressed(string(topicResp.Data))
	if len(addressed.To) == 0 && len(addressed.CC) == 0 && len(addressed.BCC) == 0 {
		return nil, output.ErrUsage("could not determine thread recipients")
	}

	entriesResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d/entries", threadID))
	if err != nil {
		return nil, convertSDKError(err)
	}
	entries := htmlutil.ParseTopicEntriesHTML(string(entriesResp.Data))
	if len(entries) == 0 {
		return nil, output.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}

	return &threadReplyTarget{EntryID: entries[len(entries)-1].ID, Addressed: addressed}, nil
}
