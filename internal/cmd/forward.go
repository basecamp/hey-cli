package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type forwardCommand struct {
	cmd     *cobra.Command
	to      string
	cc      string
	bcc     string
	message string
}

func newForwardCommand() *forwardCommand {
	forwardCommand := &forwardCommand{}
	forwardCommand.cmd = &cobra.Command{
		Use:   "forward <thread-id>",
		Short: "Forward the latest message in a thread",
		Annotations: map[string]string{
			"agent_notes": "Forwards the latest entry in a thread with HEY's quoted content. Accepts comma-separated recipients and an optional note via -m.",
		},
		Example: `  hey forward 12345 --to alice@example.com
  hey forward 12345 --to alice@example.com --cc bob@example.org -m "For your review"`,
		RunE: forwardCommand.run,
		Args: usageExactOneArg(),
	}

	forwardCommand.cmd.Flags().StringVar(&forwardCommand.to, "to", "", "Recipient email address(es)")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.cc, "cc", "", "CC recipient email address(es)")
	forwardCommand.cmd.Flags().StringVar(&forwardCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	forwardCommand.cmd.Flags().StringVarP(&forwardCommand.message, "message", "m", "", "Optional note above the forwarded message")

	return forwardCommand
}

func (c *forwardCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	to := parseAddresses(c.to)
	cc := parseAddresses(c.cc)
	bcc := parseAddresses(c.bcc)
	if len(to)+len(cc)+len(bcc) == 0 {
		return output.ErrUsageHint("at least one recipient is required", "hey forward <thread-id> --to <email>")
	}

	ctx := cmd.Context()
	topic, err := rootSDK.Topics().Get(ctx, threadID)
	if err != nil {
		return convertSDKError(err)
	}
	if topic == nil || len(topic.Entries) == 0 {
		return output.ErrNotFound("entries for thread", args[0])
	}
	forwardSDK, err := clientForResourceAccount(ctx, topic.AccountId)
	if err != nil {
		return err
	}
	entryID := topic.Entries[len(topic.Entries)-1].Id

	draft, err := forwardSDK.Entries().NewForward(ctx, entryID)
	if err != nil {
		return convertSDKError(err)
	}
	if draft == nil {
		return output.ErrNotFound("forward draft for thread", args[0])
	}

	content := htmlutil.PrependText(draft.Content, c.message)
	if err := forwardSDK.Messages().Create(ctx, draft.Subject, content, to, cc, bcc); err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Message forwarded.")
		return nil
	}

	return writeOK(map[string]any{
		"thread_id": threadID,
		"entry_id":  entryID,
		"subject":   draft.Subject,
		"to":        to,
		"cc":        cc,
		"bcc":       bcc,
	}, output.WithSummary("Message forwarded"))
}
