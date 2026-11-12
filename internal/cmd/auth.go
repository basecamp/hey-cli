package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type authCommand struct {
	cmd *cobra.Command
}

func newAuthCommand() *authCommand {
	ac := &authCommand{}
	ac.cmd = &cobra.Command{
		Use:   "auth",
		Short: "Sign in, sign out, and check login status",
		Long:  "Sign in to HEY, sign out, or check your login status.",
		Annotations: map[string]string{
			"agent_notes": "Data commands reuse stored credentials and refresh expiring OAuth tokens themselves, so run the data command without an auth preflight. Use hey auth status --json only when an explicit check is needed. Never run hey auth login unattended: the browser flow waits for a person, and --token is for a human handing over a credential, not something an agent runs on its own. A data command that exits 3 with code auth has no usable credentials; report it rather than signing in.",
		},
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
	return buildLoginCommand("hey auth login")
}

// buildLoginCommand constructs a login command whose examples read as the
// given path. Shared by `hey auth login` and the top-level `hey login`.
func buildLoginCommand(path string) *cobra.Command {
	var (
		token     string
		noBrowser bool
		mcp       bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with the HEY server",
		Long: `Authenticate with the HEY server.

Opens a browser for OAuth authentication against HEY's own OAuth server, using PKCE.
Use --token for non-interactive login.

--mcp signs in hey mcp instead, as its own app ("HEY MCP (local)" in HEY's Connected
Apps), asking to read, write and send but nothing more. Its credential is kept apart
from the CLI's, and hey mcp serves on it alone. It uses a device code where HEY
offers one, and the browser otherwise. If you uncheck sending on HEY's page, hey mcp
saves drafts but can't send them.`,
		Example: strings.Join([]string{
			"  " + path,
			"  " + path + " --token YOUR_BEARER_TOKEN",
			"  " + path + " --no-browser",
			"  " + path + " --mcp",
		}, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if mcp {
				if token != "" || cmd.Flags().Changed("token") {
					return apierr.ErrUsage("--mcp signs in through HEY; it takes no --token")
				}
				return loginMCP(cmd, noBrowser)
			}

			if token != "" {
				if err := authMgr.LoginWithToken(token); err != nil {
					return apierr.ErrAuth(fmt.Sprintf("could not save token: %v", err))
				}
				// Replaced credentials orphan whatever the old ones cached.
				clearHTTPCache(cmd.ErrOrStderr())
				return writeMutation(cmd, "Logged in with token", map[string]string{"method": "token"})
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 6*time.Minute)
			defer cancel()

			if err := authMgr.Login(ctx, auth.LoginOptions{NoBrowser: noBrowser}); err != nil {
				return apierr.ErrAuth(fmt.Sprintf("login failed: %v", err))
			}
			// Replaced credentials orphan whatever the old ones cached.
			clearHTTPCache(cmd.ErrOrStderr())

			if writer.IsStyled() {
				w := cmd.OutOrStdout()
				fmt.Fprintln(w, "Logged in successfully.")
				if identity, err := rootSDK.Identity().GetIdentity(cmd.Context()); err == nil && identity != nil {
					fmt.Fprintln(w, identityGreeting(identity))
				}
				// Agent integration is configured through `hey setup`; sign-in reports
				// the authenticated identity and completes its Omarchy integration.
				ensureOmarchyBarPluginAfterLogin(cmd.ErrOrStderr())
				return nil
			}
			return writeOK(map[string]string{"method": "oauth"}, output.WithSummary("Logged in successfully"))
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Pre-generated Bearer token")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Don't open browser, print URL instead")
	cmd.Flags().BoolVar(&mcp, "mcp", false, "Sign in hey mcp with its own credential (read, write and send)")

	return cmd
}

// logout subcommand

func newAuthLogoutCommand() *cobra.Command {
	return buildLogoutCommand("hey auth logout")
}

// buildLogoutCommand constructs a logout command whose example reads as the
// given path. Shared by `hey auth logout` and the top-level `hey logout`.
func buildLogoutCommand(path string) *cobra.Command {
	var mcp bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear stored credentials",
		Long: `Clear the CLI's stored credentials.

--mcp signs hey mcp out instead: it revokes hey mcp's sign-in at HEY and removes its
credential, leaving the CLI's alone.`,
		Example: "  " + path + "\n  " + path + " --mcp",
		RunE: func(cmd *cobra.Command, args []string) error {
			if mcp {
				return logoutMCP(cmd)
			}
			if err := authMgr.Logout(); err != nil {
				return apierr.ErrAuth(fmt.Sprintf("could not clear credentials: %v", err))
			}
			// Cached mail must not outlive the credentials that fetched it.
			clearHTTPCache(cmd.ErrOrStderr())
			return writeMutation(cmd, "Logged out", nil)
		},
	}
	cmd.Flags().BoolVar(&mcp, "mcp", false, "Sign hey mcp out: revoke its sign-in and remove its credential")
	return cmd
}

// status subcommand

func newAuthStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		Annotations: map[string]string{
			"agent_notes": "Use it when the user asks for authentication status or when an explicit check helps diagnose a failed command, not as a preflight. It reports stored credentials without contacting HEY and changes nothing, so authenticated true does not prove HEY still accepts them. JSON answers authenticated, base_url, mail_account, account_source and install_id; method env_var when HEY_TOKEN is set; otherwise storage (keyring or file), auth_type, and expires_at, expired and refresh_available for a token. An expired or missing access token with refresh_available is renewed by the next data command. sign_in_required explains an obsolete cookie-only sign-in and how to replace it. mcp reports hey mcp's own sign-in the same way, with its scope and can_send; HEY_TOKEN never stands in for it.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mcpStatus := mcpCredentialStatus()
			status := map[string]any{
				"base_url":       cfg.BaseURL,
				"mail_account":   cfg.AccountID,
				"account_source": cfg.SourceOf("account_id"),
				"authenticated":  false,
				"mcp":            mcpStatus,
			}
			if cfg.ActiveProfile != "" {
				status["profile"] = cfg.ActiveProfile
			}

			// The install identifier is install-scoped: it survives logout and
			// is sent on every OAuth login and refresh. Surface it in every
			// status path — env token and logged-out included, both of which
			// return before the signed-in path — so the JSON and styled output
			// stay consistent and can diagnose HEY's new-device alerts.
			installID, _ := authMgr.GetStore().InstallID()
			if installID != "" {
				status["install_id"] = installID
			}

			if os.Getenv("HEY_TOKEN") != "" {
				status["authenticated"] = true
				status["method"] = "env_var"

				if writer.IsStyled() {
					w := cmd.OutOrStdout()
					printProfileLine(w)
					fmt.Fprintf(w, "Base URL:  %s\n", cfg.BaseURL)
					fmt.Fprintf(w, "Mail:      %s (%s)\n", cfg.AccountID, cfg.SourceOf("account_id"))
					if installID != "" {
						fmt.Fprintf(w, "Install:   %s\n", installID)
					}
					fmt.Fprintln(w, "Status:    Logged in (via HEY_TOKEN env var)")
					printMCPStatus(w, mcpStatus)
					return nil
				}
				return writeOK(status, output.WithSummary("Logged in via HEY_TOKEN"))
			}

			store := authMgr.GetStore()
			creds, err := store.Load(authMgr.CredentialKey())
			if err != nil && !errors.Is(err, auth.ErrCredentialsNotFound) {
				return auth.CredentialStorageError("could not read authentication status", err)
			}
			if err == nil && creds.AccessToken == "" && creds.RefreshToken == "" && creds.SessionCookie != "" {
				status["sign_in_required"] = "Browser session cookie authentication is no longer supported. Run: " + authMgr.LoginCommand()
				if writer.IsStyled() {
					fmt.Fprintln(cmd.OutOrStdout(), status["sign_in_required"])
				}
			}
			if errors.Is(err, auth.ErrCredentialsNotFound) || (creds.AccessToken == "" && creds.RefreshToken == "") {
				if writer.IsStyled() {
					w := cmd.OutOrStdout()
					printProfileLine(w)
					fmt.Fprintf(w, "Base URL:  %s\n", cfg.BaseURL)
					fmt.Fprintf(w, "Mail:      %s (%s)\n", cfg.AccountID, cfg.SourceOf("account_id"))
					if installID != "" {
						fmt.Fprintf(w, "Install:   %s\n", installID)
					}
					fmt.Fprintln(w, "Status:    Not logged in")
					printMCPStatus(w, mcpStatus)
					return nil
				}
				return writeOK(status, output.WithSummary("Not logged in"),
					output.WithBreadcrumbs(output.Breadcrumb{
						Action:      "login",
						Command:     authMgr.LoginCommand(),
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
				w := cmd.OutOrStdout()
				printProfileLine(w)
				fmt.Fprintf(w, "Base URL:  %s\n", cfg.BaseURL)
				fmt.Fprintf(w, "Mail:      %s (%s)\n", cfg.AccountID, cfg.SourceOf("account_id"))
				fmt.Fprintln(w, "Status:    Logged in")

				if creds.OAuthType != "" {
					fmt.Fprintf(w, "Auth:      %s\n", creds.OAuthType)
				}

				token := creds.AccessToken
				if len(token) > 12 {
					fmt.Fprintf(w, "Token:     %s...%s\n", token[:8], token[len(token)-4:])
				}
				if installID != "" {
					fmt.Fprintf(w, "Install:   %s\n", installID)
				}

				if creds.ExpiresAt > 0 {
					expiry := time.Unix(creds.ExpiresAt, 0)
					if time.Now().After(expiry) {
						fmt.Fprintf(w, "Expiry:    Expired (%s)\n", expiry.Format(time.RFC3339))
					} else {
						fmt.Fprintf(w, "Expiry:    %s\n", expiry.Format(time.RFC3339))
					}
				}

				if creds.RefreshToken != "" {
					fmt.Fprintln(w, "Refresh:   Available")
				}

				if store.UsingKeyring() {
					fmt.Fprintln(w, "Storage:   system keyring")
				} else {
					fmt.Fprintln(w, "Storage:   file")
				}
				printMCPStatus(w, mcpStatus)
				return nil
			}

			return writeOK(status, output.WithSummary("Logged in"))
		},
	}
}

// printProfileLine names the active profile in a styled status, and prints nothing
// with none.
func printProfileLine(w io.Writer) {
	if cfg.ActiveProfile != "" {
		fmt.Fprintf(w, "Profile:   %s\n", terminal.SanitizeLine(cfg.ActiveProfile))
	}
}

// refresh subcommand

func newAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force token refresh",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authMgr.Refresh(cmd.Context()); err != nil {
				return authFailure("refresh failed", err)
			}
			return writeMutation(cmd, "Token refreshed", nil)
		},
	}
}

// token subcommand

func newAuthTokenCommand() *cobra.Command {
	var stored bool

	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print access token to stdout",
		Long: `Print the stored bearer token to stdout, for use as "Authorization: Bearer <token>".

Obsolete browser session cookie credentials require signing in again with hey auth login.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stored {
				if envToken := os.Getenv("HEY_TOKEN"); envToken != "" {
					fmt.Fprint(cmd.OutOrStdout(), envToken)
					return nil
				}
			}

			var token string
			var err error
			if stored {
				token, err = authMgr.StoredAccessToken(cmd.Context())
			} else {
				token, err = authMgr.AccessToken(cmd.Context())
			}
			if err != nil {
				return authFailure("could not get token", err)
			}
			fmt.Fprint(cmd.OutOrStdout(), token)
			return nil
		},
	}

	cmd.Flags().BoolVar(&stored, "stored", false, "Only print stored OAuth token (ignore HEY_TOKEN env var)")

	return cmd
}

// authFailure reports a manager failure with the command's context in front of it.
// The manager classifies its own refusals — a refused grant is auth, a throttled
// token endpoint is rate_limit with how long to wait — and wrapping every one as
// ErrAuth turned a 429 or a storage failure into exit 3 and "Run: hey auth login".
// A classified error keeps its code, hint and status; an unclassified one keeps
// its cause. Both gain only the command's context.
func authFailure(context string, err error) error {
	var classified *apierr.Error
	if !errors.As(err, &classified) {
		return fmt.Errorf("%s: %w", context, err)
	}
	prefixed := *classified
	prefixed.Message = fmt.Sprintf("%s: %s", context, classified.Message)
	return &prefixed
}
