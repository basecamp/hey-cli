package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// threadNameMaxBytes is where HEY cuts a thread's name: haystack's Topic::Named
// truncates it to 1024 bytes without saying so, which would leave the confirmation
// naming a subject the thread does not have.
const threadNameMaxBytes = 1024

type threadUpdateCommand struct {
	cmd  *cobra.Command
	name string
}

func newThreadUpdateCommand() *threadUpdateCommand {
	threadUpdateCommand := &threadUpdateCommand{}
	threadUpdateCommand.cmd = &cobra.Command{
		Use:     "update <thread-id>",
		Aliases: []string{"edit", "rename"},
		Short:   "Rename a thread",
		Long: "Rename a thread: change the subject HEY shows for it in your boxes and at the top " +
			"of the thread, for everyone in your account with access to it. Nothing is emailed: " +
			"the other people on the thread keep the subject they received, and messages already " +
			"sent keep the subject they were sent with. A collection's discussion thread is named " +
			"after its collection, so rename it with hey collection update instead.",
		Annotations: map[string]string{
			"agent_notes": "Renames a thread (topic) by its topic_id, as HEY's web app does; nothing is emailed. --name is required, trimmed, and at most 1024 bytes. A thread merged into another is not renamed and the command fails; rename the thread it was merged into instead. A collection's discussion thread shows its collection's name whatever this sets: use hey collection update for it.",
		},
		Example: `  hey thread update 12345 --name "Kitchen renovation quotes"
  hey thread rename 12345 --name "Flights to Lisbon, May 14"`,
		RunE: threadUpdateCommand.run,
		Args: usageExactOneArg(),
	}
	threadUpdateCommand.cmd.Flags().StringVar(&threadUpdateCommand.name, "name", "", "New thread name (required)")
	return threadUpdateCommand
}

func (c *threadUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	threadID, err := parsePositiveID(args[0], "thread")
	if err != nil {
		return err
	}
	// HEY would store a blank name as "No subject", so a blank is refused rather than
	// confirmed as a rename to nothing.
	name := strings.TrimSpace(c.name)
	if name == "" {
		return apierr.ErrUsage("thread name is required (use --name <name>)")
	}
	if len(name) > threadNameMaxBytes {
		return apierr.ErrUsage(fmt.Sprintf("thread name is too long (%d bytes, at most %d)", len(name), threadNameMaxBytes))
	}
	if err := sdk.Topics().Rename(cmd.Context(), threadID, name); err != nil {
		return threadRenameError(err, threadID)
	}
	return writeMutation(cmd, fmt.Sprintf("Thread %d renamed to %q", threadID, name), nil)
}

// threadRenameError says what an unacknowledged rename most likely was. HEY redirects
// a thread that was merged into another, before renaming anything, and net/http follows
// that PATCH as a GET; the SDK refuses the read that answers, and its status alone would
// leave the reader guessing whether the name changed.
func threadRenameError(err error, threadID int64) error {
	err = apierr.FromSDK(err)
	var cliErr *apierr.Error
	if errors.As(err, &cliErr) && cliErr.Code == apierr.CodeAPI &&
		cliErr.HTTPStatus >= http.StatusOK && cliErr.HTTPStatus < http.StatusBadRequest {
		cliErr.Message = fmt.Sprintf("thread %d was not renamed: HEY redirected the request, "+
			"which it does for a thread merged into another", threadID)
		cliErr.Hint = "Find the thread it was merged into with hey box view or hey search, and rename that one"
	}
	return err
}
