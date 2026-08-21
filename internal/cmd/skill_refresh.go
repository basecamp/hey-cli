package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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
	// Machine consumers get no stderr chatter — whether they asked with a
	// flag or got JSON automatically because stdout is not a terminal.
	if machineReadableOutput(cmd) || (writer != nil && !writer.IsStyled()) {
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

	configDir := config.ConfigDir()
	if configDir == "" {
		return false // no home to keep state in; never drop a sentinel in the cwd
	}
	sentinelPath := filepath.Join(configDir, ".last-run-version")

	// The sentinel records the active Codex home alongside the version:
	// skill locations depend on CODEX_HOME, so switching homes mid-release
	// triggers one rescan instead of leaving the other home's marked skill
	// stale until the next release.
	sentinelState := version.Version + "\n" + harness.CodexHome() + "\n"
	data, err := os.ReadFile(sentinelPath) // #nosec G304 -- fixed path under the user config dir
	if err == nil && string(data) == sentinelState {
		return false
	}

	updated, failed := refreshInstalledSkills()

	// Advance the sentinel unless something failed: nothing owned to refresh
	// and a fully successful refresh both mean this version is done. On
	// transient failure, leave it stale so the next run retries.
	if failed == 0 {
		// 0o700: ConfigDir can hold credentials.json; keep it owner-only.
		// writeSkillFile refuses to follow a symlink planted at the sentinel
		// path, the same rule as every other file this feature writes.
		_ = os.MkdirAll(filepath.Dir(sentinelPath), 0o700)
		_ = writeSkillFile(sentinelPath, []byte(sentinelState))
	}

	return updated > 0 && failed == 0
}

// skillRefreshLocations lists every absolute path an agent reads the hey
// skill from. Only existing files in directories hey-cli owns (per the
// ownership marker) are refreshed — refresh never installs, and never
// rewrites a skill it cannot prove it wrote.
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

func refreshInstalledSkills() (updated, failed int) {
	embedded, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		return 0, 1
	}

	for _, location := range skillRefreshLocations() {
		info, statErr := os.Lstat(location)
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				failed++ // permission or IO error on a known location
			}
			continue
		}
		// Never write through a symlink — neither a linked SKILL.md nor a
		// linked directory (our own Claude link resolves to the baseline,
		// which is refreshed as itself; a user's link resolves to somewhere
		// we never inspected).
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || isSymlink(filepath.Dir(location)) {
			continue
		}
		// Ownership gate: a SKILL.md in an unmarked directory is somebody
		// else's work — a hand-authored skill that happens to share the
		// name — and is left exactly as found.
		if !ownedSkillDir(filepath.Dir(location)) {
			continue
		}
		if writeErr := os.WriteFile(location, embedded, 0o644); writeErr == nil { // #nosec G306 -- installed skills are intentionally user-readable
			updated++
		} else {
			failed++
		}
	}

	// Stamp the installed version in the baseline directory only on full
	// success, and only when that directory is ours to stamp.
	if failed == 0 && updated > 0 {
		if home, err := os.UserHomeDir(); err == nil {
			baselineDir := filepath.Join(home, ".agents", "skills", "hey")
			if ownedSkillDir(baselineDir) {
				_ = writeSkillFile(filepath.Join(baselineDir, installedVersionFile), []byte(version.Version))
			}
		}
	}

	return updated, failed
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
