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
		Name:   "Codex",
		ID:     "codex",
		Detect: DetectCodex,
		// Codex discovers the shared ~/.agents skill directly, so health is
		// skill-presence only. When a native plugin lands, this grows the
		// plugin/version checks basecamp-cli has.
		Checks: func() []*StatusCheck {
			return []*StatusCheck{CheckCodexSkill()}
		},
		Diagnostics: func(_ context.Context) []*StatusCheck {
			return []*StatusCheck{CheckCodexSkill()}
		},
	})
}

// DetectCodex returns true when Codex has a home directory or executable.
func DetectCodex() bool {
	if info, err := os.Stat(CodexHome()); err == nil && info.IsDir() {
		return true
	}
	return FindCodexBinary() != ""
}

// FindCodexBinary returns the Codex executable path, or an empty string.
func FindCodexBinary() string {
	if path, err := exec.LookPath("codex"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	candidate := filepath.Join(filepath.Clean(home), ".local", "bin", "codex")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// CodexHome returns Codex's home directory: $CODEX_HOME or ~/.codex.
func CodexHome() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return codexHome
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(filepath.Clean(home), ".codex")
}

// LegacyCodexSkillPath returns the old Codex-specific skill path. Current Codex
// discovers AgentSkillPath directly; this remains only so hey-cli can safely
// migrate and remove copies written by older releases.
func LegacyCodexSkillPath() string {
	codexHome := CodexHome()
	if codexHome == "" {
		return ""
	}
	return filepath.Join(codexHome, "skills", "hey", "SKILL.md")
}

// CheckCodexSkill checks whether the shared hey skill is installed for Codex.
func CheckCodexSkill() *StatusCheck {
	skillPath := AgentSkillPath()
	if skillPath == "" {
		return &StatusCheck{
			Name:    "Codex Skill",
			Status:  "warn",
			Message: "Cannot determine shared Agent Skills directory",
		}
	}
	if _, err := os.Stat(skillPath); err != nil {
		if os.IsNotExist(err) {
			return &StatusCheck{
				Name:    "Codex Skill",
				Status:  "fail",
				Message: "Skill not installed",
				Hint:    "Run: hey setup codex",
			}
		}
		return &StatusCheck{
			Name:    "Codex Skill",
			Status:  "warn",
			Message: "Cannot check Codex skill",
			Hint:    "Unable to stat " + skillPath,
		}
	}
	// Presence is not health: the file must be a regular file (a symlinked
	// SKILL.md points somewhere never inspected)...
	if !RegularSkillFile(skillPath) {
		return &StatusCheck{
			Name:    "Codex Skill",
			Status:  "fail",
			Message: "SKILL.md at " + filepath.Dir(skillPath) + " is not a regular file",
			Hint:    "Move it aside, then run: hey setup codex",
		}
	}
	// ...written by hey-cli — anything else is somebody's work occupying
	// the path, not a connected integration.
	if skillDir := filepath.Dir(skillPath); !SkillDirOwned(skillDir) {
		return &StatusCheck{
			Name:    "Codex Skill",
			Status:  "fail",
			Message: "A skill not written by hey-cli occupies " + skillDir,
			Hint:    "Move it aside, then run: hey setup codex",
		}
	}
	legacyPath := LegacyCodexSkillPath()
	if !SameFile(skillPath, legacyPath) && RegularSkillFile(legacyPath) && SkillDirOwned(filepath.Dir(legacyPath)) {
		return &StatusCheck{
			Name:    "Codex Skill",
			Status:  "fail",
			Message: "Redundant managed skill installed at " + filepath.Dir(legacyPath),
			Hint:    "Run: hey setup codex",
		}
	}
	return &StatusCheck{
		Name:    "Codex Skill",
		Status:  "pass",
		Message: "Installed",
	}
}
