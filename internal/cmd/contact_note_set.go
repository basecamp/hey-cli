package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
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

	content, inputProvided, err := contactNoteInput(cmd.Flags().Changed("note"), c.note, args)
	if err != nil {
		return err
	}
	if !inputProvided {
		if !stdinIsTerminal() {
			content, err = readStdin()
			if err != nil {
				return err
			}
		} else {
			existing, getErr := contactNoteForEditor(cmd.Context(), contactID, sdk.Contacts().Note)
			if getErr != nil {
				return apierr.FromSDK(getErr)
			}
			content, err = editor.Open(existing)
			if err != nil {
				return apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return apierr.ErrUsage("note cannot be empty; use `hey contacts note delete <id>` to clear it")
	}

	note, err := sdk.Contacts().SetNote(cmd.Context(), contactID, content)
	if err != nil {
		return convertContactWriteError(err)
	}
	if note == nil {
		return apierr.ErrAPI(0, "contact note save returned no data")
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Private note for contact %d saved.", contactID),
		"Private contact note saved",
		note,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "read", Command: fmt.Sprintf("hey contacts note show %d", contactID), Description: "Read the private note"}),
	)
}

func contactNoteInput(flagChanged bool, flagValue string, args []string) (string, bool, error) {
	if len(args) == 2 {
		if flagChanged {
			return "", false, apierr.ErrUsage("--note and positional note are mutually exclusive")
		}
		return args[1], true, nil
	}
	if flagChanged {
		return flagValue, true, nil
	}
	return "", false, nil
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
