package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/output"
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
					return output.ErrAuth(fmt.Sprintf("could not save token: %v", err))
				}
				if writer.IsStyled() {
					fmt.Println("Logged in with token.")
					return nil
				}
				return writer.OK(map[string]string{"method": "token"}, output.WithSummary("Logged in with token"))
			}

			if cookie != "" {
				if err := authMgr.LoginWithCookie(cookie); err != nil {
					return output.ErrAuth(fmt.Sprintf("could not save cookie: %v", err))
				}
				if writer.IsStyled() {
					fmt.Println("Logged in with session cookie.")
					return nil
				}
				return writer.OK(map[string]string{"method": "cookie"}, output.WithSummary("Logged in with session cookie"))
			}

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()

			if err := authMgr.Login(ctx, auth.LoginOptions{NoBrowser: noBrowser}); err != nil {
				return output.ErrAuth(fmt.Sprintf("login failed: %v", err))
			}

			if writer.IsStyled() {
				fmt.Println("Logged in successfully.")
				return nil
			}
			return writer.OK(map[string]string{"method": "oauth"}, output.WithSummary("Logged in successfully"))
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
				return output.ErrAuth(fmt.Sprintf("could not clear credentials: %v", err))
			}
			if writer.IsStyled() {
				fmt.Println("Logged out.")
				return nil
			}
			return writer.OK(nil, output.WithSummary("Logged out"))
		},
	}
}

// status subcommand

func newAuthStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			status := map[string]any{
				"base_url":      cfg.BaseURL,
				"authenticated": false,
			}

			if os.Getenv("HEY_TOKEN") != "" {
				status["authenticated"] = true
				status["method"] = "env_var"

				if writer.IsStyled() {
					fmt.Printf("Base URL:  %s\n", cfg.BaseURL)
					fmt.Println("Status:    Logged in (via HEY_TOKEN env var)")
					return nil
				}
				return writer.OK(status, output.WithSummary("Logged in via HEY_TOKEN"))
			}

			store := authMgr.GetStore()
			creds, err := store.Load(authMgr.CredentialKey())
			if err != nil || (creds.AccessToken == "" && creds.SessionCookie == "") {
				if writer.IsStyled() {
					fmt.Printf("Base URL:  %s\n", cfg.BaseURL)
					fmt.Println("Status:    Not logged in")
					return nil
				}
				return writer.OK(status, output.WithSummary("Not logged in"),
					output.WithBreadcrumbs(output.Breadcrumb{
						Action:      "login",
						Command:     "hey auth login",
						Description: "Authenticate with HEY",
					}),
				)
			}

			status["authenticated"] = true
			if creds.OAuthType != "" {
				status["auth_type"] = creds.OAuthType
			}
			if store.UsingKeyring() {
				status["storage"] = "keyring"
			} else {
				status["storage"] = "file"
			}
			if creds.ExpiresAt > 0 {
				expiry := time.Unix(creds.ExpiresAt, 0)
				status["expires_at"] = expiry.Format(time.RFC3339)
				status["expired"] = time.Now().After(expiry)
			}
			if creds.RefreshToken != "" {
				status["refresh_available"] = true
			}

			if writer.IsStyled() {
				fmt.Printf("Base URL:  %s\n", cfg.BaseURL)
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
			}

			return writer.OK(status, output.WithSummary("Logged in"))
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
				return output.ErrAuth(fmt.Sprintf("refresh failed: %v", err))
			}
			if writer.IsStyled() {
				fmt.Println("Token refreshed.")
				return nil
			}
			return writer.OK(nil, output.WithSummary("Token refreshed"))
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
				return output.ErrAuth(fmt.Sprintf("could not get token: %v", err))
			}
			fmt.Print(token)
			return nil
		},
	}

	cmd.Flags().BoolVar(&stored, "stored", false, "Only print stored OAuth token (ignore HEY_TOKEN env var)")

	return cmd
}
