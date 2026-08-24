package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
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
			"agent_notes": "Use status to check auth before other commands. Returns token expiry info in JSON. Use login --token for non-interactive auth.",
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
		cookie    string
		noBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with the HEY server",
		Long: `Authenticate with the HEY server.

Opens a browser for OAuth authentication against HEY's own OAuth server, using PKCE.
Use --token or --cookie for non-interactive login.`,
		Example: strings.Join([]string{
			"  " + path,
			"  " + path + " --token YOUR_BEARER_TOKEN",
			"  " + path + " --cookie SESSION_COOKIE_VALUE",
			"  " + path + " --no-browser",
		}, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token != "" {
				if err := authMgr.LoginWithToken(token); err != nil {
					return apierr.ErrAuth(fmt.Sprintf("could not save token: %v", err))
				}
				return writeMutation(cmd, "Logged in with token", map[string]string{"method": "token"})
			}

			if cookie != "" {
				if err := authMgr.LoginWithCookie(cookie); err != nil {
					return apierr.ErrAuth(fmt.Sprintf("could not save cookie: %v", err))
				}
				return writeMutation(cmd, "Logged in with session cookie", map[string]string{"method": "cookie"})
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 6*time.Minute)
			defer cancel()

			if err := authMgr.Login(ctx, auth.LoginOptions{NoBrowser: noBrowser}); err != nil {
				return apierr.ErrAuth(fmt.Sprintf("login failed: %v", err))
			}

			if writer.IsStyled() {
				w := cmd.OutOrStdout()
				fmt.Fprintln(w, "Logged in successfully.")
				if identity, err := rootSDK.Identity().GetIdentity(cmd.Context()); err == nil && identity != nil {
					fmt.Fprintln(w, identityGreeting(identity))
				}
				printAgentNudge(w)
				ensureOmarchyBarPluginAfterLogin(cmd.ErrOrStderr())
				return nil
			}
			return writeOK(map[string]string{"method": "oauth"}, output.WithSummary("Logged in successfully"))
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Pre-generated Bearer token")
	cmd.Flags().StringVar(&cookie, "cookie", "", "Session cookie value from browser (session_token)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Don't open browser, print URL instead")

	return cmd
}

// logout subcommand

func newAuthLogoutCommand() *cobra.Command {
	return buildLogoutCommand("hey auth logout")
}

// buildLogoutCommand constructs a logout command whose example reads as the
// given path. Shared by `hey auth logout` and the top-level `hey logout`.
func buildLogoutCommand(path string) *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Clear stored credentials",
		Example: "  " + path,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authMgr.Logout(); err != nil {
				return apierr.ErrAuth(fmt.Sprintf("could not clear credentials: %v", err))
			}
			return writeMutation(cmd, "Logged out", nil)
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
				"base_url":       cfg.BaseURL,
				"mail_account":   cfg.AccountID,
				"account_source": cfg.SourceOf("account_id"),
				"authenticated":  false,
			}

			if os.Getenv("HEY_TOKEN") != "" {
				status["authenticated"] = true
				status["method"] = "env_var"

				if writer.IsStyled() {
					w := cmd.OutOrStdout()
					fmt.Fprintf(w, "Base URL:  %s\n", cfg.BaseURL)
					fmt.Fprintf(w, "Mail:      %s (%s)\n", cfg.AccountID, cfg.SourceOf("account_id"))
					fmt.Fprintln(w, "Status:    Logged in (via HEY_TOKEN env var)")
					return nil
				}
				return writeOK(status, output.WithSummary("Logged in via HEY_TOKEN"))
			}

			store := authMgr.GetStore()
			creds, err := store.Load(authMgr.CredentialKey())
			if err != nil || (creds.AccessToken == "" && creds.SessionCookie == "") {
				if writer.IsStyled() {
					w := cmd.OutOrStdout()
					fmt.Fprintf(w, "Base URL:  %s\n", cfg.BaseURL)
					fmt.Fprintf(w, "Mail:      %s (%s)\n", cfg.AccountID, cfg.SourceOf("account_id"))
					fmt.Fprintln(w, "Status:    Not logged in")
					return nil
				}
				return writeOK(status, output.WithSummary("Not logged in"),
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
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Base URL:  %s\n", cfg.BaseURL)
				fmt.Fprintf(w, "Mail:      %s (%s)\n", cfg.AccountID, cfg.SourceOf("account_id"))
				fmt.Fprintln(w, "Status:    Logged in")

				if creds.OAuthType != "" {
					fmt.Fprintf(w, "Auth:      %s\n", creds.OAuthType)
				}

				token := creds.AccessToken
				if len(token) > 12 {
					fmt.Fprintf(w, "Token:     %s...%s\n", token[:8], token[len(token)-4:])
				} else if creds.SessionCookie != "" {
					cookie := creds.SessionCookie
					if len(cookie) > 12 {
						fmt.Fprintf(w, "Cookie:    %s...%s\n", cookie[:8], cookie[len(cookie)-4:])
					}
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
				return nil
			}

			return writeOK(status, output.WithSummary("Logged in"))
		},
	}
}

// refresh subcommand

func newAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force token refresh",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authMgr.Refresh(cmd.Context()); err != nil {
				return apierr.ErrAuth(fmt.Sprintf("refresh failed: %v", err))
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

A login made with --cookie stores a browser session cookie instead. HEY sends that as a
Cookie header, so it is not a bearer token and this command refuses to print it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stored {
				if envToken := os.Getenv("HEY_TOKEN"); envToken != "" {
					fmt.Fprint(cmd.OutOrStdout(), envToken)
					return nil
				}
			}

			if err := refuseSessionCookieAsToken(); err != nil {
				return err
			}

			token, err := authMgr.AccessToken(cmd.Context())
			if err != nil {
				return apierr.ErrAuth(fmt.Sprintf("could not get token: %v", err))
			}
			fmt.Fprint(cmd.OutOrStdout(), token)
			return nil
		},
	}

	cmd.Flags().BoolVar(&stored, "stored", false, "Only print stored OAuth token (ignore HEY_TOKEN env var)")

	return cmd
}

// refuseSessionCookieAsToken stops `hey auth token` from printing a session cookie.
// AccessToken falls back to one, and a cookie sent as a bearer token 401s with
// nothing to explain it — besides leaving the cookie in the caller's shell history.
func refuseSessionCookieAsToken() error {
	creds, err := authMgr.GetStore().Load(authMgr.CredentialKey())
	if err == nil && creds.AccessToken == "" && creds.SessionCookie != "" {
		return &apierr.Error{
			Code:       apierr.CodeAuth,
			Message:    "the stored credential is a browser session cookie, not a bearer token",
			Hint:       "Run: hey auth login   (HEY sends a session cookie as a Cookie header, so it cannot be used as Bearer)",
			HTTPStatus: 401,
		}
	}
	return nil
}

// printAgentNudge prints a hint about coding agent setup after login.
//
// Detection proves presence, not intent: with a single detected-unhealthy
// agent it points at that agent; with several, it never guesses — it prints
// every `hey setup <id>` choice so the user picks. It never suggests
// `hey setup agents`, which is the installer's non-interactive path.
func printAgentNudge(w io.Writer) {
	type nudgeAgent struct{ id, name string }
	var unhealthy []nudgeAgent
	for _, agent := range harness.DetectedAgents() {
		if agent.Checks == nil {
			continue
		}
		for _, c := range agent.Checks() {
			if c.Status != "pass" {
				unhealthy = append(unhealthy, nudgeAgent{id: agent.ID, name: agent.Name})
				break
			}
		}
	}
	sort.Slice(unhealthy, func(i, j int) bool { return unhealthy[i].id < unhealthy[j].id })

	switch len(unhealthy) {
	case 0:
		return
	case 1:
		fmt.Fprintln(w)
		fmt.Fprintln(w, muted.format(fmt.Sprintf("  %s detected. Connect it to HEY:", unhealthy[0].name)))
		fmt.Fprintln(w, bold.format("  hey setup "+unhealthy[0].id))
	default:
		fmt.Fprintln(w)
		fmt.Fprintln(w, muted.format("  Multiple coding agents detected. Choose one:"))
		for _, a := range unhealthy {
			fmt.Fprintln(w, bold.format("  hey setup "+a.id))
		}
	}
}
