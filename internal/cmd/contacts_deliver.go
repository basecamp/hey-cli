package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type contactsDeliverCommand struct {
	cmd         *cobra.Command
	to          string
	contactID   int64
	destination contactDeliveryDestination
}

type contactDeliveryDestination struct {
	token   string
	label   string
	boxKind string
}

type contactDeliveryResult struct {
	ID          int64  `json:"id"`
	Destination string `json:"destination"`
}

func newContactsDeliverCommand() *contactsDeliverCommand {
	deliverCommand := &contactsDeliverCommand{}
	deliverCommand.cmd = &cobra.Command{
		Use:   "deliver <contact-id>",
		Short: "Choose where a contact's email arrives",
		Long: "Choose where future email from a contact arrives: the Imbox, The Feed, Paper Trail, or Screened Out. " +
			"Screened Out applies only to external email contacts. Selecting The Feed removes an existing bundle; Imbox and Paper Trail preserve eligible bundles.",
		Annotations: map[string]string{
			"agent_notes": "Use a contact ID from `hey contact list`, not a clearance ID, box item ID, box ID, or email address. Accepted --to values: imbox, feed, papertrail, screened-out. This command changes the setting but cannot read it back.",
		},
		Example: `  hey contact deliver 12345 --to feed
  hey contact deliver 12345 --to screened-out`,
		Args: deliverCommand.validateArgs,
		RunE: deliverCommand.run,
	}
	deliverCommand.cmd.Flags().StringVar(&deliverCommand.to, "to", "", "Delivery destination: imbox, feed, papertrail, or screened-out")
	if err := deliverCommand.cmd.MarkFlagRequired("to"); err != nil {
		panic(err)
	}
	return deliverCommand
}

func (c *contactsDeliverCommand) validateArgs(cmd *cobra.Command, args []string) error {
	if err := usageExactOneArg()(cmd, args); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	destination, err := parseContactDeliveryDestination(c.to)
	if err != nil {
		return err
	}
	c.contactID = contactID
	c.destination = destination
	return nil
}

func parseContactDeliveryDestination(value string) (contactDeliveryDestination, error) {
	switch value {
	case "imbox":
		return contactDeliveryDestination{token: value, label: "Imbox", boxKind: hey.BoxKindImbox}, nil
	case "feed":
		return contactDeliveryDestination{token: value, label: "The Feed", boxKind: hey.BoxKindFeed}, nil
	case "papertrail":
		return contactDeliveryDestination{token: value, label: "Paper Trail", boxKind: hey.BoxKindTrail}, nil
	case "screened-out":
		return contactDeliveryDestination{token: value, label: "Screened Out"}, nil
	case "":
		return contactDeliveryDestination{}, apierr.ErrUsage("--to is required")
	default:
		return contactDeliveryDestination{}, apierr.ErrUsage(fmt.Sprintf("unsupported delivery destination: %s", value))
	}
}

func (c *contactsDeliverCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if c.destination.boxKind != "" {
		boxID, err := sdk.BoxIDByKind(cmd.Context(), c.destination.boxKind)
		if err != nil {
			return apierr.FromSDK(err)
		}
		if err := sdk.Designations().Create(cmd.Context(), boxID, c.contactID); err != nil {
			return apierr.FromSDK(err)
		}
	} else {
		contact, err := sdk.Contacts().Get(cmd.Context(), c.contactID)
		if err != nil {
			return apierr.FromSDK(err)
		}
		if contact == nil {
			return apierr.ErrNotFound("contact", fmt.Sprintf("%d", c.contactID))
		}
		if !screenableContactType(contact.ContactableType) {
			return apierr.ErrValidation(0,
				fmt.Sprintf("contact %d cannot be screened out", c.contactID),
				"Screened Out applies only to external email contacts.", nil)
		}
		if err := sdk.Contacts().Screen(cmd.Context(), c.contactID, hey.ClearanceDenied); err != nil {
			return apierr.FromSDK(err)
		}
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Delivery changed for contact %d: %s.", c.contactID, c.destination.label),
		"Contact delivery changed",
		contactDeliveryResult{ID: c.contactID, Destination: c.destination.token},
	)
}

func screenableContactType(contactType string) bool {
	switch contactType {
	case "Alias", "Person", "Service":
		return true
	default:
		return false
	}
}
