package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
)

type contactNoteSetCommand struct {
	cmd  *cobra.Command
	note string
}

func newContactNoteSetCommand() *contactNoteSetCommand {
	setCommand := &contactNoteSetCommand{}
	setCommand.cmd = &cobra.Command{
		Use:   "set <id> [note]",
		Short: "Write or edit a private contact note",
		Annotations: map[string]string{
			"agent_notes": "Accepts --note, positional content, stdin, or opens $EDITOR with the existing note. Use the delete subcommand to clear a note.",
		},
		Example: `  hey contacts note set 12345 "Prefers email"
  hey contacts note set 12345 --note "Prefers email"
  echo "Prefers email" | hey contacts note set 12345`,
		RunE: setCommand.run,
		Args: cobra.MatchAll(usageMinOneArg(), cobra.MaximumNArgs(2)),
	}
	setCommand.cmd.Flags().StringVarP(&setCommand.note, "note", "n", "", "Private note content (or opens $EDITOR)")
	return setCommand
}

func (c *contactNoteSetCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}

	content := c.note
	if len(args) == 2 {
		if cmd.Flags().Changed("note") {
			return output.ErrUsage("--note and positional note are mutually exclusive")
		}
		content = args[1]
	}
	if content == "" {
		if !stdinIsTerminal() {
			content, err = readStdin()
			if err != nil {
				return err
			}
		} else {
			existing, getErr := contactNoteForEditor(cmd.Context(), contactID, sdk.Contacts().Note)
			if getErr != nil {
				return convertSDKError(getErr)
			}
			content, err = editor.Open(existing)
			if err != nil {
				return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return output.ErrUsage("note cannot be empty; use `hey contacts note delete <id>` to clear it")
	}

	note, err := sdk.Contacts().SetNote(cmd.Context(), contactID, content)
	if err != nil {
		return convertContactWriteError(err)
	}
	if note == nil {
		return output.ErrAPI(0, "contact note save returned no data")
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Private note for contact %d saved.\n", contactID)
		return nil
	}
	return writeOK(note,
		output.WithSummary("Private contact note saved"),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "read", Command: fmt.Sprintf("hey contacts note show %d", contactID), Description: "Read the private note"}),
	)
}

type contactNoteFetcher func(context.Context, int64) (*generated.ContactNote, error)

func contactNoteForEditor(ctx context.Context, contactID int64, fetch contactNoteFetcher) (string, error) {
	note, err := fetch(ctx, contactID)
	if err != nil {
		return "", err
	}
	if note == nil {
		return "", nil
	}
	return note.Note, nil
}
