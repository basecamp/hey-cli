package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type replyCommand struct {
	cmd     *cobra.Command
	message string
	draft   bool
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Annotations: map[string]string{
			"agent_notes": "Replies to the latest entry in a thread. Accepts message via -m, stdin, or $EDITOR. Use --draft to save without sending.",
		},
		Example: `  hey reply 12345 -m "Thanks!"
  hey reply 12345 --draft -m "Draft reply"
  echo "Detailed reply" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message (or opens $EDITOR)")
	replyCommand.cmd.Flags().BoolVar(&replyCommand.draft, "draft", false, "Save as draft instead of sending")

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

	// Fetch topic page to extract recipients (To/CC/BCC).
	topicResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d", threadID))
	if err != nil {
		return convertSDKError(err)
	}
	addressed := htmlutil.ParseTopicAddressed(string(topicResp.Data))
	if len(addressed.To) == 0 && len(addressed.CC) == 0 && len(addressed.BCC) == 0 {
		return output.ErrUsage("could not determine thread recipients")
	}

	// Fetch entries to find the latest entry ID for the reply.
	entriesResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d/entries", threadID))
	if err != nil {
		return convertSDKError(err)
	}
	entries := htmlutil.ParseTopicEntriesHTML(string(entriesResp.Data))
	if len(entries) == 0 {
		return output.ErrNotFound("entries for thread", args[0])
	}

	latestEntryID := entries[len(entries)-1].ID

	message := c.message
	if message == "" {
		if !stdinIsTerminal() {
			message, err = readStdin()
			if err != nil {
				return err
			}
			if message == "" {
				return output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else {
			message, err = editor.Open("")
			if err != nil {
				return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
			if message == "" {
				return output.ErrUsage("empty message, aborting")
			}
		}
	}

	if c.draft {
		return createReplyDraft(ctx, cmd.OutOrStdout(), threadID, draftFormRequest{
			Content: message,
			To:      addressed.To,
			CC:      addressed.CC,
			BCC:     addressed.BCC,
		})
	}

	if err = sdk.Entries().CreateReply(ctx, latestEntryID, message, addressed.To, addressed.CC, addressed.BCC); err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Reply sent.")
		return nil
	}

	return writeOK(nil,
		output.WithSummary("Reply sent"),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey threads %d", threadID),
			Description: "View the full thread",
		}),
	)
}
