package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type replyCommand struct {
	cmd         *cobra.Command
	message     string
	messageHTML string
	attachments []string
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Long: `Reply to a thread's latest entry.

The reply is addressed the way HEY's own web app addresses one: everyone that entry was
addressed to, with whoever wrote it on the To line. HEY saves an unaddressed reply as a
draft rather than sending it, so the command fails when it cannot work the recipients out.`,
		Annotations: map[string]string{
			"agent_notes": "Replies to the latest entry in a thread, addressed the way HEY addresses a reply: everyone that entry was addressed to, plus its sender on the To line. Accepts message via -m, stdin, or $EDITOR, plus repeatable --attach files; an attachment can be sent without body text. The message is Markdown; use --message-html to send raw HTML instead.",
		},
		Example: `  hey reply 12345 -m "Friday works for me — I'll send an agenda."
  hey reply 12345 -m "Attached is the report." --attach ./report.pdf
  echo "Longer reply from a file or a heredoc" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message as Markdown (or opens $EDITOR)")
	replyCommand.cmd.Flags().StringVar(&replyCommand.messageHTML, "message-html", "", "Reply message as raw HTML instead of Markdown")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.attachments, "attach", nil, "File to attach (repeatable)")
	replyCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")

	return replyCommand
}

func (c *replyCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	ctx := cmd.Context()

	target, err := resolveThreadReply(ctx, threadID)
	if err != nil {
		return err
	}
	replySDK := target.client

	message := c.messageHTML
	if message == "" {
		markdownMessage := c.message
		if markdownMessage == "" && !stdinIsTerminal() {
			markdownMessage, err = readStdin()
			if err != nil {
				return err
			}
			if markdownMessage == "" && len(c.attachments) == 0 {
				return apierr.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else if markdownMessage == "" && len(c.attachments) == 0 {
			markdownMessage, err = editor.Open("")
			if err != nil {
				return apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
			if markdownMessage == "" {
				return apierr.ErrUsage("empty message, aborting")
			}
		}
		message = htmlutil.FromMarkdown(markdownMessage)
	}

	message, err = attachFilesWithClient(ctx, replySDK, message, c.attachments)
	if err != nil {
		return err
	}
	if err = replySDK.Entries().CreateReply(ctx, target.EntryID, message, target.Addressed.To, target.Addressed.CC, target.Addressed.BCC); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, sentWithAttachmentsSummary("Reply sent", len(c.attachments)), nil,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey threads %d", threadID),
			Description: "View the full thread",
		}),
	)
}
