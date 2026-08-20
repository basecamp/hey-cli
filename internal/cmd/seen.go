package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type seenCommand struct {
	cmd  *cobra.Command
	kind string
}

func newSeenCommand() *seenCommand {
	seenCommand := &seenCommand{}
	seenCommand.cmd = &cobra.Command{
		Use:   "seen <id>...",
		Short: "Mark email threads as seen",
		Example: `  hey seen 12345 --kind topic
  hey seen 12345 67890 --kind topic`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Pass --kind exactly as returned by hey box --json. HEY World posts are rejected before any email action is requested. Marks each email thread as seen/read.",
		},
		RunE: seenCommand.run,
		Args: usageMinOneArg(),
	}
	seenCommand.cmd.Flags().StringVar(&seenCommand.kind, "kind", "", "Item kind from hey box --json")

	return seenCommand
}

func (c *seenCommand) run(cmd *cobra.Command, args []string) error {
	if err := validateEmailPostingKind(c.cmd.Name(), c.kind); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().MarkSeen(cmd.Context(), ids); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("%d %s marked as seen", len(ids), threadNoun(len(ids)))

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}

// unseen

type unseenCommand struct {
	cmd  *cobra.Command
	kind string
}

func newUnseenCommand() *unseenCommand {
	unseenCommand := &unseenCommand{}
	unseenCommand.cmd = &cobra.Command{
		Use:   "unseen <id>...",
		Short: "Mark email threads as unseen",
		Example: `  hey unseen 12345 --kind topic
  hey unseen 12345 67890 --kind topic`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Pass --kind exactly as returned by hey box --json. HEY World posts are rejected before any email action is requested. Marks each email thread as unseen/unread.",
		},
		RunE: unseenCommand.run,
		Args: usageMinOneArg(),
	}
	unseenCommand.cmd.Flags().StringVar(&unseenCommand.kind, "kind", "", "Item kind from hey box --json")

	return unseenCommand
}

func (c *unseenCommand) run(cmd *cobra.Command, args []string) error {
	if err := validateEmailPostingKind(c.cmd.Name(), c.kind); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().MarkUnseen(cmd.Context(), ids); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("%d %s marked as unseen", len(ids), threadNoun(len(ids)))

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}

func parseIntArgs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil, output.ErrUsage(fmt.Sprintf("invalid ID: %s", arg))
		}
		ids = append(ids, id)
	}
	return ids, nil
}
