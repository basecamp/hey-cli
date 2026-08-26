package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() {
	RegisterAgent(AgentInfo{
		Name:   "Grok",
		ID:     "grok",
		Detect: DetectGrok,
		// Grok discovers the shared ~/.agents skill directly, so health is
		// skill-presence only. When a native plugin lands, this grows the
		// plugin/version checks Claude has.
		Checks: func() []*StatusCheck {
			return []*StatusCheck{CheckGrokSkill()}
		},
		Diagnostics: func(_ context.Context) []*StatusCheck {
			return []*StatusCheck{CheckGrokSkill()}
		},
	})
}

// DetectGrok returns true when Grok has a home directory or executable.
func DetectGrok() bool {
	if info, err := os.Stat(GrokHome()); err == nil && info.IsDir() {
		return true
	}
	return FindGrokBinary() != ""
}

// FindGrokBinary returns the Grok executable path, or an empty string. Grok
// Build's installers put it in $GROK_HOME/bin, which may not be on PATH yet.
func FindGrokBinary() string {
	if path, err := exec.LookPath("grok"); err == nil {
		return path
	}
	if home := GrokHome(); home != "" {
		candidate := filepath.Join(home, "bin", "grok")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	candidate := filepath.Join(filepath.Clean(home), ".local", "bin", "grok")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// GrokHome returns Grok's home directory: $GROK_HOME or ~/.grok.
func GrokHome() string {
	if grokHome := strings.TrimSpace(os.Getenv("GROK_HOME")); grokHome != "" {
		return grokHome
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(filepath.Clean(home), ".grok")
}

// CheckGrokSkill checks whether the shared hey skill is installed for Grok.
func CheckGrokSkill() *StatusCheck {
	skillPath := AgentSkillPath()
	if skillPath == "" {
		return &StatusCheck{
			Name:    "Grok Skill",
			Status:  "warn",
			Message: "Cannot determine shared Agent Skills directory",
		}
	}
	if _, err := os.Stat(skillPath); err != nil {
		if os.IsNotExist(err) {
			return &StatusCheck{
				Name:    "Grok Skill",
				Status:  "fail",
				Message: "Skill not installed",
				Hint:    "Run: hey setup grok",
			}
		}
		return &StatusCheck{
			Name:    "Grok Skill",
			Status:  "warn",
			Message: "Cannot check Grok skill",
			Hint:    "Unable to stat " + skillPath,
		}
	}
	// Presence is not health: the file must be a regular file (a symlinked
	// SKILL.md points somewhere never inspected)...
	if !RegularSkillFile(skillPath) {
		return &StatusCheck{
			Name:    "Grok Skill",
			Status:  "fail",
			Message: "SKILL.md at " + filepath.Dir(skillPath) + " is not a regular file",
			Hint:    "Move it aside, then run: hey setup grok",
		}
	}
	// ...written by hey-cli — anything else is somebody's work occupying
	// the path, not a connected integration.
	if skillDir := filepath.Dir(skillPath); !SkillDirOwned(skillDir) {
		return &StatusCheck{
			Name:    "Grok Skill",
			Status:  "fail",
			Message: "A skill not written by hey-cli occupies " + skillDir,
			Hint:    "Move it aside, then run: hey setup grok",
		}
	}
	return &StatusCheck{
		Name:    "Grok Skill",
		Status:  "pass",
		Message: "Installed",
	}
}
