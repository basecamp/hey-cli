package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/output"
)

func newSetupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "First-run setup wizard",
		Long:  "Walks through initial authentication setup for hey CLI.",
		Annotations: map[string]string{
			"agent_notes": "Run this on first use. Performs OAuth login. Equivalent to hey auth login.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if authMgr.IsAuthenticated() {
				if writer.IsStyled() {
					fmt.Fprintln(cmd.OutOrStdout(), "Already authenticated. Run `hey auth status` for details.")
					return nil
				}
				return writeOK(map[string]string{"status": "already_authenticated"},
					output.WithSummary("Already authenticated"),
				)
			}

			if writer.IsStyled() {
				w := cmd.OutOrStdout()
				fmt.Fprintln(w, "Welcome to hey CLI!")
				fmt.Fprintln(w)
				fmt.Fprintln(w, "Let's get you logged in...")
				fmt.Fprintln(w)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()

			if err := authMgr.Login(ctx, auth.LoginOptions{}); err != nil {
				return output.ErrAuth(fmt.Sprintf("login failed: %v", err))
			}

			if writer.IsStyled() {
				w := cmd.OutOrStdout()
				fmt.Fprintln(w, "Setup complete! You're ready to use hey.")
				fmt.Fprintln(w)
				fmt.Fprintln(w, "Try: hey boxes")
				return nil
			}

			return writeOK(map[string]string{"status": "setup_complete"},
				output.WithSummary("Setup complete"),
				output.WithBreadcrumbs(output.Breadcrumb{
					Action:      "start",
					Command:     "hey boxes",
					Description: "List your mailboxes",
				}),
			)
		},
	}
}
