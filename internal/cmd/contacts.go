package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type contactsCommand struct {
	cmd *cobra.Command
}

func newContactsCommand() *contactsCommand {
	contactsCommand := &contactsCommand{}
	contactsCommand.cmd = &cobra.Command{
		Use:   "contact",
		Short: "Manage contacts",
		Long:  "List, view, add, edit, hide, bundle, and annotate HEY contacts.",
		Annotations: map[string]string{
			"agent_notes": "Contact IDs come from `hey contact list`. Hiding and bundling are reversible. Private notes are managed under `hey contact note`.",
		},
	}

	contactsCommand.cmd.AddCommand(newContactsListCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsShowCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsAddCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsUpdateCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsHideCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsShowAgainCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsBundleCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsUnbundleCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactNoteCommand().cmd)
	return contactsCommand
}

func parseContactID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, apierr.ErrUsage(fmt.Sprintf("invalid contact ID: %s", value))
	}
	return id, nil
}

func contactNoun(count int) string {
	if count == 1 {
		return "contact"
	}
	return "contacts"
}

func convertContactWriteError(err error) error {
	var conflict *hey.ContactConflictError
	if !errors.As(err, &conflict) {
		return apierr.FromSDK(err)
	}
	ids := make([]string, 0, len(conflict.ConflictingContactIDs))
	for _, id := range conflict.ConflictingContactIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	hint := fmt.Sprintf("The written contact is %d.", conflict.ContactID)
	if len(ids) > 0 {
		hint += " Conflicting contact IDs: " + strings.Join(ids, ", ") + "."
	}
	return apierr.ErrConflict(409, conflict.Error(), hint, err)
}
