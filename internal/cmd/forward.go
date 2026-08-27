package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
)

type forwardCommand struct {
	cmd             *cobra.Command
	to              string
	cc              string
	bcc             string
	message         string
	messageHTML     string
	messageHTMLFile string
}

func newForwardCommand() *forwardCommand {
	forwardCommand := &forwardCommand{}
	forwardCommand.cmd = &cobra.Command{
		Use:   "forward <thread-id>",
		Short: "Forward the latest message in a thread",
		Annotations: map[string]string{
			"agent_notes": "Forwards the latest entry in a thread with HEY's quoted content. Accepts comma-separated recipients and an optional Markdown note via -m; use --message-html for inline raw HTML or --message-html-file to read the raw HTML note from a file.",
		},
		Example: `  hey forward 12345 --to alice@example.com
  hey forward 12345 --to alice@example.com --cc bob@example.org -m "For your review"
  hey forward 12345 --to alice@example.com --message-html-file ./forward-note.html`,
		RunE: forwardCommand.run,
		Args: usageExactOneArg(),
	}

	forwardCommand.cmd.Flags().StringVar(&forwardCommand.to, "to", "", "Recipient email address(es)")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.cc, "cc", "", "CC recipient email address(es)")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	forwardCommand.cmd.Flags().StringVarP(&forwardCommand.message, "message", "m", "", "Optional Markdown note above the forwarded message")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.messageHTML, "message-html", "", "The note as raw HTML instead of Markdown")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.messageHTMLFile, "message-html-file", "", "Read the raw HTML note from this file")
	forwardCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html", "message-html-file")

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

	note := c.messageHTML
	messageHTMLFileProvided := cmd.Flags().Changed("message-html-file")
	if messageHTMLFileProvided {
		note, err = readMessageHTMLFile(c.messageHTMLFile)
		if err != nil {
			return err
		}
	} else if note == "" {
		note = htmlutil.FromMarkdown(c.message)
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
	entryID := topic.Entries[len(topic.Entries)-1].Id

	draft, err := forwardSDK.Entries().NewForward(ctx, entryID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if draft == nil {
		return apierr.ErrNotFound("forward draft for thread", args[0])
	}

	content := htmlutil.PrependHTML(draft.Content, note)
	if err := forwardSDK.Messages().Create(ctx, draft.Subject, content, to, cc, bcc); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Message forwarded", map[string]any{
		"thread_id": threadID,
		"entry_id":  entryID,
		"subject":   draft.Subject,
		"to":        to,
		"cc":        cc,
		"bcc":       bcc,
	})
}
