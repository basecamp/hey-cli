package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
)

type replyCommand struct {
	cmd         *cobra.Command
	message     string
	attachments []string
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Annotations: map[string]string{
			"agent_notes": "Replies to the latest entry in a thread. Accepts message via -m, stdin, or $EDITOR, plus repeatable --attach files; an attachment can be sent without body text.",
		},
		Example: `  hey reply 12345 -m "Thanks!"
  hey reply 12345 -m "Attached is the report." --attach ./report.pdf
  echo "Detailed reply" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message (or opens $EDITOR)")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.attachments, "attach", nil, "File to attach (repeatable)")

	return replyCommand
}

func (c *replyCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	ctx := cmd.Context()

	target, err := resolveThreadReply(ctx, threadID)
	if err != nil {
		return err
	}
	replySDK, err := clientForResourceAccount(ctx, target.AccountID)
	if err != nil {
		return err
	}

	message := c.message
	if message == "" && !stdinIsTerminal() {
		message, err = readStdin()
		if err != nil {
			return err
		}
		if message == "" && len(c.attachments) == 0 {
			return output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
		}
	} else if message == "" && len(c.attachments) == 0 {
		message, err = editor.Open("")
		if err != nil {
			return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
		}
		if message == "" {
			return output.ErrUsage("empty message, aborting")
		}
	}

	message, err = attachFilesWithClient(ctx, replySDK, message, c.attachments)
	if err != nil {
		return err
	}
	if err = replySDK.Entries().CreateReply(ctx, target.EntryID, message, target.Addressed.To, target.Addressed.CC, target.Addressed.BCC); err != nil {
		return convertSDKError(err)
	}

	summary := sentWithAttachmentsSummary("Reply sent", len(c.attachments))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil,
		output.WithSummary(summary),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey threads %d", threadID),
			Description: "View the full thread",
		}),
	)
}
