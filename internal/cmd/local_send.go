package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type localSendCommand struct {
	cmd *cobra.Command
}

// localSendSnapshot is the part of a waiting send's snapshot hey local shows: what the
// person will see in the HEY window before they press Send.
type localSendSnapshot struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
}

func newLocalSendCommand() *localSendCommand {
	sendCommand := &localSendCommand{}
	sendCommand.cmd = &cobra.Command{
		Use:   "send <draft>",
		Short: "Ask to send a saved draft, which you confirm in HEY",
		Long: `Ask the HEY app to send a saved draft. The app shows you exactly what would go,
from whom, to whom, and its words, and sends it only when you press Send in its
window.

hey local send never sends anything itself: it exits 9 and says "Waiting in HEY".
Asking again while the same draft waits answers the same confirmation.`,
		Annotations: map[string]string{
			"agent_notes": "Always exits 9 (pending) when the app takes the draft: nothing is sent until the person presses Send in the HEY window, and no command or flag here can press it. Tell the person it waits for them in HEY; never say it was sent. The draft ID is the HEY app's own, not hey draft list's.",
		},
		Example: `  hey local send 1731280000123
  hey local send 1731280000123 --json`,
		RunE: sendCommand.run,
		Args: usageExactOneArg(),
	}
	return sendCommand
}

func (c *localSendCommand) run(cmd *cobra.Command, args []string) error {
	draft, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || draft <= 0 {
		return apierr.ErrUsage(fmt.Sprintf("invalid draft ID: %s", args[0]))
	}
	result, err := callLocal(cmd, "message.send", map[string]any{"draft": draft})
	if err != nil {
		return err
	}
	return writeLocalAnswer(cmd, "message.send", result)
}
