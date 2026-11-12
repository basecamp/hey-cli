package cmd

import (
	"github.com/spf13/cobra"
)

type localWriteCommand struct {
	cmd     *cobra.Command
	to      string
	subject string
	message string
}

func newLocalWriteCommand() *localWriteCommand {
	writeCommand := &localWriteCommand{}
	writeCommand.cmd = &cobra.Command{
		Use:   "write",
		Short: "Write a new message as a draft in the HEY app",
		Long: `Write a new message as a draft in the HEY app on this computer. Nothing is sent:
hey local send asks HEY to send it, and it waits for you to press Send in HEY.

--to reads as HEY's To line does: addresses, or Name <address>, separated by
commas. The message is plain text and goes above your name tag. Cc, Bcc, files
and a schedule are yours to add in HEY. Each run writes one more draft; delete
one you don't want in HEY.`,
		Annotations: map[string]string{
			"agent_notes": "Runs the app's draft.write, which saves a draft and sends nothing. It answers the draft, whose id hey local send takes; when sendable is false (to nobody, say) HEY would not send it from here. Not idempotent: each run writes one more draft, and nothing here edits or deletes one, so write again rather than retrying a run that answered, and leave the wrong one for the person to delete in HEY. The message is plain text, read from -m or stdin.",
		},
		Example: `  hey local write --to "Marcus <marcus@example.com>" --subject "Stint plan" -m "Saturday works"
  echo "Saturday works" | hey local write --to marcus@example.com --subject "Stint plan" --json`,
		RunE: writeCommand.run,
		Args: cobra.NoArgs,
	}
	writeCommand.cmd.Flags().StringVar(&writeCommand.to, "to", "", "Recipients, as HEY's To line reads them")
	writeCommand.cmd.Flags().StringVar(&writeCommand.subject, "subject", "", "Subject")
	writeCommand.cmd.Flags().StringVarP(&writeCommand.message, "message", "m", "", "Message as plain text (or pipe it to stdin)")
	return writeCommand
}

func (c *localWriteCommand) run(cmd *cobra.Command, _ []string) error {
	body, err := localMessageBody(c.message)
	if err != nil {
		return err
	}
	result, err := callLocal(cmd, "draft.write", map[string]any{"to": c.to, "subject": c.subject, "body": body})
	if err != nil {
		return err
	}
	return writeLocalDraft(cmd, "draft.write", result, 0)
}
