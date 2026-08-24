package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type contactsBundleCommand struct {
	cmd *cobra.Command
}

type contactBundleResult struct {
	ID     int64  `json:"id"`
	Action string `json:"action"`
}

func newContactsBundleCommand() *contactsBundleCommand {
	bundleCommand := &contactsBundleCommand{}
	bundleCommand.cmd = &cobra.Command{
		Use:   "bundle <id>",
		Short: "Bundle a contact's mail",
		Long:  "Group mail from a contact into one bundle in HEY instead of listing every thread separately.",
		Annotations: map[string]string{
			"agent_notes": "Bundling is a per-contact preference. HEY applies it when the contact's delivery setting supports bundles. Reverse it with `hey contact unbundle <id>`.",
		},
		Example: `  hey contact bundle 12345`,
		RunE:    bundleCommand.run,
		Args:    usageExactOneArg(),
	}
	return bundleCommand
}

func (c *contactsBundleCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	if err := sdk.Contacts().Bundle(cmd.Context(), contactID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Bundle request accepted for contact %d.", contactID),
		"Bundle request accepted",
		contactBundleResult{ID: contactID, Action: "bundle"},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "unbundle", Command: fmt.Sprintf("hey contact unbundle %d", contactID), Description: "List this contact's mail separately"}),
	)
}
