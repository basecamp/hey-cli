package cmd

import (
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/tui"
)

type tuiCommand struct {
	cmd *cobra.Command
}

func newTuiCommand() *tuiCommand {
	return &tuiCommand{cmd: newTuiRunner("tui", false)}
}

func newHeyCommand() *tuiCommand {
	return &tuiCommand{cmd: newTuiRunner("hey", true)}
}

func newTuiRunner(use string, hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:    use,
		Short:  "Launch the interactive terminal UI",
		Hidden: hidden,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			return tui.Run(rootSDK, sdk, cfg.AccountID)
		},
	}
}
