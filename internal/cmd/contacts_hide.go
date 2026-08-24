package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type contactsHideCommand struct {
	cmd *cobra.Command
}

func newContactsHideCommand() *contactsHideCommand {
	hideCommand := &contactsHideCommand{}
	hideCommand.cmd = &cobra.Command{
		Use:   "hide <id>",
		Short: "Hide a contact",
		Long:  "Hide a contact from contact lists, autocomplete, and search results. The contact remains available by ID and can be shown again.",
		Annotations: map[string]string{
			"agent_notes": "This is reversible. Use `hey contact show-again <id>` to show the contact again.",
		},
		Example: `  hey contact hide 12345`,
		RunE:    hideCommand.run,
		Args:    usageExactOneArg(),
	}
	return hideCommand
}

func (c *contactsHideCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	if err := sdk.Contacts().Hide(cmd.Context(), contactID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Contact %d hidden.", contactID),
		"Contact hidden",
		map[string]int64{"id": contactID},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "show_again", Command: fmt.Sprintf("hey contact show-again %d", contactID), Description: "Show the contact again"}),
	)
}
