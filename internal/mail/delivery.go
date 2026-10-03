package mail

import (
	"context"
	"fmt"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// NotDeliveredError is a send HEY refused: it kept the message as a draft instead, and
// DraftID names that draft.
type NotDeliveredError struct {
	DraftID int64
}

func (e *NotDeliveredError) Error() string {
	return fmt.Sprintf("HEY kept this message as draft %d instead of sending it, usually because the account has reached its sending limit", e.DraftID)
}

// ConfirmDelivered checks that a message HEY answered for was delivered, and returns a
// NotDeliveredError when it was not.
//
// HEY answers a delivery with the entry that went out and the thread it went out on
// (entries/_sent.jbuilder). When it refuses one — the account has reached its sending
// limit, or the delivery could not be queued — it keeps the message as a draft and
// redirects to the draft's edit page instead (EntryResponses), whatever the format asked
// for. The SDK follows that redirect, so the answer a caller gets is the draft: an id and
// no thread. That is the sign looked for here, and it is confirmed before anything is
// said, because the edit page is served only while the message is still a draft — HEY
// answers 422 for one that went out, Undo Send's held ones included.
//
// A HEY that predates the ids answers a delivery with no id at all, and is taken at its
// word. So is an answer whose confirmation could not be read: telling somebody a message
// was not sent when it was would have them send it twice.
func ConfirmDelivered(ctx context.Context, client *hey.Client, sent *generated.SentMessage) error {
	if sent == nil || sent.Id == 0 || sent.TopicId != 0 {
		return nil
	}
	// Only an edit page served says the message is still a draft; a 422 — it went out —
	// and a read that failed are both taken as sent.
	if edit, err := client.Messages().GetEdit(ctx, sent.Id); err == nil && edit != nil {
		return &NotDeliveredError{DraftID: sent.Id}
	}
	return nil
}
