package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type seenCommand struct {
	cmd *cobra.Command
}

func newSeenCommand() *seenCommand {
	seenCommand := &seenCommand{}
	seenCommand.cmd = &cobra.Command{
		Use:   "seen <id>...",
		Short: "Mark email threads as seen",
		Example: `  hey seen 12345
  hey seen 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Marks each email thread as seen/read.",
		},
		RunE: seenCommand.run,
		Args: usageMinOneArg(),
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
		Use:   "unseen <id>...",
		Short: "Mark email threads as unseen",
		Example: `  hey unseen 12345
  hey unseen 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Marks each email thread as unseen/unread.",
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

// parseIntArgs parses posting IDs, rejecting non-positive values and dropping
// duplicates. Zero and negatives are not valid posting IDs, so including one in
// the posting_ids payload asks the server to act on something the client already
// knows is invalid; rejecting locally gives a clearer message than whatever
// comes back. Duplicates are dropped, first occurrence wins — for the bulk
// seen/unseen calls that only trims the payload, but it matters more for any
// caller that issues one request per ID, where a repeat can come back as a
// failure.
func parseIntArgs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	seen := make(map[int64]bool, len(args))

	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil, apierr.ErrUsage(fmt.Sprintf("invalid ID: %s", arg))
		}
		if id <= 0 {
			return nil, output.ErrUsage(fmt.Sprintf("invalid posting ID: %d (must be positive)", id))
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	return ids, nil
}
