package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
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
			return convertSDKError(getErr)
		}
		return nil
	})
	group.Go(func() error {
		var getErr error
		note, getErr = sdk.Contacts().Note(ctx, contactID)
		if getErr != nil {
			return convertSDKError(getErr)
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return err
	}
	if contact == nil {
		return output.ErrNotFound("contact", args[0])
	}

	result := contactShowResult{ContactDetail: *contact}
	if note != nil {
		result.Note = note.Note
		result.NoteHTML = note.NoteHtml
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

func printContactDetails(cmd *cobra.Command, result contactShowResult) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s\n", result.Name)
	fmt.Fprintf(w, "ID: %d\n", result.Id)
	fmt.Fprintf(w, "Email: %s\n", result.EmailAddress)
	if len(result.Aliases) > 0 {
		aliases := make([]string, 0, len(result.Aliases))
		for _, alias := range result.Aliases {
			aliases = append(aliases, alias.EmailAddress)
		}
		fmt.Fprintf(w, "Aliases: %s\n", strings.Join(aliases, ", "))
	}
	if result.Clearance.Status != "" {
		fmt.Fprintf(w, "Screening: %s\n", result.Clearance.Status)
	}
	fmt.Fprintln(w, "\nPrivate note:")
	if result.Note == "" {
		fmt.Fprintln(w, "(empty)")
	} else {
		fmt.Fprintln(w, result.Note)
	}
}
