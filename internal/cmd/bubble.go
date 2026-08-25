package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

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

	bubbleCommand.cmd.AddCommand(newBubbleListCommand().cmd)
	bubbleCommand.cmd.AddCommand(newBubbleUpCommand().cmd)
	bubbleCommand.cmd.AddCommand(newBubblePopCommand().cmd)

	return bubbleCommand
}

type bubbleUpCommand struct {
	cmd      *cobra.Command
	now      bool
	on       string
	tomorrow bool
	weekend  bool
	nextWeek bool
}

func newBubbleUpCommand() *bubbleUpCommand {
	bubbleUpCommand := &bubbleUpCommand{}
	bubbleUpCommand.cmd = &cobra.Command{
		Use:   "up <box-item-id>... (--now | --on <date> | --tomorrow | --weekend | --next-week)",
		Short: "Bubble email threads up",
		Long:  "Bubble one or more email threads up to the top of the Imbox: right away with --now, at HEY's morning hour of a date with --on, or at its morning hour of tomorrow, Saturday, or next Monday with the named flags. Today's date under --on schedules HEY's Later today slot instead — its evening hour (18:00) — since this morning has already passed.",
		Example: `  hey bubble up 12345 --now
  hey bubble up 12345 67890 --now
  hey bubble up 12345 --on 2026-09-04
  hey bubble up 12345 --weekend`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Exactly one of --now, --on, --tomorrow, --weekend and --next-week is required. --on takes a YYYY-MM-DD date; HEY bubbles the threads up at its morning hour of that day, or at its evening hour (18:00) when the date is today. --tomorrow, --weekend and --next-week land at the morning hour of tomorrow, the coming Saturday, and next Monday.",
		},
		RunE: bubbleUpCommand.run,
		Args: usageMinOneArg(),
	}

	bubbleUpCommand.cmd.Flags().BoolVar(&bubbleUpCommand.now, "now", false, "Bubble the threads up right away")
	bubbleUpCommand.cmd.Flags().StringVar(&bubbleUpCommand.on, "on", "", "Bubble the threads up on a date (YYYY-MM-DD)")
	bubbleUpCommand.cmd.Flags().BoolVar(&bubbleUpCommand.tomorrow, "tomorrow", false, "Bubble the threads up tomorrow morning")
	bubbleUpCommand.cmd.Flags().BoolVar(&bubbleUpCommand.weekend, "weekend", false, "Bubble the threads up Saturday morning")
	bubbleUpCommand.cmd.Flags().BoolVar(&bubbleUpCommand.nextWeek, "next-week", false, "Bubble the threads up Monday morning")

	return bubbleUpCommand
}

func (c *bubbleUpCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if err := c.requireOneSchedule(); err != nil {
		return err
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

	switch {
	case c.tomorrow:
		return c.scheduleFor(cmd, hey.BubbleUpTomorrow, "tomorrow morning", ids)
	case c.weekend:
		return c.scheduleFor(cmd, hey.BubbleUpThisWeekend, "Saturday morning", ids)
	case c.nextWeek:
		return c.scheduleFor(cmd, hey.BubbleUpNextWeek, "Monday morning", ids)
	}

	on, err := parseDateArg("on date", c.on)
	if err != nil {
		return err
	}

	if on.Format(dateLayout) == time.Now().Format(dateLayout) {
		return c.scheduleFor(cmd, hey.BubbleUpLaterToday, "this evening", ids)
	}

	if err = sdk.Postings().ScheduleBubbleUp(cmd.Context(), on.Format(dateLayout), ids...); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s will bubble up on %s", len(ids), threadNoun(len(ids)), on.Format(dateLayout)), nil)
}

func (c *bubbleUpCommand) requireOneSchedule() error {
	chosen := 0
	for _, picked := range []bool{c.now, c.on != "", c.tomorrow, c.weekend, c.nextWeek} {
		if picked {
			chosen++
		}
	}

	if chosen > 1 {
		return apierr.ErrUsage("--now, --on, --tomorrow, --weekend and --next-week are mutually exclusive")
	}
	if chosen == 0 {
		return apierr.ErrUsage("one of --now, --on <date>, --tomorrow, --weekend or --next-week is required")
	}
	return nil
}

func (c *bubbleUpCommand) scheduleFor(cmd *cobra.Command, slot hey.BubbleUpSlot, when string, ids []int64) error {
	if err := sdk.Postings().ScheduleBubbleUpFor(cmd.Context(), slot, ids...); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s will bubble up %s", len(ids), threadNoun(len(ids)), when), nil)
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
