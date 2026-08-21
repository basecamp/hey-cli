package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// writeMutation confirms a completed write. Somebody reading along gets one line
// and nothing else; everybody else gets the envelope, with the same words as its
// summary.
func writeMutation(cmd *cobra.Command, summary string, data any, opts ...output.ResponseOption) error {
	return writeMutationLine(cmd, summary+".", summary, data, opts...)
}

// writeMutationLine is writeMutation where the line carries something the summary
// does not — the id the API assigned, the name that was typed, the bytes written.
func writeMutationLine(cmd *cobra.Command, line, summary string, data any, opts ...output.ResponseOption) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), terminal.SanitizeLine(line))
		return nil
	}
	return writeOK(data, append(opts, output.WithSummary(summary))...)
}
