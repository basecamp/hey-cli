package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type logoutCommand struct {
	cmd *cobra.Command
}

func newLogoutCommand() *logoutCommand {
	logoutCommand := &logoutCommand{}
	logoutCommand.cmd = &cobra.Command{
		Use:   "logout",
		Short: "Clear stored credentials",
		RunE:  logoutCommand.run,
	}

	return logoutCommand
}

func (c *logoutCommand) run(cmd *cobra.Command, args []string) error {
	if err := cfg.Clear(); err != nil {
		return fmt.Errorf("could not clear config: %w", err)
	}
	fmt.Println("Logged out.")
	return nil
}
