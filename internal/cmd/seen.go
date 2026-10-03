package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type seenCommand struct {
	cmd *cobra.Command
	box string
}

func newSeenCommand() *seenCommand {
	seenCommand := &seenCommand{}
	seenCommand.cmd = &cobra.Command{
		Use:   "seen (<box-item-id>... | --box <name|id>)",
		Short: "Mark email threads as seen",
		Long: "Mark email threads as seen by box item ID, or queue marking a whole box as seen. " +
			"--box resolves a name or ID the same way as hey box view and cannot be combined with box item IDs. " +
			"Whole-box work is queued; threads may still appear unseen immediately afterward.",
		Example: `  hey seen 12345
  hey seen 12345 67890
  hey seen --box "paper trail"
  hey seen --box 987`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output, or --box <name|id> to mark a whole box as seen. The two target modes are mutually exclusive. --box resolves like hey box view and uses the current account selection. Whole-box work is queued, so success acknowledges the request rather than completed changes.",
		},
		RunE: seenCommand.run,
		Args: seenCommand.validateArgs,
	}
	seenCommand.cmd.Flags().StringVar(&seenCommand.box, "box", "", "Queue marking a whole box as seen (name or ID, as in hey box view)")

	return seenCommand
}

func (c *seenCommand) validateArgs(cmd *cobra.Command, args []string) error {
	if !cmd.Flags().Changed("box") {
		return usageMinOneArg()(cmd, args)
	}
	if len(args) > 0 {
		return apierr.ErrUsage("--box cannot be combined with box item IDs")
	}
	if strings.TrimSpace(c.box) == "" {
		return apierr.ErrUsage("--box requires a box name or ID")
	}
	return nil
}

func (c *seenCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if c.box != "" {
		box, err := resolveBox(cmd.Context(), c.box, "")
		if err != nil {
			return err
		}
		if box == nil || box.Id <= 0 {
			return apierr.ErrAPI(0, "HEY returned no valid box ID")
		}
		if err := sdk.Boxes().MarkSeen(cmd.Context(), box.Id); err != nil {
			return apierr.FromSDK(err)
		}
		return writeMutation(cmd, fmt.Sprintf("Queued marking %s as seen", terminal.SanitizeLine(box.Name)), nil)
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().MarkSeen(cmd.Context(), ids); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s marked as seen", len(ids), threadNoun(len(ids))), nil)
}

// unseen

type unseenCommand struct {
	cmd *cobra.Command
}

func newUnseenCommand() *unseenCommand {
	unseenCommand := &unseenCommand{}
	unseenCommand.cmd = &cobra.Command{
		Use:   "unseen <box-item-id>...",
		Short: "Mark email threads as unseen",
		Example: `  hey unseen 12345
  hey unseen 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Marks each email thread as unseen/unread.",
		},
		RunE: unseenCommand.run,
		Args: usageMinOneArg(),
	}

	return unseenCommand
}

func (c *unseenCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().MarkUnseen(cmd.Context(), ids); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s marked as unseen", len(ids), threadNoun(len(ids))), nil)
}

func parseIntArgs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	seen := make(map[int64]struct{}, len(args))
	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || id <= 0 {
			return nil, apierr.ErrUsage(fmt.Sprintf("invalid ID: %s", arg))
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}
