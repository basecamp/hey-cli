package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type contactsAddCommand struct {
	cmd           *cobra.Command
	name          string
	email         string
	aliases       []string
	accountUserID int64
}

func newContactsAddCommand() *contactsAddCommand {
	addCommand := &contactsAddCommand{}
	addCommand.cmd = &cobra.Command{
		Use:   "add",
		Short: "Add a contact",
		Annotations: map[string]string{
			"agent_notes": "Name and email are required. Repeat --alias for alternate email addresses. --account-user-id selects an account when the HEY identity has more than one.",
		},
		Example: `  hey contacts add --name "Jane Doe" --email jane@example.com
  hey contacts add --name "Jane Doe" --email jane@example.com --alias jane.doe@example.org`,
		RunE: addCommand.run,
		Args: cobra.NoArgs,
	}
	flags := addCommand.cmd.Flags()
	flags.StringVar(&addCommand.name, "name", "", "Contact name (required)")
	flags.StringVar(&addCommand.email, "email", "", "Primary email address (required)")
	flags.StringSliceVar(&addCommand.aliases, "alias", nil, "Alternate email address (repeatable)")
	flags.Int64Var(&addCommand.accountUserID, "account-user-id", 0, "Account user ID for multi-account identities")
	return addCommand
}

func (c *contactsAddCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	name := strings.TrimSpace(c.name)
	email := strings.TrimSpace(c.email)
	if name == "" {
		return output.ErrUsage("--name is required")
	}
	if email == "" {
		return output.ErrUsage("--email is required")
	}
	aliases := cleanContactAliases(c.aliases)
	if err := validateDistinctContactEmails(email, aliases); err != nil {
		return err
	}

	contact, err := sdk.Contacts().Create(cmd.Context(), hey.ContactParams{
		Name:                name,
		EmailAddress:        email,
		AliasEmailAddresses: aliases,
		AccountUserID:       c.accountUserID,
	})
	if err != nil {
		return convertContactWriteError(err)
	}
	if contact == nil {
		return output.ErrAPI(0, "contact add returned no data")
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Contact added: %s <%s> (#%d)\n", contact.Name, contact.EmailAddress, contact.Id)
		return nil
	}
	return writeOK(contact,
		output.WithSummary("Contact added"),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "view", Command: fmt.Sprintf("hey contacts show %d", contact.Id), Description: "View the contact"}),
	)
}

func cleanContactAliases(values []string) []string {
	aliases := make([]string, 0, len(values))
	for _, value := range values {
		if alias := strings.TrimSpace(value); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func validateDistinctContactEmails(email string, aliases []string) error {
	seen := map[string]bool{strings.ToLower(email): true}
	for _, alias := range aliases {
		key := strings.ToLower(alias)
		if seen[key] {
			return output.ErrUsage(fmt.Sprintf("duplicate contact email address: %s", alias))
		}
		seen[key] = true
	}
	return nil
}
