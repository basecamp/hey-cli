package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type statusCommand struct {
	cmd *cobra.Command
}

func newStatusCommand() *statusCommand {
	statusCommand := &statusCommand{}
	statusCommand.cmd = &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		RunE:  statusCommand.run,
	}

	return statusCommand
}

func (c *statusCommand) run(cmd *cobra.Command, args []string) error {
	fmt.Printf("Base URL:  %s\n", cfg.BaseURL)

	if !cfg.IsLoggedIn() {
		fmt.Println("Status:    Not logged in")
		return nil
	}

	fmt.Println("Status:    Logged in")

	token := cfg.AccessToken
	if len(token) > 12 {
		fmt.Printf("Token:     %s...%s\n", token[:8], token[len(token)-4:])
	}

	if cfg.TokenExpiry > 0 {
		expiry := time.Unix(cfg.TokenExpiry, 0)
		if time.Now().After(expiry) {
			fmt.Printf("Expiry:    Expired (%s)\n", expiry.Format(time.RFC3339))
		} else {
			fmt.Printf("Expiry:    %s\n", expiry.Format(time.RFC3339))
		}
	}

	if cfg.RefreshToken != "" {
		fmt.Println("Refresh:   Available")
	}

	return nil
}
