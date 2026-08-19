package cmd

import "github.com/spf13/cobra"

type contactNoteCommand struct {
	cmd *cobra.Command
}

func newContactNoteCommand() *contactNoteCommand {
	noteCommand := &contactNoteCommand{}
	noteCommand.cmd = &cobra.Command{
		Use:   "note",
		Short: "Manage private contact notes",
		Annotations: map[string]string{
			"agent_notes": "Private notes support show, set, and delete. Set accepts --note, positional content, stdin, or $EDITOR.",
		},
	}
	noteCommand.cmd.AddCommand(newContactNoteShowCommand().cmd)
	noteCommand.cmd.AddCommand(newContactNoteSetCommand().cmd)
	noteCommand.cmd.AddCommand(newContactNoteDeleteCommand().cmd)
	return noteCommand
}
