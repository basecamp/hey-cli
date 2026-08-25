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
	on  string
}

func newBubbleUpCommand() *bubbleUpCommand {
	bubbleUpCommand := &bubbleUpCommand{}
	bubbleUpCommand.cmd = &cobra.Command{
		Use:   "up <box-item-id>... (--now | --on <date>)",
		Short: "Bubble email threads up",
		Long:  "Bubble one or more email threads up to the top of the Imbox, right away with --now or at HEY's morning hour of a date with --on.",
		Example: `  hey bubble up 12345 --now
  hey bubble up 12345 67890 --now
  hey bubble up 12345 --on 2026-09-04`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Exactly one of --now and --on is required. --on takes a YYYY-MM-DD date; HEY bubbles the threads up at its morning hour of that day.",
		},
		RunE: bubbleUpCommand.run,
		Args: usageMinOneArg(),
	}

	bubbleUpCommand.cmd.Flags().BoolVar(&bubbleUpCommand.now, "now", false, "Bubble the threads up right away")
	bubbleUpCommand.cmd.Flags().StringVar(&bubbleUpCommand.on, "on", "", "Bubble the threads up on a date (YYYY-MM-DD)")

	return bubbleUpCommand
}

func (c *bubbleUpCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.now && c.on != "" {
		return apierr.ErrUsage("--now and --on are mutually exclusive")
	}
	if !c.now && c.on == "" {
		return apierr.ErrUsage("either --now or --on <date> is required")
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if c.now {
		if err = sdk.Postings().BubbleUpNow(cmd.Context(), ids...); err != nil {
			return apierr.FromSDK(err)
		}
		return writeMutation(cmd, fmt.Sprintf("%d %s bubbled up", len(ids), threadNoun(len(ids))), nil)
	}

	on, err := parseDateArg("on date", c.on)
	if err != nil {
		return err
	}

	if err := sdk.Postings().ScheduleBubbleUp(cmd.Context(), on.Format(dateLayout), ids...); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s will bubble up on %s", len(ids), threadNoun(len(ids)), on.Format(dateLayout)), nil)
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
