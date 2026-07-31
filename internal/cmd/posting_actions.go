package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type postingActionCommand struct {
	cmd       *cobra.Command
	pastTense string
	runSingle func(context.Context, int64) error
}

func newPostingActionCommand(name, short, pastTense, agentNotes string, runSingle func(context.Context, int64) error, aliases ...string) *postingActionCommand {
	c := &postingActionCommand{
		pastTense: pastTense,
		runSingle: runSingle,
	}
	c.cmd = &cobra.Command{
		Use:     name + " <posting-id>",
		Aliases: aliases,
		Short:   short,
		Example: fmt.Sprintf("  hey %s 12345", name),
		Annotations: map[string]string{
			"agent_notes": agentNotes,
		},
		RunE: c.run,
		Args: usageExactOneArg(),
	}

	return c
}

func (c *postingActionCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := c.runSingle(cmd.Context(), ids[0]); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("Posting %d %s", ids[0], c.pastTense)

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}

func newPaperTrailCommand() *postingActionCommand {
	return newPostingActionCommand(
		"paper-trail",
		"Move postings to Paper Trail",
		"moved to Paper Trail",
		"State-changing action. Confirm the exact posting IDs and obtain explicit user approval before moving each posting to Paper Trail.",
		func(ctx context.Context, id int64) error {
			return sdk.Postings().MoveToPaperTrail(ctx, id)
		},
		"papertrail",
		"trail",
	)
}

func newTrashCommand() *postingActionCommand {
	return newPostingActionCommand(
		"trash",
		"Move postings to Trash",
		"moved to Trash",
		"State-changing action. Confirm the exact posting IDs and obtain explicit user approval before moving each posting to Trash.",
		func(ctx context.Context, id int64) error {
			return sdk.Postings().MoveToTrash(ctx, id)
		},
	)
}

func newFeedCommand() *postingActionCommand {
	return newPostingActionCommand(
		"feed",
		"Move postings to The Feed",
		"moved to The Feed",
		"State-changing action. Confirm the exact posting IDs and obtain explicit user approval before moving each posting to The Feed.",
		func(ctx context.Context, id int64) error {
			return sdk.Postings().MoveToFeed(ctx, id)
		},
	)
}

func newSetAsideCommand() *postingActionCommand {
	return newPostingActionCommand(
		"set-aside",
		"Move postings to Set Aside",
		"moved to Set Aside",
		"State-changing action. Confirm the exact posting IDs and obtain explicit user approval before moving each posting to Set Aside.",
		func(ctx context.Context, id int64) error {
			return sdk.Postings().MoveToSetAside(ctx, id)
		},
		"aside",
	)
}

func newReplyLaterCommand() *postingActionCommand {
	return newPostingActionCommand(
		"reply-later",
		"Move postings to Reply Later",
		"moved to Reply Later",
		"State-changing action. Confirm the exact posting IDs and obtain explicit user approval before moving each posting to Reply Later.",
		func(ctx context.Context, id int64) error {
			return sdk.Postings().MoveToReplyLater(ctx, id)
		},
		"later",
	)
}

func newIgnoreCommand() *postingActionCommand {
	return newPostingActionCommand(
		"ignore",
		"Ignore postings",
		"ignored",
		"State-changing action. Confirm the exact posting IDs and obtain explicit user approval before ignoring each posting.",
		func(ctx context.Context, id int64) error {
			return sdk.Postings().Ignore(ctx, id)
		},
	)
}
