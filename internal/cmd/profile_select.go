package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// profileFlag is -P/--profile.
var profileFlag string

// resolveProfile determines which profile to use, ported from basecamp-cli
// (internal/cli/root.go at 48b01aaf) with its words:
//
//  1. --profile / -P flag
//  2. HEY_PROFILE env var
//  3. default_profile in config
//  4. Single profile → auto-use
//  5. Multiple profiles → interactive picker (if TTY)
//  6. No profiles → empty string (use top-level config values)
//
// mayLeaveUnselected lets a command that needs no server run with no profile where
// step 6 would refuse it: the profile commands, so `hey profile set-default` can answer
// the refusal it names, and the commands that read no server at all.
func resolveProfile(cmd *cobra.Command, c *config.Config, mayLeaveUnselected bool) (string, config.Source, error) {
	// 1. --profile flag
	if profileFlag != "" {
		if _, ok := c.Profiles[profileFlag]; ok {
			return profileFlag, config.SourceFlag, nil
		}
		if len(c.Profiles) == 0 {
			return "", "", apierr.ErrUsage(fmt.Sprintf("profile %q specified via --profile but no profiles are configured; create one with: hey profile create", profileFlag))
		}
		return "", "", apierr.ErrUsage(fmt.Sprintf("unknown profile %q (available: %s)", profileFlag, strings.Join(c.ProfileNames(), ", ")))
	}

	// 2. HEY_PROFILE env var
	if profile := os.Getenv("HEY_PROFILE"); profile != "" {
		if _, ok := c.Profiles[profile]; ok {
			return profile, config.SourceEnv, nil
		}
		if len(c.Profiles) == 0 {
			return "", "", apierr.ErrUsage(fmt.Sprintf("profile %q specified via HEY_PROFILE but no profiles are configured; create one with: hey profile create", profile))
		}
		return "", "", apierr.ErrUsage(fmt.Sprintf("unknown profile %q from HEY_PROFILE (available: %s)", profile, strings.Join(c.ProfileNames(), ", ")))
	}

	// No profiles configured - use top-level config
	if len(c.Profiles) == 0 {
		return "", "", nil
	}

	// 3. default_profile in config
	if c.DefaultProfile != "" {
		if _, ok := c.Profiles[c.DefaultProfile]; ok {
			return c.DefaultProfile, c.SourceOf("default_profile"), nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: default_profile %q not found in configured profiles\n", terminal.SanitizeLine(c.DefaultProfile))
	}

	// 4. Single profile → auto-use
	if names := c.ProfileNames(); len(names) == 1 {
		return names[0], config.SourceDefault, nil
	}

	// 5. Multiple profiles → interactive picker (if TTY)
	if interactiveStdio() && !machineReadableOutput(cmd) {
		if name, err := promptForProfile(cmd, c); err == nil {
			return name, config.SourcePrompt, nil
		}
	}

	if mayLeaveUnselected {
		return "", "", nil
	}

	// 6. Multiple profiles but non-interactive — require explicit selection
	return "", "", apierr.ErrUsage(fmt.Sprintf("multiple profiles configured but none selected; use --profile or set default_profile (available: %s)", strings.Join(c.ProfileNames(), ", ")))
}

// commandMayRunWithoutProfile reports whether a command may run with no profile
// selected among several: the profile commands, which manage them, and the commands
// that read neither the server nor the account, and the config commands but `config
// show`, which reports what a run uses.
func commandMayRunWithoutProfile(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return false
	}
	if isProfileCommand(cmd) {
		return true
	}
	if parts[1] == "config" {
		// trust-local, set and the rest manage the files a profile comes from.
		return len(parts) >= 3 && parts[2] != "show"
	}
	return commandIgnoresLocalConfig(cmd) || !commandUsesRuntimeConfig(cmd)
}

// promptForProfile asks which profile to use, on stderr so stdout stays data. A seam
// so tests can answer it.
var promptForProfile = func(cmd *cobra.Command, c *config.Config) (string, error) {
	w := cmd.ErrOrStderr()
	names := c.ProfileNames()
	fmt.Fprintln(w, "Choose a profile:")
	for i, name := range names {
		fmt.Fprintf(w, "  [%d] %s  %s\n", i+1, terminal.SanitizeLine(name), terminal.SanitizeLine(c.Profiles[name].BaseURL))
	}
	fmt.Fprint(w, "Selection: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("could not read profile selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if n, convErr := strconv.Atoi(choice); convErr == nil && n >= 1 && n <= len(names) {
		return names[n-1], nil
	}
	if _, ok := c.Profiles[choice]; ok && choice != "" {
		return choice, nil
	}
	return "", apierr.ErrUsage("profile selection canceled")
}

// loginCommandPattern finds the login a hint names.
var loginCommandPattern = regexp.MustCompile(`hey auth login( --mcp)?`)

// addressLoginHint points an error's sign-in remedy at the active profile. Every
// "Run: hey auth login" in the CLI is written for the top-level credential, and a bare
// login under a profile would store the new credential there instead, so the remedy
// is rewritten here, once, rather than at each of the places that raise it.
func addressLoginHint(err error) error {
	if cfg == nil || cfg.ActiveProfile == "" || authMgr == nil {
		return err
	}
	var e *apierr.Error
	if !errors.As(err, &e) || strings.Contains(e.Hint, " -P ") {
		return err
	}
	e.Hint = loginCommandPattern.ReplaceAllStringFunc(e.Hint, func(match string) string {
		if strings.HasSuffix(match, "--mcp") && mcpAuthMgr != nil {
			return mcpAuthMgr.LoginCommand()
		}
		return authMgr.LoginCommand()
	})
	return err
}

// completeProfileNames offers the profiles -P can name here, a trusted repository's
// included.
func completeProfileNames(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	loaded, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []cobra.Completion
	for _, name := range loaded.ProfileNames() {
		if strings.HasPrefix(name, toComplete) {
			completions = append(completions, cobra.CompletionWithDesc(name, loaded.Profiles[name].BaseURL))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// commandReadsNoServer reports the commands that never use a profile, so a broken or
// unchosen one can't stop them: version, upgrade, commands and shell completion, which a
// shell's rc runs at every start. The config commands read none either, but config show
// reports the selected profile.
func commandReadsNoServer(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return false
	}
	return parts[1] == "shell-completion" || (parts[1] != "config" && !commandUsesRuntimeConfig(cmd))
}
