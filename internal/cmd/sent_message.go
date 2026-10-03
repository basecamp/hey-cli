package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
)

// confirmSent checks that HEY delivered the message it answered for, through the client
// that sent it (mail.ConfirmDelivered), and turns a refusal into the CLI's error for it:
// not_delivered, naming the draft HEY kept and the command that sends it. Every command
// that delivers a message asks this before confirming it.
func confirmSent(ctx context.Context, client *hey.Client, sent *generated.SentMessage) error {
	var refused *mail.NotDeliveredError
	if err := mail.ConfirmDelivered(ctx, client, sent); errors.As(err, &refused) {
		return &apierr.Error{
			Code:    apierr.CodeNotDelivered,
			Message: refused.Error(),
			Hint:    fmt.Sprintf("hey draft send %d", refused.DraftID),
			Meta:    map[string]any{"draft_id": refused.DraftID},
		}
	}
	return nil
}

// messageSent is how a command confirms a delivery, before HEY's answer is added to it.
type messageSent struct {
	// line is how the styled line starts ("Message sent", "Draft 12345 sent"), and
	// summary the envelope's summary; they differ only where the line names an id.
	line    string
	summary string
	// lineID is an id the line already names — the draft in "Draft 12345 sent" — which
	// the line does not repeat when HEY delivers the message under that same id.
	lineID int64
	// reported is what the command reported before HEY named the entry, kept beside
	// HEY's answer; HEY's keys win over it.
	reported map[string]any
	// thread is the thread a reply addressed, which the "view" breadcrumb falls back to
	// when HEY does not name one.
	thread int64
}

// writeMessageSent confirms a message HEY has delivered — a new one, a reply, a forward
// or a sent draft. HEY answers a delivery with the entry that went out and the thread it
// is on, so the line and the envelope both name them, the envelope with the subject and
// whether Undo Send is holding the delivery back. An answer that names no entry carries
// no ids, and neither does this: nothing is guessed in their place.
func writeMessageSent(cmd *cobra.Command, confirmation messageSent, sent *generated.SentMessage) error {
	data := map[string]any{}
	maps.Copy(data, confirmation.reported)
	maps.Copy(data, sentMessageData(sent))

	threadID := confirmation.thread
	if sent != nil && sent.TopicId != 0 {
		threadID = sent.TopicId
	}
	var opts []output.ResponseOption
	if threadID != 0 {
		opts = append(opts, output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey thread read %d", threadID),
			Description: "Read the thread",
		}))
	}
	return writeMutationLine(cmd, sentMessageLine(confirmation, sent), confirmation.summary, data, opts...)
}

// sentMessageData is HEY's answer for a delivery as the JSON output carries it: the keys
// HEY served, and only those. HEY serves subject and delayed whenever it names the entry,
// so both are reported then — an empty subject included, since a draft can go out without
// one — and the SDK's field, which cannot tell an empty subject from none, is not asked.
// Without the entry, delayed is reported only when an undo says the delivery is delayed;
// a bare {} says nothing either way.
func sentMessageData(sent *generated.SentMessage) map[string]any {
	data := map[string]any{}
	if sent == nil {
		return data
	}
	if sent.Id != 0 {
		data["id"] = sent.Id
	}
	if sent.TopicId != 0 {
		data["topic_id"] = sent.TopicId
	}
	if sent.Id != 0 {
		data["subject"] = sent.Subject
	}
	if sent.Id != 0 || sent.Delayed {
		data["delayed"] = sent.Delayed
	}
	return data
}

// sentMessageLine is the styled confirmation: the line's lead, the message HEY delivered
// and the thread it put it on, and a word that Undo Send is holding it back.
func sentMessageLine(confirmation messageSent, sent *generated.SentMessage) string {
	line := confirmation.line
	if sent == nil {
		return line + "."
	}

	var named []string
	if sent.Id != 0 && sent.Id != confirmation.lineID {
		named = append(named, fmt.Sprintf("message %d", sent.Id))
	}
	if sent.TopicId != 0 {
		named = append(named, fmt.Sprintf("thread %d", sent.TopicId))
	}
	if len(named) > 0 {
		line += " (" + strings.Join(named, ", ") + ")"
	}

	if sent.Delayed {
		return line + "; Undo Send is holding it back."
	}
	return line + "."
}
