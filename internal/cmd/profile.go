package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// The profile commands are basecamp-cli's (internal/commands/profile.go at 48b01aaf):
// the same five, the same output fields and the same words. profile create signs in
// before it writes anything, so a failed sign-in leaves no profile behind.

type profileCommand struct {
	cmd *cobra.Command
}

func newProfileCommand() *profileCommand {
	profileCommand := &profileCommand{}
	profileCommand.cmd = &cobra.Command{
		Use:   "profile",
		Short: "Manage named profiles",
		Long: `Manage named profiles that bundle identity (credentials) with environment (server + mail account).

Profiles let you keep more than one HEY sign-in on this computer: a second account,
or a separate configuration for staging or a beta. Each profile has its own
credential, stored apart from every other's. Choose one per command with --profile
(-P) or HEY_PROFILE, or set a default. With no profiles configured, hey uses the
top-level configuration, as it always has.

Examples:
  hey profile list                                         # List all profiles
  hey profile show                                         # Show active profile details
  hey profile create personal                              # Create a new profile
  hey profile create staging --base-url https://staging.example.com
  hey profile delete old-profile                           # Remove a profile
  hey profile set-default personal                         # Set default profile
  hey -P personal box view imbox                           # Run one command as a profile`,
		Annotations: map[string]string{
			"agent_notes": "A profile is a named server, mail account and credential. Pass -P <name> (or set HEY_PROFILE) to run any command as that profile; with several profiles and no default_profile, a command without one exits 1 naming the profiles. profile create signs in through the browser, so never run it unattended. JSON answers carry meta.profile when a profile is active.",
		},
	}

	profileCommand.cmd.AddCommand(newProfileListCommand())
	profileCommand.cmd.AddCommand(newProfileShowCommand())
	profileCommand.cmd.AddCommand(newProfileCreateCommand())
	profileCommand.cmd.AddCommand(newProfileDeleteCommand())
	profileCommand.cmd.AddCommand(newProfileSetDefaultCommand())

	return profileCommand
}

func newProfileListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all profiles",
		Long:  "List all configured profiles with their base URL and authentication status.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(cfg.Profiles) == 0 {
				if writer.IsStyled() {
					fmt.Fprintln(cmd.OutOrStdout(), "No profiles configured")
					return nil
				}
				return writeOK([]any{}, output.WithSummary("No profiles configured"))
			}

			names := cfg.ProfileNames()
			profiles := make([]map[string]any, 0, len(names))
			for _, name := range names {
				p := cfg.Profiles[name]
				entry := map[string]any{
					"name":          name,
					"base_url":      p.BaseURL,
					"authenticated": profileAuthenticated(name),
				}
				if cfg.DefaultProfile == name {
					entry["default"] = true
				}
				if cfg.ActiveProfile == name {
					entry["active"] = true
				}
				if p.AccountID != "" {
					entry["account_id"] = p.AccountID
				}
				profiles = append(profiles, entry)
			}

			if writer.IsStyled() {
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"Name", "Base URL", "Account", "Signed in", ""})
				for i, name := range names {
					p := cfg.Profiles[name]
					signedIn := "no"
					if profiles[i]["authenticated"] == true {
						signedIn = "yes"
					}
					var marks []string
					if cfg.ActiveProfile == name {
						marks = append(marks, "active")
					}
					if cfg.DefaultProfile == name {
						marks = append(marks, "default")
					}
					table.addRow([]string{terminal.SanitizeLine(name), terminal.SanitizeLine(p.BaseURL), p.AccountID, signedIn, strings.Join(marks, ", ")})
				}
				table.print()
				return nil
			}
			return writeOK(profiles, output.WithSummary(fmt.Sprintf("%d profile(s)", len(profiles))))
		},
	}
}

func newProfileShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show profile details",
		Long:  "Show configuration and authentication details for a profile.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			switch {
			case len(args) > 0:
				name = args[0]
			case cfg.ActiveProfile != "":
				name = cfg.ActiveProfile
			case cfg.DefaultProfile != "":
				name = cfg.DefaultProfile
			default:
				return cmd.Help()
			}

			p, ok := cfg.Profiles[name]
			if !ok {
				return apierr.ErrUsage(fmt.Sprintf("Profile %q not found", name))
			}

			result := map[string]any{
				"name":          name,
				"base_url":      p.BaseURL,
				"authenticated": false,
			}
			if p.AccountID != "" {
				result["account_id"] = p.AccountID
			}
			if cfg.DefaultProfile == name {
				result["default"] = true
			}
			creds, err := authMgr.GetStore().Load(auth.ProfileCredentialKey(name))
			if err != nil && !errors.Is(err, auth.ErrCredentialsNotFound) {
				return auth.CredentialStorageError("could not read the profile's credential", err)
			}
			if err == nil && (creds.AccessToken != "" || creds.RefreshToken != "") {
				result["authenticated"] = true
				if creds.OAuthType != "" {
					result["oauth_type"] = creds.OAuthType
				}
				if creds.Scope != "" {
					result["credential_scope"] = creds.Scope
				}
			}

			if writer.IsStyled() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Profile:   %s\n", terminal.SanitizeLine(name))
				fmt.Fprintf(w, "Base URL:  %s\n", terminal.SanitizeLine(p.BaseURL))
				if p.AccountID != "" {
					fmt.Fprintf(w, "Mail:      %s\n", p.AccountID)
				}
				if cfg.DefaultProfile == name {
					fmt.Fprintln(w, "Default:   yes")
				}
				if result["authenticated"] == true {
					fmt.Fprintln(w, "Status:    Logged in")
				} else {
					fmt.Fprintln(w, "Status:    Not logged in")
				}
				return nil
			}
			return writeOK(result, output.WithSummary(fmt.Sprintf("Profile: %s", name)))
		},
	}
}

// signInProfile runs a profile's sign-in. A seam so tests need no browser.
var signInProfile = func(ctx context.Context, mgr *auth.Manager, opts auth.LoginOptions) error {
	return mgr.Login(ctx, opts)
}

func newProfileCreateCommand() *cobra.Command {
	var (
		profileBaseURL string
		accountID      string
		noBrowser      bool
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new profile",
		Long: `Create a new named profile and sign it in.

The sign-in opens a browser, as hey auth login does, and the profile is written only
once it succeeds. The first profile becomes the default.

Examples:
  hey profile create personal
  hey profile create staging --base-url https://staging.example.com
  hey profile create work --account 12345`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			// Validate profile name (used in credential keys and cache paths)
			if !config.ValidProfileName(name) {
				return apierr.ErrUsage(fmt.Sprintf("Invalid profile name %q: use only letters, numbers, hyphens, and underscores", name))
			}
			if _, exists := cfg.Profiles[name]; exists {
				return apierr.ErrUsage(fmt.Sprintf("Profile %q already exists", name))
			}

			if profileBaseURL == "" {
				profileBaseURL = config.Defaults().BaseURL
			}
			if err := config.ValidateBaseURL(profileBaseURL); err != nil {
				return err
			}
			profile := config.ProfileConfig{BaseURL: profileBaseURL}
			if accountID != "" {
				normalized, err := config.NormalizeAccountID(accountID)
				if err != nil {
					return err
				}
				profile.AccountID = normalized
			}

			// The entry is written only after the login succeeds, so prove the config
			// file can take it before a credential exists to orphan.
			if err := config.GlobalConfigTakesProfiles(); err != nil {
				return err
			}

			mgr := auth.NewManager(profile.BaseURL, &http.Client{Timeout: 30 * time.Second}, config.ConfigDir())
			mgr.UseProfile(name)
			ctx, cancel := context.WithTimeout(cmd.Context(), 6*time.Minute)
			defer cancel()
			if err := signInProfile(ctx, mgr, auth.LoginOptions{NoBrowser: noBrowser}); err != nil {
				return apierr.ErrAuth(fmt.Sprintf("login failed: %v", err))
			}
			clearProfileHTTPCache(cmd, name)

			isDefault, err := config.RegisterProfile(name, profile)
			if err != nil {
				// No profile names the credential just stored, so nothing would reach it.
				if revokeErr := mgr.Revoke(ctx); revokeErr != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), terminal.SanitizeLine(fmt.Sprintf("Warning: could not revoke the sign-in for profile %q: %v", name, revokeErr)))
				}
				if logoutErr := mgr.Logout(); logoutErr != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), terminal.SanitizeLine(fmt.Sprintf("Warning: could not delete credentials for profile %q: %v", name, logoutErr)))
				}
				return err
			}

			result := map[string]any{
				"name":     name,
				"base_url": profile.BaseURL,
			}
			if profile.AccountID != "" {
				result["account_id"] = profile.AccountID
			}
			if isDefault {
				result["default"] = true
			}
			summary := fmt.Sprintf("Created profile %q", name)
			return writeMutationLine(cmd, terminal.SanitizeLine(summary)+".", summary, result)
		},
	}

	cmd.Flags().StringVar(&profileBaseURL, "base-url", "", "HEY server URL (default: https://app.hey.com)")
	cmd.Flags().StringVar(&accountID, "account", "", "Linked mail account ID or all")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Don't open browser, print URL instead")

	return cmd
}

func newProfileDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Long:  "Remove a profile configuration and its stored credentials, revoking the credential with the server when it can be.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			p, ok := cfg.Profiles[name]
			if !ok {
				return apierr.ErrUsage(fmt.Sprintf("Profile %q not found", name))
			}
			if !cfg.ProfileInGlobalConfig(name) {
				// Its entry would outlive the delete and bring the profile back.
				where := "this repository's .hey/config.json"
				if local := cfg.LocalConfig(); local != nil {
					where = local.Path
				}
				return apierr.ErrUsage(fmt.Sprintf("Profile %q is set by %s, not the global config; remove it there", name, terminal.SanitizeLine(where)))
			}
			if local := cfg.LocalConfig(); local != nil && local.Trusted && localEntrySetsProfile(local, name) {
				// Its repository entry would bring the profile back, signed out, here.
				return apierr.ErrUsage(fmt.Sprintf("Profile %q is also set by %s; remove it there first", name, terminal.SanitizeLine(local.Path)))
			}

			// The credential delete is irreversible, so prove the config file can take
			// the entry's removal before it goes.
			if err := config.GlobalConfigTakesProfiles(); err != nil {
				return err
			}

			fields := map[string]any{"name": name, "status": "deleted"}
			// The revocation goes to the deleted profile's own saved server, never to
			// the active configuration's, which may belong to another profile.
			httpClient := &http.Client{Timeout: 15 * time.Second}
			cli := auth.NewManager(p.BaseURL, httpClient, config.ConfigDir())
			cli.UseProfile(name)
			// An unreadable client id still leaves a credential to remove; its own
			// record names the client it was issued to.
			clientID, _, _ := config.MCPClientID(p.BaseURL)
			mcp := auth.NewMCPManager(p.BaseURL, httpClient, config.ConfigDir(), clientID)
			mcp.UseProfile(name)
			for _, mgr := range []*auth.Manager{cli, mcp} {
				revoked, err := revokeAndForget(cmd, mgr, name, p.BaseURL)
				if err != nil {
					// The profile stays, so its credential can still be reached to retry.
					arg := name
					if !config.ValidProfileName(name) {
						arg = shellQuoted(name)
					}
					// A storage error keeps its own class and hint, a refused keyring's.
					e := *apierr.AsError(auth.CredentialStorageError(fmt.Sprintf("could not delete credentials for profile %q", name), err))
					if e.Hint == "" {
						e.Hint = "Run hey doctor -P " + arg + " to check the credential store, then delete the profile again"
					}
					return &e
				}
				if revoked != nil && mgr == cli {
					fields["revoked"] = *revoked
				}
			}
			clearProfileHTTPCache(cmd, name)

			if err := config.UnregisterProfile(name); err != nil {
				return err
			}
			summary := fmt.Sprintf("Deleted profile %q", name)
			return writeMutationLine(cmd, terminal.SanitizeLine(summary)+".", summary, fields)
		},
	}
}

// revokeAndForget revokes a stored OAuth sign-in at HEY, then deletes the credential.
// revoked is nil when there was no sign-in to revoke: none stored, or a pasted
// --token, which hey did not issue and so does not revoke.
//
// Delete takes the store's lock once for the revocation and once for the removal, as
// hey auth logout --mcp does: a refresh another process makes in between leaves its
// rotated grant unrevoked, listed in HEY's Connected Apps, and nothing else.
//
// The refresh token goes only to a server hey would send it to, and only to the one
// that issued it: a base_url edited since the sign-in, or one on plain HTTP, gets
// nothing, and the credential is removed unrevoked with a warning.
func revokeAndForget(cmd *cobra.Command, mgr *auth.Manager, name, baseURL string) (*bool, error) {
	creds, err := mgr.GetStore().Load(mgr.CredentialKey())
	if errors.Is(err, auth.ErrCredentialsNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var revoked *bool
	issued := creds.OAuthType != "token" && (creds.RefreshToken != "" || creds.AccessToken != "")
	if issued && !revocableAt(baseURL, creds.TokenEndpoint) {
		no := false
		revoked = &no
		fmt.Fprintln(cmd.ErrOrStderr(), terminal.SanitizeLine(fmt.Sprintf("warning: not revoking profile %q's sign-in at %s, which did not issue it or takes no credential; remove it in HEY's Connected Apps", name, baseURL)))
	} else if issued {
		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
		revokeErr := mgr.Revoke(ctx)
		cancel()
		ok := revokeErr == nil
		revoked = &ok
		if revokeErr != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), terminal.SanitizeLine(fmt.Sprintf("warning: couldn't revoke profile %q's sign-in at HEY (%v); remove it in HEY's Connected Apps", name, revokeErr)))
		}
	}
	return revoked, mgr.Logout()
}

// revocableAt reports whether a profile's server may take its refresh token back: a
// server hey sends credentials to, and the one whose token endpoint issued it.
func revocableAt(baseURL, tokenEndpoint string) bool {
	if config.ValidateBaseURL(baseURL) != nil {
		return false
	}
	if tokenEndpoint == "" {
		// Nothing says which server issued it.
		return false
	}
	profileOrigin, err := config.Origin(baseURL)
	if err != nil {
		return false
	}
	issuer, err := config.Origin(tokenEndpoint)
	return err == nil && issuer == profileOrigin
}

func newProfileSetDefaultCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set-default <name>",
		Short: "Set the default profile",
		Long:  "Set which profile is used when no --profile flag is specified.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := cfg.Profiles[name]; !ok {
				return apierr.ErrUsage(fmt.Sprintf("Profile %q not found", name))
			}
			if err := config.SetDefaultProfile(name); err != nil {
				return err
			}
			summary := fmt.Sprintf("Default profile set to %q", name)
			return writeMutationLine(cmd, terminal.SanitizeLine(summary)+".", summary,
				map[string]any{"name": name, "status": "set_default"})
		},
	}
}

// profileAuthenticated reports whether a profile holds a usable stored credential.
func profileAuthenticated(name string) bool {
	creds, err := authMgr.GetStore().Load(auth.ProfileCredentialKey(name))
	return err == nil && (creds.AccessToken != "" || creds.RefreshToken != "")
}

// clearProfileHTTPCache drops a profile's response cache: a new sign-in, or none,
// must not revalidate what the last one fetched.
func clearProfileHTTPCache(cmd *cobra.Command, name string) {
	if dir := profileHTTPCacheDir(name); dir != "" {
		clearHTTPCacheDir(cmd.ErrOrStderr(), dir)
	}
}

// localEntrySetsProfile reports whether a repository config's entry for name is one the
// loader reads as a profile: an entry with no base_url is skipped there.
func localEntrySetsProfile(local *config.LocalConfig, name string) bool {
	entry, _ := local.Profiles[name].(map[string]any)
	baseURL, _ := entry["base_url"].(string)
	return baseURL != ""
}
