package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
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
			"agent_notes": "Reverses `hey contacts hide <id>`.",
		},
		Example: `  hey contacts show-again 12345`,
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
		return convertSDKError(err)
	}
	if contact == nil {
		return output.ErrNotFound("contact", args[0])
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Contact shown again: %s <%s> (#%d)\n", contact.Name, contact.EmailAddress, contact.Id)
		return nil
	}
	return writeOK(contact,
		output.WithSummary("Contact shown again"),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "view", Command: fmt.Sprintf("hey contacts show %d", contact.Id), Description: "View the contact"}),
	)
}
