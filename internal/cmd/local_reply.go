package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type localReplyCommand struct {
	cmd     *cobra.Command
	message string
}

func newLocalReplyCommand() *localReplyCommand {
	replyCommand := &localReplyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread>",
		Short: "Write a reply as a draft in the HEY app",
		Long: `Write a reply to an Imbox thread as a draft in the HEY app on this computer.
Nothing is sent: hey local send asks HEY to send it, and it waits for you to press
Send in HEY.

<thread> is a thread hey local imbox shows, from its Thread column. The reply
answers the thread's last message, to everyone it went to, as HEY's Reply does,
with the message above your name tag and the quote. It is a new draft beside any
you have on the thread, never written over yours.`,
		Annotations: map[string]string{
			"agent_notes": "Runs the app's draft.reply, which saves a draft and sends nothing. <thread> is a topic from hey local imbox (its topic field, the Thread column), not the row's id; only Imbox threads are reachable. It answers the draft, whose id hey local send takes. Not idempotent: each run writes one more draft. The message is plain text, read from -m or stdin.",
		},
		Example: `  hey local reply 801 -m "Saturday works"
  hey local reply 801 -m "Saturday works" --json`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}
	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Message as plain text (or pipe it to stdin)")
	return replyCommand
}

func (c *localReplyCommand) run(cmd *cobra.Command, args []string) error {
	thread, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || thread <= 0 {
		return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}
	body, err := localMessageBody(c.message)
	if err != nil {
		return err
	}
	result, err := callLocal(cmd, "draft.reply", map[string]any{"thread": thread, "body": body})
	if err != nil {
		return err
	}
	return writeLocalDraft(cmd, "draft.reply", result, thread)
}
