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
		// hey has no Codex plugin yet (no .codex-plugin manifest ships in
		// this repo), so Codex health is skill-presence only. When a native
		// plugin lands, this grows the plugin/version checks basecamp-cli has.
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

// CodexSkillPath returns where Codex reads the hey skill from.
func CodexSkillPath() string {
	codexHome := CodexHome()
	if codexHome == "" {
		return ""
	}
	return filepath.Join(codexHome, "skills", "hey", "SKILL.md")
}

// CheckCodexSkill checks whether the hey skill is installed for Codex.
func CheckCodexSkill() *StatusCheck {
	skillPath := CodexSkillPath()
	if skillPath == "" {
		return &StatusCheck{
			Name:    "Codex Skill",
			Status:  "warn",
			Message: "Cannot determine Codex home directory",
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
	return &StatusCheck{
		Name:    "Codex Skill",
		Status:  "pass",
		Message: "Installed",
	}
}
