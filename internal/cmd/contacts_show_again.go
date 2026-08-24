package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type contactsShowAgainCommand struct {
	cmd *cobra.Command
}

func newContactsShowAgainCommand() *contactsShowAgainCommand {
	showAgainCommand := &contactsShowAgainCommand{}
	showAgainCommand.cmd = &cobra.Command{
		Use:   "show-again <id>",
		Short: "Show a hidden contact again",
		Annotations: map[string]string{
			"agent_notes": "Reverses `hey contact hide <id>`.",
		},
		Example: `  hey contact show-again 12345`,
		RunE:    showAgainCommand.run,
		Args:    usageExactOneArg(),
	}
	return showAgainCommand
}

func (c *contactsShowAgainCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	contact, err := sdk.Contacts().Reveal(cmd.Context(), contactID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if contact == nil {
		return apierr.ErrNotFound("contact", args[0])
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Contact shown again: %s <%s> (#%d)", terminal.SanitizeLine(contact.Name), terminal.SanitizeLine(contact.EmailAddress), contact.Id),
		"Contact shown again",
		contact,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "view", Command: fmt.Sprintf("hey contact show %d", contact.Id), Description: "View the contact"}),
	)
}
