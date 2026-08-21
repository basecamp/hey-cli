package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type contactsUnbundleCommand struct {
	cmd *cobra.Command
}

func newContactsUnbundleCommand() *contactsUnbundleCommand {
	unbundleCommand := &contactsUnbundleCommand{}
	unbundleCommand.cmd = &cobra.Command{
		Use:   "unbundle <id>",
		Short: "List a contact's mail separately",
		Long:  "Stop grouping mail from a contact so each thread appears separately in HEY.",
		Annotations: map[string]string{
			"agent_notes": "Unbundling reverses `hey contacts bundle <id>` and preserves the underlying threads.",
		},
		Example: `  hey contacts unbundle 12345`,
		RunE:    unbundleCommand.run,
		Args:    usageExactOneArg(),
	}
	return unbundleCommand
}

func (c *contactsUnbundleCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	if err := sdk.Contacts().Unbundle(cmd.Context(), contactID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Unbundle request accepted for contact %d.", contactID),
		"Unbundle request accepted",
		contactBundleResult{ID: contactID, Action: "unbundle"},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "bundle", Command: fmt.Sprintf("hey contacts bundle %d", contactID), Description: "Bundle this contact's mail"}),
	)
}
