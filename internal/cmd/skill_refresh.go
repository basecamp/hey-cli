package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/version"
	"github.com/basecamp/hey-cli/skills"
)

// maybeRefreshSkills runs after every command: when the CLI version changed
// since the last run, installed skill copies are silently brought up to date
// so agents never read instructions for a binary that no longer matches.
// The notice is stderr-only and suppressed for machine output.
func maybeRefreshSkills(cmd *cobra.Command) {
	if !refreshSkillsIfVersionChanged() {
		return
	}
	if machineReadableOutput(cmd) {
		return
	}
	fmt.Fprintf(os.Stderr, "Agent skill updated to match CLI %s\n", version.Version)

	// One-time hint: a plugin/CLI version mismatch right after an upgrade
	// means auto-update is off in Claude Code.
	if pv := harness.InstalledPluginVersion(); pv != "" && pv != version.Version && !version.IsDev() {
		fmt.Fprintf(os.Stderr, "hey plugin version mismatch (plugin %s, CLI %s) — %s\n", pv, version.Version, harness.AutoUpdateHint)
	}
}

// refreshSkillsIfVersionChanged checks the CLI version sentinel and refreshes
// installed skills when the version changed. Reports whether a refresh ran.
func refreshSkillsIfVersionChanged() bool {
	if version.IsDev() {
		return false
	}

	sentinelPath := filepath.Join(config.ConfigDir(), ".last-run-version")

	data, err := os.ReadFile(sentinelPath) // #nosec G304 -- fixed path under the user config dir
	if err == nil && strings.TrimSpace(string(data)) == version.Version {
		return false
	}

	refreshed := refreshInstalledSkills()

	// Repair the Claude symlink if broken (e.g. baseline dir was recreated).
	if harness.DetectClaude() {
		repairClaudeSkillLink()
	}

	// Update the sentinel only when no refresh was needed or it succeeded.
	// On transient failure, leave it stale so the next run retries.
	if !baselineSkillInstalled() || refreshed {
		// 0o700: ConfigDir can hold credentials.json; keep it owner-only.
		_ = os.MkdirAll(filepath.Dir(sentinelPath), 0o700)
		_ = os.WriteFile(sentinelPath, []byte(version.Version), 0o644) // #nosec G306 -- not a secret
	}

	return refreshed
}

// skillRefreshLocations lists every absolute path an agent reads the hey
// skill from. Only existing files are refreshed — refresh never installs.
func skillRefreshLocations() []string {
	var locations []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		locations = append(locations,
			filepath.Join(home, ".agents", "skills", "hey", skillFilename),
			filepath.Join(home, ".claude", "skills", "hey", skillFilename),
		)
	}
	if codexPath := harness.CodexSkillPath(); codexPath != "" {
		locations = append(locations, codexPath)
	}
	return locations
}

func refreshInstalledSkills() bool {
	embedded, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		return false
	}

	updated, failed := 0, 0
	for _, location := range skillRefreshLocations() {
		if _, statErr := os.Stat(location); statErr != nil {
			if !os.IsNotExist(statErr) {
				failed++ // permission or IO error on a known location
			}
			continue
		}
		if writeErr := os.WriteFile(location, embedded, 0o644); writeErr == nil { // #nosec G306 -- installed skills are intentionally user-readable
			updated++
		} else {
			failed++
		}
	}

	// Stamp the installed version in the baseline directory only on full success.
	if failed == 0 && updated > 0 {
		if home, err := os.UserHomeDir(); err == nil {
			stamp := filepath.Join(home, ".agents", "skills", "hey", installedVersionFile)
			_ = os.WriteFile(stamp, []byte(version.Version), 0o644) // #nosec G306 -- not a secret
		}
	}

	return updated > 0 && failed == 0
}

// repairClaudeSkillLink repairs a broken symlink at ~/.claude/skills/hey.
// If the path is a directory (copy fallback), the file refresh handled it.
func repairClaudeSkillLink() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	symlinkPath := filepath.Join(home, ".claude", "skills", harness.ClaudePluginName)
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		return // doesn't exist, nothing to repair
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return // not a symlink (copy fallback), file refresh handled it
	}
	if _, statErr := os.Stat(symlinkPath); statErr == nil {
		return // symlink is healthy
	}

	_, _ = linkSkillToClaude()
}
