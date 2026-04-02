package cmd

import (
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/tui"
)

type tuiCommand struct {
	cmd *cobra.Command
}

func newTuiCommand() *tuiCommand {
	c := &tuiCommand{}
	c.cmd = &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			return tui.Run(sdk)
		},
	}
	return c
}
