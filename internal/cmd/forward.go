package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
)

type forwardCommand struct {
	cmd         *cobra.Command
	to          string
	cc          string
	bcc         string
	message     string
	messageHTML string
}

func newForwardCommand() *forwardCommand {
	forwardCommand := &forwardCommand{}
	forwardCommand.cmd = &cobra.Command{
		Use:   "forward <thread-id>",
		Short: "Forward the latest message in a thread",
		Long: `Forward the latest emailed message in a thread, with HEY's quoted content.

A note or share notice posted after it is internal — visible to everyone with access to
the thread, never emailed — so it is skipped, as HEY's web app skips it. A thread holding
nothing but notes and share notices is refused.

A --to, --cc or --bcc address HEY would drop without saying so — one with no domain,
or a top-level domain HEY does not know — is refused before anything is sent.

The answer's thread_id and entry_id name the thread and entry that were forwarded. Beside
them, id and topic_id name the new message and the thread it started, with delayed (true
while Undo Send holds it back). Whatever HEY's answer leaves out is left out here too;
nothing is guessed in its place.`,
		Annotations: map[string]string{
			"agent_notes": "Forwards the latest emailed message in a thread with HEY's quoted content — never an internal note or share notice posted after it. Accepts comma-separated recipients and an optional note via -m. The answer's thread_id and entry_id name what was forwarded; id and topic_id name the new message and the thread it started, as HEY's answer names them.",
		},
		Example: `  hey forward 12345 --to alice@example.com
  hey forward 12345 --to alice@example.com --cc bob@example.org -m "For your review"`,
		RunE: forwardCommand.run,
		Args: recipientsChecked(usageExactOneArg()),
	}

	forwardCommand.cmd.Flags().StringVar(&forwardCommand.to, "to", "", "Recipient email address(es)")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.cc, "cc", "", "CC recipient email address(es)")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	forwardCommand.cmd.Flags().StringVarP(&forwardCommand.message, "message", "m", "", "Optional Markdown note above the forwarded message")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.messageHTML, "message-html", "", "The note as raw HTML instead of Markdown")
	forwardCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")

	return forwardCommand
}

func (c *forwardCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	to := parseAddresses(c.to)
	cc := parseAddresses(c.cc)
	bcc := parseAddresses(c.bcc)
	if len(to)+len(cc)+len(bcc) == 0 {
		return apierr.ErrUsageHint("at least one recipient is required", "hey forward <thread-id> --to <email>")
	}

	ctx := cmd.Context()
	topic, err := rootSDK.Topics().Get(ctx, threadID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if topic == nil || len(topic.Entries) == 0 {
		return apierr.ErrNotFound("entries for thread", args[0])
	}
	forwardSDK, err := clientForResourceAccount(ctx, topic.AccountId)
	if err != nil {
		return err
	}
	entry, err := threadReplyEntry(ctx, forwardSDK, topic)
	if err != nil {
		return err
	}
	entryID := entry.Id

	draft, err := forwardSDK.Entries().NewForward(ctx, entryID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if draft == nil {
		return apierr.ErrNotFound("forward draft for thread", args[0])
	}

	note := c.messageHTML
	if note == "" {
		note = htmlutil.FromMarkdown(c.message)
	}
	content := htmlutil.PrependHTML(draft.Content, note)
	sent, err := forwardSDK.Messages().Create(ctx, draft.Subject, content, to, cc, bcc)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if err := confirmSent(ctx, forwardSDK, sent); err != nil {
		return err
	}

	// thread_id and entry_id name what was forwarded; id and topic_id, from HEY's answer,
	// name the message that went out and the thread it started. An empty recipient line is
	// [] rather than null, as it is in hey reply --dry-run.
	return writeMessageSent(cmd, messageSent{
		line:    "Message forwarded",
		summary: "Message forwarded",
		reported: map[string]any{
			"thread_id": threadID,
			"entry_id":  entryID,
			"subject":   draft.Subject,
			"to":        nonNilAddresses(to),
			"cc":        nonNilAddresses(cc),
			"bcc":       nonNilAddresses(bcc),
		},
	}, sent)
}
