package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
)

type localConfigTrustChoice int

const (
	localConfigTrustCancel localConfigTrustChoice = iota
	localConfigTrustOnce
	localConfigTrustAlways
)

var askLocalConfigTrust = promptForLocalConfigTrust

func ensureLocalConfigTrusted(cmd *cobra.Command) error {
	local := cfg.UntrustedLocalConfig()
	if local == nil || !commandUsesRuntimeConfig(cmd) {
		return nil
	}
	if machineReadableOutput(cmd) || !interactiveStdio() {
		return untrustedLocalConfigError(local)
	}

	choice, err := askLocalConfigTrust(cmd, local)
	if err != nil {
		return err
	}
	switch choice {
	case localConfigTrustOnce:
		return nil
	case localConfigTrustAlways:
		if err := cfg.TrustLocalConfig(); err != nil {
			return err
		}
		return nil
	default:
		return untrustedLocalConfigError(local)
	}
}

// commandIgnoresLocalConfig reports whether a command reads only the global and
// environment configuration, so a repository-local .hey/config.json is never
// even parsed for it. The bar poller runs from the shell's working directory,
// wherever that happens to be: a local config must neither redirect it to
// another server nor fail it (trust gate or malformed file) — the indicator
// has to stay dark rather than error. setup omarchy only edits fixed desktop
// paths and must not be blocked by a checkout's config either.
func commandIgnoresLocalConfig(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return false
	}
	return parts[1] == "omarchy" || (len(parts) >= 3 && parts[1] == "setup" && parts[2] == "omarchy")
}

// commandUsesRuntimeConfig reports whether a command reads the effective
// server or account, which is what trusting a local config approves. upgrade
// and version talk only to GitHub and the local install, so an untrusted
// checkout must not block them.
func commandUsesRuntimeConfig(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return true
	}
	switch parts[1] {
	case "commands", "completion", "config", "skill", "upgrade", "version":
		return false
	case "setup":
		// `hey setup` itself signs in against the effective server, but its
		// subcommands (agents, claude, codex) only touch local agent files.
		// The installer's non-TTY handoff runs `setup agents` from whatever
		// directory the user piped curl in — possibly a repository with an
		// untrusted .hey/config.json — and must not be blocked by it.
		return len(parts) == 2
	default:
		return true
	}
}

func machineReadableOutput(cmd *cobra.Command) bool {
	return jsonFlag || quietFlag || idsOnly || countFlag || markdownF || agentFlag || cmd.Flags().Changed("jq")
}

func promptForLocalConfigTrust(cmd *cobra.Command, local *config.LocalConfig) (localConfigTrustChoice, error) {
	w := cmd.ErrOrStderr()
	fmt.Fprintln(w, "This repository contains local HEY configuration:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Configuration:       %s\n", terminalSafeText(local.Path))
	fmt.Fprintf(w, "  Local server:        %s\n", localTrustDisplayValue(local.BaseURL))
	fmt.Fprintf(w, "  Local mail account:  %s\n", localTrustDisplayValue(local.AccountID))
	fmt.Fprintf(w, "  Effective server:    %s\n", terminalSafeText(cfg.BaseURL))
	fmt.Fprintf(w, "  Effective account:   %s\n", terminalSafeText(cfg.AccountID))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Always trust approves the exact local values above for this effective server origin.")
	fmt.Fprintln(w, "Commands run here may send credentials to that server and read or send mail using that account.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  [1] Use once")
	fmt.Fprintln(w, "  [2] Always trust these settings")
	fmt.Fprintln(w, "  [3] Cancel")
	fmt.Fprint(w, "Selection [3]: ")

	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return localConfigTrustCancel, fmt.Errorf("could not read local configuration trust choice: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "1", "once", "use once":
		return localConfigTrustOnce, nil
	case "2", "always", "trust", "always trust":
		return localConfigTrustAlways, nil
	default:
		return localConfigTrustCancel, nil
	}
}

func localTrustDisplayValue(value string) string {
	if value == "" {
		return "(not set)"
	}
	return terminalSafeText(value)
}

func untrustedLocalConfigError(local *config.LocalConfig) error {
	path := terminalSafeText(local.Path)
	return output.ErrUsageHint(
		fmt.Sprintf("local HEY configuration is not trusted: %s", path),
		"Review it, then run `hey config trust-local` from that directory.",
	)
}
