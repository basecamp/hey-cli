package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"hey-cli/internal/auth"
)

type authCommand struct {
	cmd *cobra.Command
}

func newAuthCommand() *authCommand {
	ac := &authCommand{}
	ac.cmd = &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
		Long:  "Manage authentication with the HEY server via Launchpad OAuth.",
	}

	ac.cmd.AddCommand(newAuthLoginCommand())
	ac.cmd.AddCommand(newAuthLogoutCommand())
	ac.cmd.AddCommand(newAuthStatusCommand())
	ac.cmd.AddCommand(newAuthRefreshCommand())
	ac.cmd.AddCommand(newAuthTokenCommand())

	return ac
}

// login subcommand

func newAuthLoginCommand() *cobra.Command {
	var (
		token     string
		cookie    string
		noBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with the HEY server",
		Long: `Authenticate with the HEY server via Launchpad OAuth.

Opens a browser for OAuth authentication. Use --token or --cookie for non-interactive login.`,
		Example: `  hey auth login
  hey auth login --token YOUR_BEARER_TOKEN
  hey auth login --cookie SESSION_COOKIE_VALUE
  hey auth login --no-browser`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token != "" {
				if err := authMgr.LoginWithToken(token); err != nil {
					return fmt.Errorf("could not save token: %w", err)
				}
				fmt.Println("Logged in with token.")
				return nil
			}

			if cookie != "" {
				if err := authMgr.LoginWithCookie(cookie); err != nil {
					return fmt.Errorf("could not save cookie: %w", err)
				}
				fmt.Println("Logged in with session cookie.")
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()

			if err := authMgr.Login(ctx, auth.LoginOptions{NoBrowser: noBrowser}); err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			fmt.Println("Logged in successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Pre-generated Bearer token")
	cmd.Flags().StringVar(&cookie, "cookie", "", "Session cookie value from browser (session_token)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Don't open browser, print URL instead")

	return cmd
}

// logout subcommand

func newAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear stored credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authMgr.Logout(); err != nil {
				return fmt.Errorf("could not clear credentials: %w", err)
			}
			fmt.Println("Logged out.")
			return nil
		},
	}
}

// status subcommand

func newAuthStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Base URL:  %s\n", cfg.BaseURL)

			if os.Getenv("HEY_TOKEN") != "" {
				fmt.Println("Status:    Logged in (via HEY_TOKEN env var)")
				return nil
			}

			store := authMgr.GetStore()
			creds, err := store.Load(authMgr.CredentialKey())
			if err != nil {
				fmt.Println("Status:    Not logged in")
				return nil
			}

			if creds.AccessToken == "" && creds.SessionCookie == "" {
				fmt.Println("Status:    Not logged in")
				return nil
			}

			fmt.Println("Status:    Logged in")

			if creds.OAuthType != "" {
				fmt.Printf("Auth:      %s\n", creds.OAuthType)
			}

			token := creds.AccessToken
			if len(token) > 12 {
				fmt.Printf("Token:     %s...%s\n", token[:8], token[len(token)-4:])
			} else if creds.SessionCookie != "" {
				cookie := creds.SessionCookie
				if len(cookie) > 12 {
					fmt.Printf("Cookie:    %s...%s\n", cookie[:8], cookie[len(cookie)-4:])
				}
			}

			if creds.ExpiresAt > 0 {
				expiry := time.Unix(creds.ExpiresAt, 0)
				if time.Now().After(expiry) {
					fmt.Printf("Expiry:    Expired (%s)\n", expiry.Format(time.RFC3339))
				} else {
					fmt.Printf("Expiry:    %s\n", expiry.Format(time.RFC3339))
				}
			}

			if creds.RefreshToken != "" {
				fmt.Println("Refresh:   Available")
			}

			if store.UsingKeyring() {
				fmt.Println("Storage:   system keyring")
			} else {
				fmt.Println("Storage:   file")
			}

			return nil
		},
	}
}

// refresh subcommand

func newAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force token refresh",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			if err := authMgr.Refresh(ctx); err != nil {
				return fmt.Errorf("refresh failed: %w", err)
			}
			fmt.Println("Token refreshed.")
			return nil
		},
	}
}

// token subcommand

func newAuthTokenCommand() *cobra.Command {
	var stored bool

	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print access token to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stored {
				if envToken := os.Getenv("HEY_TOKEN"); envToken != "" {
					fmt.Print(envToken)
					return nil
				}
			}

			ctx := context.Background()
			token, err := authMgr.AccessToken(ctx)
			if err != nil {
				return fmt.Errorf("could not get token: %w", err)
			}
			fmt.Print(token)
			return nil
		},
	}

	cmd.Flags().BoolVar(&stored, "stored", false, "Only print stored OAuth token (ignore HEY_TOKEN env var)")

	return cmd
}
