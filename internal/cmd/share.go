package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type shareCommand struct {
	cmd *cobra.Command
}

func newShareCommand() *shareCommand {
	shareCommand := &shareCommand{}
	shareCommand.cmd = &cobra.Command{
		Use:   "share <thread-id>",
		Short: "Get a sharing link for an email thread",
		Long:  "Get a sharing link for an email thread. Anyone with the link can see the entire thread and future emails or replies sent to it.",
		Example: `  hey share 12345
  hey share 12345 --json`,
		Annotations: map[string]string{
			"agent_notes": "Accepts the topic_id from hey box view, hey label view, or hey search output. Returns the sharing link in the url field.",
		},
		RunE: shareCommand.run,
		Args: usageExactOneArg(),
	}

	return shareCommand
}

func (c *shareCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := parsePositiveID(args[0], "thread")
	if err != nil {
		return err
	}

	publication, err := sdk.Publications().Create(cmd.Context(), threadID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if publication == nil || !publication.Published || publication.Url == "" {
		return apierr.ErrAPI(0, "HEY did not return a sharing link")
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Sharing link: %s", terminal.SanitizeLine(publication.Url)),
		"Sharing link turned on",
		publication)
}

type unshareCommand struct {
	cmd *cobra.Command
}

func newUnshareCommand() *unshareCommand {
	unshareCommand := &unshareCommand{}
	unshareCommand.cmd = &cobra.Command{
		Use:     "unshare <thread-id>",
		Short:   "Turn off an email thread's sharing link",
		Long:    "Turn off the sharing link for an email thread.",
		Example: `  hey unshare 12345`,
		Annotations: map[string]string{
			"agent_notes": "Accepts the topic_id from hey box view, hey label view, or hey search output. The thread remains in HEY.",
		},
		RunE: unshareCommand.run,
		Args: usageExactOneArg(),
	}

	return unshareCommand
}

func (c *unshareCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := parsePositiveID(args[0], "thread")
	if err != nil {
		return err
	}

	if err := sdk.Publications().Delete(cmd.Context(), threadID); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Sharing link turned off", nil)
}
