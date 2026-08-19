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
	Subject   string
	Addressed *htmlutil.ReplyForm
}

// resolveThreadReply works out what replying to a thread means: the last entry on it, and
// the live envelope HEY prepared for that reply.
func resolveThreadReply(ctx context.Context, threadID int64) (*threadReplyTarget, error) {
	return resolveThreadReplyAtEntry(ctx, threadID, 0)
}

// resolveThreadReplyAtEntry refuses to load a reply envelope when the latest entry no
// longer matches the one the user previewed.
func resolveThreadReplyAtEntry(ctx context.Context, threadID, expectedEntryID int64) (*threadReplyTarget, error) {
	topic, err := sdk.Topics().Get(ctx, threadID)
	if err != nil {
		return nil, convertSDKError(err)
	}
	if topic == nil || topic.LatestEntry.Id <= 0 {
		return nil, output.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}
	if expectedEntryID > 0 && topic.LatestEntry.Id != expectedEntryID {
		return nil, output.ErrUsageHint(
			fmt.Sprintf("thread changed after preview: expected entry %d, latest entry is %d", expectedEntryID, topic.LatestEntry.Id),
			fmt.Sprintf("Run: hey reply %d -m <message> --preview --json", threadID),
		)
	}

	replyResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/entries/%d/replies/new", topic.LatestEntry.Id))
	if err != nil {
		return nil, convertSDKError(err)
	}
	addressed, err := htmlutil.ParseReplyFormHTML(string(replyResp.Data))
	if err != nil {
		return nil, output.ErrUsage("could not determine thread recipients from HEY's reply form")
	}

	return &threadReplyTarget{
		EntryID:   topic.LatestEntry.Id,
		Subject:   topic.Name,
		Addressed: &addressed,
	}, nil
}
