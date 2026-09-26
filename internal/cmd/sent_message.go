package cmd

import (
	"fmt"
	"maps"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

// messageSent is how a command confirms a delivery, before HEY's answer is added to it.
type messageSent struct {
	// line is how the styled line starts ("Message sent", "Draft 12345 sent"), and
	// summary the envelope's summary; they differ only where the line names an id.
	line    string
	summary string
	// reported is what the command reported before HEY named the entry, kept beside
	// HEY's answer; HEY's keys win over it.
	reported map[string]any
	// thread is the thread a reply addressed, which the "view" breadcrumb falls back to
	// when HEY does not name one.
	thread int64
}

// writeMessageSent confirms a message HEY has delivered — a new one, a reply, a forward
// or a sent draft. HEY answers a delivery with the entry that went out and the thread it
// is on, so the line names the thread and the envelope carries both, with the subject and
// whether Undo Send is holding the delivery back. A HEY that predates the ids answers
// without them, and so does this: nothing is guessed in their place.
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
	return writeMutationLine(cmd, sentMessageLine(confirmation.line, sent), confirmation.summary, data, opts...)
}

// sentMessageData is HEY's answer for a delivery as the JSON output carries it: the keys
// HEY served, and only those. delayed is reported once HEY names the entry, or when an
// undo says the delivery is delayed; a bare {} says nothing either way.
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
	if sent.Subject != "" {
		data["subject"] = sent.Subject
	}
	if sent.Id != 0 || sent.Delayed {
		data["delayed"] = sent.Delayed
	}
	return data
}

// sentMessageLine is the styled confirmation: the line's lead, the thread HEY put the
// message on, and a word that Undo Send is holding it back.
func sentMessageLine(lead string, sent *generated.SentMessage) string {
	switch {
	case sent == nil:
		return lead + "."
	case sent.TopicId != 0 && sent.Delayed:
		return fmt.Sprintf("%s (thread %d); Undo Send is holding it back.", lead, sent.TopicId)
	case sent.TopicId != 0:
		return fmt.Sprintf("%s (thread %d).", lead, sent.TopicId)
	case sent.Delayed:
		return lead + "; Undo Send is holding it back."
	default:
		return lead + "."
	}
}
