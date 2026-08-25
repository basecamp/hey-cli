package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type bubbleCommand struct {
	cmd *cobra.Command
}

func newBubbleCommand() *bubbleCommand {
	bubbleCommand := &bubbleCommand{}
	bubbleCommand.cmd = &cobra.Command{
		Use:   "bubble",
		Short: "Bubble email threads up in the Imbox",
		Long:  "Bubble email threads up to the top of the Imbox, or cancel a bubble-up.",
	}

	bubbleCommand.cmd.AddCommand(newBubbleUpCommand().cmd)
	bubbleCommand.cmd.AddCommand(newBubblePopCommand().cmd)

	return bubbleCommand
}

type bubbleUpCommand struct {
	cmd *cobra.Command
	now bool
}

func newBubbleUpCommand() *bubbleUpCommand {
	bubbleUpCommand := &bubbleUpCommand{}
	bubbleUpCommand.cmd = &cobra.Command{
		Use:   "up <box-item-id>... --now",
		Short: "Bubble email threads up now",
		Long:  "Bubble one or more email threads up to the top of the Imbox right away.",
		Example: `  hey bubble up 12345 --now
  hey bubble up 12345 67890 --now`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. --now is required; scheduled bubble-up is not supported yet.",
		},
		RunE: bubbleUpCommand.run,
		Args: usageMinOneArg(),
	}

	bubbleUpCommand.cmd.Flags().BoolVar(&bubbleUpCommand.now, "now", false, "Bubble the threads up right away (required)")

	return bubbleUpCommand
}

func (c *bubbleUpCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if !c.now {
		return apierr.ErrUsage("scheduled bubble-up is not supported yet (use --now)")
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().BubbleUpNow(cmd.Context(), ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s bubbled up", len(ids), threadNoun(len(ids))), nil)
}

type bubblePopCommand struct {
	cmd *cobra.Command
}

func newBubblePopCommand() *bubblePopCommand {
	bubblePopCommand := &bubblePopCommand{}
	bubblePopCommand.cmd = &cobra.Command{
		Use:   "pop <box-item-id>...",
		Short: "Cancel bubble-ups",
		Long:  "Cancel the bubble-up on one or more email threads.",
		Example: `  hey bubble pop 12345
  hey bubble pop 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Cancels each thread's bubble-up.",
		},
		RunE: bubblePopCommand.run,
		Args: usageMinOneArg(),
	}

	return bubblePopCommand
}

func (c *bubblePopCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().CancelBubbleUp(cmd.Context(), ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s no longer bubbled up", len(ids), threadNoun(len(ids))), nil)
}
