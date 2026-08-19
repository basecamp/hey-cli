package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type contactsCommand struct {
	cmd *cobra.Command
}

func newContactsCommand() *contactsCommand {
	contactsCommand := &contactsCommand{}
	contactsCommand.cmd = &cobra.Command{
		Use:   "contacts",
		Short: "Manage contacts",
		Long:  "List, view, add, edit, hide, and annotate HEY contacts.",
		Annotations: map[string]string{
			"agent_notes": "Contact IDs come from `hey contacts list`. Hiding is reversible with `hey contacts show-again <id>`. Private notes are managed under `hey contacts note`.",
		},
	}

	contactsCommand.cmd.AddCommand(newContactsListCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsShowCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsAddCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsUpdateCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsHideCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactsShowAgainCommand().cmd)
	contactsCommand.cmd.AddCommand(newContactNoteCommand().cmd)
	return contactsCommand
}

func parseContactID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, output.ErrUsage(fmt.Sprintf("invalid contact ID: %s", value))
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
		return convertSDKError(err)
	}
	ids := make([]string, 0, len(conflict.ConflictingContactIDs))
	for _, id := range conflict.ConflictingContactIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	hint := fmt.Sprintf("The written contact is %d.", conflict.ContactID)
	if len(ids) > 0 {
		hint += " Conflicting contact IDs: " + strings.Join(ids, ", ") + "."
	}
	return &output.Error{Code: "conflict", Message: conflict.Error(), Hint: hint, HTTPStatus: 409, Cause: err}
}
