package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type seenCommand struct {
	cmd *cobra.Command
}

func newSeenCommand() *seenCommand {
	seenCommand := &seenCommand{}
	seenCommand.cmd = &cobra.Command{
		Use:   "seen <posting-id>...",
		Short: "Mark postings as seen",
		Example: `  hey seen 12345
  hey seen 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more posting IDs. Marks each as seen/read.",
		},
		RunE: seenCommand.run,
		Args: cobra.MinimumNArgs(1),
	}

	return seenCommand
}

func (c *seenCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if _, err := apiClient.MarkSeen(ids); err != nil {
		return err
	}

	summary := fmt.Sprintf("%d posting(s) marked as seen", len(ids))

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}

// unseen

type unseenCommand struct {
	cmd *cobra.Command
}

func newUnseenCommand() *unseenCommand {
	unseenCommand := &unseenCommand{}
	unseenCommand.cmd = &cobra.Command{
		Use:   "unseen <posting-id>...",
		Short: "Mark postings as unseen",
		Example: `  hey unseen 12345
  hey unseen 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more posting IDs. Marks each as unseen/unread.",
		},
		RunE: unseenCommand.run,
		Args: cobra.MinimumNArgs(1),
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

	if _, err := apiClient.MarkUnseen(ids); err != nil {
		return err
	}

	summary := fmt.Sprintf("%d posting(s) marked as unseen", len(ids))

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}

func parseIntArgs(args []string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, arg := range args {
		id, err := strconv.Atoi(arg)
		if err != nil {
			return nil, output.ErrUsage(fmt.Sprintf("invalid posting ID: %s", arg))
		}
		ids = append(ids, id)
	}
	return ids, nil
}
