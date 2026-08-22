package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type moveCommand struct {
	cmd *cobra.Command
	to  string
}

func newMoveCommand() *moveCommand {
	moveCommand := &moveCommand{}
	moveCommand.cmd = &cobra.Command{
		Use:   "move <id>...",
		Short: "Move email threads to another box",
		Long:  "Move one or more email threads to Imbox, The Feed, Set Aside, Reply Later, or Paper Trail.",
		Example: `  hey move 12345 --to feed
  hey move 12345 67890 --to "paper trail"
  hey move 12345 --to 987`,
		Annotations: map[string]string{
			"agent_notes": "Accepts box item IDs from hey box view output. --to accepts a box name, kind, or ID. Use HEY's scheduled Bubble Up flow for Bubble Up.",
		},
		RunE: moveCommand.run,
		Args: usageMinOneArg(),
	}

	moveCommand.cmd.Flags().StringVar(&moveCommand.to, "to", "", "Destination box name, kind, or ID (required)")

	return moveCommand
}

func (c *moveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.to) == "" {
		return apierr.ErrUsage("destination box is required (use --to <box>)")
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	destination, err := resolveMoveDestination(cmd.Context(), c.to)
	if err != nil {
		return err
	}

	if err := sdk.Postings().Move(cmd.Context(), destination.Id, ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s moved to %s", len(ids), threadNoun(len(ids)), terminal.SanitizeLine(destination.Name)), nil)
}

func resolveMoveDestination(ctx context.Context, nameOrID string) (*generated.Box, error) {
	boxes, err := sdk.Boxes().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if boxes == nil {
		return nil, apierr.ErrNotFound("box", nameOrID)
	}

	if id, err := strconv.ParseInt(nameOrID, 10, 64); err == nil {
		for i := range *boxes {
			if (*boxes)[i].Id == id {
				return validateMoveDestination(&(*boxes)[i])
			}
		}
		return nil, apierr.ErrNotFound("box", nameOrID)
	}

	query := canonicalBoxName(nameOrID)
	for i := range *boxes {
		box := &(*boxes)[i]
		if canonicalBoxName(box.Kind) == query || canonicalBoxName(box.Name) == query {
			return validateMoveDestination(box)
		}
	}

	return nil, apierr.ErrNotFound("box", nameOrID)
}

func validateMoveDestination(box *generated.Box) (*generated.Box, error) {
	if strings.EqualFold(box.Kind, hey.BoxKindBubbleUp) {
		return nil, apierr.ErrUsage("Bubble Up requires a scheduled date and is not supported by hey move")
	}
	return box, nil
}

func canonicalBoxName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(name)

	switch name {
	case "feed", "thefeed":
		return hey.BoxKindFeed
	case "aside", "setaside":
		return hey.BoxKindSetAside
	case "later", "replylater":
		return hey.BoxKindLater
	case "trail", "papertrail":
		return hey.BoxKindTrail
	case "bubble", "bubbleup", "bubbled", "bubbledup":
		return hey.BoxKindBubbleUp
	}
	return name
}
