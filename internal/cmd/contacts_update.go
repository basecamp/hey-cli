package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type contactsUpdateCommand struct {
	cmd     *cobra.Command
	name    string
	email   string
	aliases []string
}

func newContactsUpdateCommand() *contactsUpdateCommand {
	updateCommand := &contactsUpdateCommand{}
	updateCommand.cmd = &cobra.Command{
		Use:   "update <id>",
		Short: "Edit a contact",
		Long:  "Edit a contact. Omitted name and email fields stay unchanged. Supplying --alias replaces the complete alias list; use --alias= to clear it.",
		Annotations: map[string]string{
			"agent_notes": "At least one field is required. Repeat --alias to replace aliases. --alias= clears every alias.",
		},
		Example: `  hey contacts update 12345 --name "Jane Doe"
  hey contacts update 12345 --email jane@example.com --alias jane.doe@example.org
  hey contacts update 12345 --alias=`,
		RunE: updateCommand.run,
		Args: usageExactOneArg(),
	}
	flags := updateCommand.cmd.Flags()
	flags.StringVar(&updateCommand.name, "name", "", "New contact name")
	flags.StringVar(&updateCommand.email, "email", "", "New primary email address")
	flags.StringSliceVar(&updateCommand.aliases, "alias", nil, "Replacement alternate email address (repeatable)")
	return updateCommand
}

func (c *contactsUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	nameChanged := cmd.Flags().Changed("name")
	emailChanged := cmd.Flags().Changed("email")
	aliasesChanged := cmd.Flags().Changed("alias")
	if !nameChanged && !emailChanged && !aliasesChanged {
		return output.ErrUsage("provide at least one of --name, --email, or --alias")
	}

	params := hey.ContactParams{}
	if nameChanged {
		params.Name = strings.TrimSpace(c.name)
		if params.Name == "" {
			return output.ErrUsage("--name cannot be empty")
		}
	}
	if emailChanged {
		params.EmailAddress = strings.TrimSpace(c.email)
		if params.EmailAddress == "" {
			return output.ErrUsage("--email cannot be empty")
		}
	}
	if aliasesChanged {
		params.AliasEmailAddresses = cleanContactAliases(c.aliases)
	}
	if emailChanged || aliasesChanged {
		validationEmail := params.EmailAddress
		validationAliases := params.AliasEmailAddresses
		if validationEmail == "" || validationAliases == nil {
			current, getErr := sdk.Contacts().Get(cmd.Context(), contactID)
			if getErr != nil {
				return convertSDKError(getErr)
			}
			if current == nil {
				return output.ErrNotFound("contact", args[0])
			}
			if validationEmail == "" {
				validationEmail = current.EmailAddress
			}
			if validationAliases == nil {
				validationAliases = make([]string, 0, len(current.Aliases))
				for _, alias := range current.Aliases {
					validationAliases = append(validationAliases, alias.EmailAddress)
				}
			}
		}
		if validationErr := validateDistinctContactEmails(validationEmail, validationAliases); validationErr != nil {
			return validationErr
		}
	}

	contact, err := sdk.Contacts().Update(cmd.Context(), contactID, params)
	if err != nil {
		return convertContactWriteError(err)
	}
	if contact == nil {
		return output.ErrNotFound("contact", args[0])
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Contact updated: %s <%s> (#%d)\n", contact.Name, contact.EmailAddress, contact.Id)
		return nil
	}
	return writeOK(contact,
		output.WithSummary("Contact updated"),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "view", Command: fmt.Sprintf("hey contacts show %d", contact.Id), Description: "View the contact"}),
	)
}
