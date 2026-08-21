package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/basecamp/hey-cli/internal/version"
)

const (
	// ClaudeMarketplaceSource is the marketplace repository carrying the hey plugin.
	ClaudeMarketplaceSource = "basecamp/claude-plugins"
	// ClaudeMarketplaceName is the marketplace name as it appears in plugin keys.
	ClaudeMarketplaceName = "37signals"
	// ClaudePluginName is the plugin identifier to install.
	ClaudePluginName = "hey"
	// ClaudeExpectedPluginKey is the fully-qualified key for a correctly installed plugin.
	ClaudeExpectedPluginKey = ClaudePluginName + "@" + ClaudeMarketplaceName
)

func init() {
	RegisterAgent(AgentInfo{
		Name:   "Claude Code",
		ID:     "claude",
		Detect: DetectClaude,
		Checks: claudeChecks,
		Diagnostics: func(_ context.Context) []*StatusCheck {
			return append(claudeChecks(), CheckClaudePluginVersion())
		},
	})
}

func claudeChecks() []*StatusCheck {
	checks := []*StatusCheck{CheckClaudePlugin()}
	// Only check the skill link if ~/.claude exists (i.e. Claude is dir-detected)
	home, err := os.UserHomeDir()
	if err == nil {
		if info, statErr := os.Stat(filepath.Join(home, ".claude")); statErr == nil && info.IsDir() {
			checks = append(checks, CheckClaudeSkillLink())
		}
	}
	return checks
}

// DetectClaude returns true if Claude Code is installed.
// Checks ~/.claude/ directory first, then falls back to binary on PATH.
func DetectClaude() bool {
	home, err := os.UserHomeDir()
	if err == nil {
		info, statErr := os.Stat(filepath.Join(filepath.Clean(home), ".claude"))
		if statErr == nil && info.IsDir() {
			return true
		}
	}
	return FindClaudeBinary() != ""
}

// FindClaudeBinary returns the path to the claude binary, or "" if not found.
func FindClaudeBinary() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	candidate := filepath.Join(filepath.Clean(home), ".local", "bin", "claude")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// CheckClaudePlugin checks whether the hey plugin is installed in Claude Code.
func CheckClaudePlugin() *StatusCheck {
	home, err := os.UserHomeDir()
	if err != nil {
		return &StatusCheck{
			Name:    "Claude Code Plugin",
			Status:  "warn",
			Message: "Cannot determine home directory",
		}
	}

	pluginsPath := filepath.Join(filepath.Clean(home), ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(pluginsPath) //nolint:gosec // G304: trusted path
	if err != nil {
		if os.IsNotExist(err) {
			return &StatusCheck{
				Name:    "Claude Code Plugin",
				Status:  "fail",
				Message: "Plugin not installed",
				Hint:    "Run: hey setup claude",
			}
		}
		return &StatusCheck{
			Name:    "Claude Code Plugin",
			Status:  "warn",
			Message: "Cannot check Claude Code integration",
			Hint:    "Unable to read " + pluginsPath,
		}
	}

	if pluginInstalled(data) {
		return &StatusCheck{
			Name:    "Claude Code Plugin",
			Status:  "pass",
			Message: "Installed",
		}
	}

	return &StatusCheck{
		Name:    "Claude Code Plugin",
		Status:  "fail",
		Message: "Plugin not installed",
		Hint:    "Run: hey setup claude",
	}
}

// CheckClaudeSkillLink checks whether ~/.claude/skills/hey contains a valid SKILL.md.
func CheckClaudeSkillLink() *StatusCheck {
	home, err := os.UserHomeDir()
	if err != nil {
		return &StatusCheck{
			Name:    "Claude Code Skill",
			Status:  "warn",
			Message: "Cannot determine home directory",
		}
	}

	skillDir := filepath.Join(filepath.Clean(home), ".claude", "skills", ClaudePluginName)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		if os.IsNotExist(err) {
			return &StatusCheck{
				Name:    "Claude Code Skill",
				Status:  "fail",
				Message: "Skill not linked",
				Hint:    "Run: hey setup claude",
			}
		}
		return &StatusCheck{
			Name:    "Claude Code Skill",
			Status:  "warn",
			Message: "Cannot check skill link",
			Hint:    "Unable to stat " + skillPath,
		}
	}
	// Presence is not health: the file must be a regular file (a symlinked
	// SKILL.md points somewhere never inspected)...
	if !RegularSkillFile(skillPath) {
		return &StatusCheck{
			Name:    "Claude Code Skill",
			Status:  "fail",
			Message: "SKILL.md at " + skillDir + " is not a regular file",
			Hint:    "Move it aside, then run: hey setup claude",
		}
	}
	// ...written by hey-cli — anything else is somebody's work occupying
	// the path, not a connected integration.
	if !SkillDirOwned(skillDir) {
		return &StatusCheck{
			Name:    "Claude Code Skill",
			Status:  "fail",
			Message: "A skill not written by hey-cli occupies " + skillDir,
			Hint:    "Move it aside, then run: hey setup claude",
		}
	}

	return &StatusCheck{
		Name:    "Claude Code Skill",
		Status:  "pass",
		Message: "Linked",
	}
}

// AutoUpdateHint is the user-facing instruction for enabling plugin auto-update.
const AutoUpdateHint = "In Claude Code: /plugins → Marketplaces → 37signals → Enable auto-update"

// CheckClaudePluginVersion compares the installed plugin version against the
// running CLI version. Returns a warn check when they differ.
func CheckClaudePluginVersion() *StatusCheck {
	installed := InstalledPluginVersion()
	if installed == "" {
		// Can't determine version — don't nag.
		return &StatusCheck{
			Name:    "Claude Code Plugin Version",
			Status:  "pass",
			Message: "Version not tracked",
		}
	}

	if version.IsDev() {
		return &StatusCheck{
			Name:    "Claude Code Plugin Version",
			Status:  "pass",
			Message: fmt.Sprintf("Installed (%s, dev build)", installed),
		}
	}

	if installed == version.Version {
		return &StatusCheck{
			Name:    "Claude Code Plugin Version",
			Status:  "pass",
			Message: fmt.Sprintf("Up to date (%s)", installed),
		}
	}

	return &StatusCheck{
		Name:    "Claude Code Plugin Version",
		Status:  "warn",
		Message: fmt.Sprintf("Mismatched (plugin %s, CLI %s)", installed, version.Version),
		Hint:    AutoUpdateHint,
	}
}

// InstalledPluginVersion reads the installed plugin version from
// ~/.claude/plugins/installed_plugins.json. Returns "" if unreadable.
func InstalledPluginVersion() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(filepath.Clean(home), ".claude", "plugins", "installed_plugins.json")) //nolint:gosec // G304: trusted path
	if err != nil {
		return ""
	}
	return installedPluginVersion(data)
}

// installedPluginVersion extracts the hey plugin's version from
// installed_plugins.json data. Handles v2, v1 flat map, and array formats.
func installedPluginVersion(data []byte) string {
	// v2 format: {"version": 2, "plugins": {"hey@37signals": [{"version": "1.0.0", ...}]}}
	var pluginMap map[string]any
	if err := json.Unmarshal(data, &pluginMap); err == nil {
		if inner, ok := pluginMap["plugins"].(map[string]any); ok {
			for key, val := range inner {
				if !matchesPluginKey(key) {
					continue
				}
				if arr, ok := val.([]any); ok {
					for _, entry := range arr {
						if obj, ok := entry.(map[string]any); ok {
							if v, ok := obj["version"].(string); ok {
								return v
							}
						}
					}
				}
			}
		}

		// v1 flat map: {"hey@37signals": {"version": "1.0.0"}}
		for key, val := range pluginMap {
			if !matchesPluginKey(key) {
				continue
			}
			if obj, ok := val.(map[string]any); ok {
				if v, ok := obj["version"].(string); ok {
					return v
				}
			}
		}
	}

	// Array format: [{"name": "hey@37signals", "version": "1.0.0", ...}]
	var plugins []map[string]any
	if err := json.Unmarshal(data, &plugins); err == nil {
		for _, p := range plugins {
			if matchesHeyPlugin(p) {
				if v, ok := p["version"].(string); ok {
					return v
				}
			}
		}
	}

	return ""
}

// pluginInstalled checks if the hey plugin appears as installed.
// Handles multiple possible JSON schemas without panicking.
func pluginInstalled(data []byte) bool {
	// Try as array of objects
	var plugins []map[string]any
	if err := json.Unmarshal(data, &plugins); err == nil {
		for _, p := range plugins {
			if matchesHeyPlugin(p) {
				return true
			}
		}
		return false
	}

	// Try as map (key = plugin identifier, or v2 envelope with "plugins" key)
	var pluginMap map[string]any
	if err := json.Unmarshal(data, &pluginMap); err == nil {
		// v2 format: {"version": 2, "plugins": {"hey@37signals": [...]}}
		if inner, ok := pluginMap["plugins"].(map[string]any); ok {
			for key := range inner {
				if matchesPluginKey(key) {
					return true
				}
			}
			return false
		}
		// v1 flat map: {"hey@37signals": {...}}
		for key := range pluginMap {
			if matchesPluginKey(key) {
				return true
			}
		}
		return false
	}

	// Fallback: raw string search for the fully-qualified key. The bare name
	// is deliberately not matched here — "hey" is far too generic a string to
	// prove anything about an unparseable schema.
	return strings.Contains(string(data), `"`+ClaudeExpectedPluginKey+`"`)
}

func matchesHeyPlugin(p map[string]any) bool {
	for _, field := range []string{"name", "package", "id"} {
		if s, ok := p[field].(string); ok && matchesPluginKey(s) {
			return true
		}
	}
	return false
}

// matchesPluginKey reports whether the key identifies a correctly installed
// hey plugin. Unlike basecamp-cli there is no bare legacy key: hey only ever
// shipped the marketplace-qualified "hey@37signals".
func matchesPluginKey(key string) bool {
	return key == ClaudeExpectedPluginKey
}
