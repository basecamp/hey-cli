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
	from    string
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Annotations: map[string]string{
			"agent_notes": "Replies to the latest entry in a thread. Accepts message via -m, stdin, or $EDITOR.",
		},
		Example: `  hey reply 12345 -m "Thanks!"
  echo "Detailed reply" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message (or opens $EDITOR)")
	replyCommand.cmd.Flags().StringVar(&replyCommand.from, "from", "", "Sender email address (overrides default)")

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

	hasSenderOverride := c.from != "" || cfg.DefaultSender != ""
	if hasSenderOverride {
		senderID, err := effectiveSenderID(ctx, c.from)
		if err != nil {
			return err
		}
		body := map[string]any{
			"acting_sender_id": senderID,
			"message": map[string]any{
				"content": message,
			},
		}
		addrMap := map[string]any{}
		if len(addressed.To) > 0 {
			addrMap["directly"] = addressed.To
		}
		if len(addressed.CC) > 0 {
			addrMap["copied"] = addressed.CC
		}
		if len(addressed.BCC) > 0 {
			addrMap["blindcopied"] = addressed.BCC
		}
		if len(addrMap) > 0 {
			body["entry"] = map[string]any{
				"addressed": addrMap,
			}
		}
		if _, err := sdk.PostMutation(ctx, fmt.Sprintf("/entries/%d/replies.json", latestEntryID), body); err != nil {
			return convertSDKError(err)
		}
	} else {
		if err = sdk.Entries().CreateReply(ctx, latestEntryID, message, addressed.To, addressed.CC, addressed.BCC); err != nil {
			return convertSDKError(err)
		}
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
