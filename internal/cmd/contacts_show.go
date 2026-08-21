package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type contactsShowCommand struct {
	cmd *cobra.Command
}

type contactShowResult struct {
	generated.ContactDetail
	Note     string `json:"note"`
	NoteHTML string `json:"note_html,omitempty"`
}

func newContactsShowCommand() *contactsShowCommand {
	showCommand := &contactsShowCommand{}
	showCommand.cmd = &cobra.Command{
		Use:   "show <id>",
		Short: "View a contact",
		Annotations: map[string]string{
			"agent_notes": "Returns contact details, aliases, screening status, and the private note.",
		},
		Example: `  hey contacts show 12345
  hey contacts show 12345 --json`,
		RunE: showCommand.run,
		Args: usageExactOneArg(),
	}
	return showCommand
}

func (c *contactsShowCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}

	var contact *generated.ContactDetail
	var note *generated.ContactNote
	group, ctx := errgroup.WithContext(cmd.Context())
	group.Go(func() error {
		var getErr error
		contact, getErr = sdk.Contacts().Get(ctx, contactID)
		if getErr != nil {
			return apierr.FromSDK(getErr)
		}
		return nil
	})
	group.Go(func() error {
		var getErr error
		note, getErr = sdk.Contacts().Note(ctx, contactID)
		if getErr != nil {
			return apierr.FromSDK(getErr)
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return err
	}
	if contact == nil {
		return apierr.ErrNotFound("contact", args[0])
	}

	result := contactShowResult{ContactDetail: *contact}
	if note != nil {
		result.Note = note.Note
		result.NoteHTML = note.NoteHtml
	}
	if writer.EffectiveFormat() == output.FormatHTML {
		return writeNoteHTML(cmd.OutOrStdout(), result.NoteHTML)
	}
	if writer.IsStyled() {
		printContactDetails(cmd, result)
		return nil
	}
	return writeOK(result,
		output.WithSummary(fmt.Sprintf("Contact %d", contactID)),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "edit", Command: fmt.Sprintf("hey contacts update %d", contactID), Description: "Edit this contact"},
			output.Breadcrumb{Action: "note", Command: fmt.Sprintf("hey contacts note set %d", contactID), Description: "Edit the private note"},
		),
	)
}

// writeNoteHTML is what --html writes for a contact: the note's original HTML, and
// nothing at all when there is no note.
func writeNoteHTML(w io.Writer, noteHTML string) error {
	if noteHTML == "" {
		return nil
	}
	_, err := fmt.Fprintln(w, noteHTML)
	return err
}

func printContactDetails(cmd *cobra.Command, result contactShowResult) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s\n", terminal.SanitizeLine(result.Name))
	fmt.Fprintf(w, "ID: %d\n", result.Id)
	fmt.Fprintf(w, "Email: %s\n", terminal.SanitizeLine(result.EmailAddress))
	if len(result.Aliases) > 0 {
		aliases := make([]string, 0, len(result.Aliases))
		for _, alias := range result.Aliases {
			aliases = append(aliases, terminal.SanitizeLine(alias.EmailAddress))
		}
		fmt.Fprintf(w, "Aliases: %s\n", strings.Join(aliases, ", "))
	}
	if result.Clearance.Status != "" {
		fmt.Fprintf(w, "Screening: %s\n", terminal.SanitizeLine(result.Clearance.Status))
	}
	fmt.Fprintln(w, "\nPrivate note:")
	fmt.Fprintln(w, renderedNote(result.Note, result.NoteHTML))
}

// renderedNote is a contact note for a terminal: the rich note rendered from its HTML
// when HEY served one, the plain note otherwise, and never the HTML itself.
func renderedNote(note, noteHTML string) string {
	switch {
	case noteHTML != "":
		return markdown.Render(htmlutil.ToMarkdown(noteHTML), stdoutWidth())
	case note != "":
		return terminal.Sanitize(note)
	default:
		return "(empty)"
	}
}
