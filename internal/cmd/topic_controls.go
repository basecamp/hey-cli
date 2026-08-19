package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type topicControlResult struct {
	TopicID int64  `json:"topic_id,omitempty"`
	EntryID int64  `json:"entry_id,omitempty"`
	Action  string `json:"action"`
}

func parsePositiveControlID(value, kind string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, output.ErrUsage(fmt.Sprintf("invalid %s ID: %s", kind, value))
	}
	return id, nil
}

func rejectControlListFormats() error {
	switch writer.EffectiveFormat() {
	case output.FormatIDs:
		return output.ErrUsage("--ids-only requires list data")
	case output.FormatCount:
		return output.ErrUsage("--count requires list data")
	default:
		return nil
	}
}

func writeTopicControlResult(cmd *cobra.Command, result topicControlResult, summary string) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}
	return writeOK(result, output.WithSummary(summary))
}
