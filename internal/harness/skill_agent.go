package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkillAgent describes a coding agent that reads the shared ~/.agents skill
// directly: hey has no plugin for it, so its whole integration is the
// baseline skill and health is skill presence only. Codex and Grok are both
// this shape and differ only in the five fields below, which is why they are
// rows in a table rather than two files. When one of them grows a native
// plugin, it leaves the table for a registration of its own, the way Claude
// Code has.
type SkillAgent struct {
	Name    string // "Codex"
	ID      string // "codex"; the `hey setup <id>` subcommand and HEY_SETUP_AGENT value
	HomeEnv string // "CODEX_HOME"; overrides HomeDir when set
	HomeDir string // ".codex"; under the user's home directory
	Binary  string // "codex"; the executable's name
}

// Codex is OpenAI's Codex CLI.
var Codex = SkillAgent{Name: "Codex", ID: "codex", HomeEnv: "CODEX_HOME", HomeDir: ".codex", Binary: "codex"}

// Grok is xAI's Grok Build CLI.
var Grok = SkillAgent{Name: "Grok", ID: "grok", HomeEnv: "GROK_HOME", HomeDir: ".grok", Binary: "grok"}

// skillAgents is the registration table: every agent that reads the shared
// skill, in the order they register.
var skillAgents = []SkillAgent{Codex, Grok}

func init() {
	for _, agent := range skillAgents {
		RegisterAgent(agent.agentInfo())
	}
}

// SkillAgents returns every shared-skill agent, in registration order.
func SkillAgents() []SkillAgent {
	return append([]SkillAgent(nil), skillAgents...)
}

func (a SkillAgent) agentInfo() AgentInfo {
	checks := func() []*StatusCheck { return []*StatusCheck{a.CheckSkill()} }
	return AgentInfo{
		Name:        a.Name,
		ID:          a.ID,
		Detect:      a.Detect,
		FindBinary:  a.FindBinary,
		Checks:      checks,
		Diagnostics: func(context.Context) []*StatusCheck { return checks() },
	}
}

// Detect reports whether the agent has a home directory or an executable.
func (a SkillAgent) Detect() bool {
	if info, err := os.Stat(a.Home()); err == nil && info.IsDir() {
		return true
	}
	return a.FindBinary() != ""
}

// FindBinary returns the agent's executable path, or an empty string. It
// looks on PATH first, then where an installer puts the binary when the
// shell has not picked up the PATH change yet: ~/.local/bin, and the agent's
// own home's bin (Grok Build's installers write ~/.grok/bin/grok, or
// $GROK_HOME/bin/grok for the npm package).
func (a SkillAgent) FindBinary() string {
	if path, err := exec.LookPath(a.Binary); err == nil {
		return path
	}
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(filepath.Clean(home), ".local", "bin", a.Binary))
	}
	if agentHome := a.Home(); agentHome != "" {
		candidates = append(candidates, filepath.Join(agentHome, "bin", a.Binary))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// Home returns the agent's home directory: $HomeEnv, or HomeDir under the
// user's home.
func (a SkillAgent) Home() string {
	if home := strings.TrimSpace(os.Getenv(a.HomeEnv)); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(filepath.Clean(home), a.HomeDir)
}

// LegacySkillPath returns the agent-specific path an older hey-cli copied the
// skill to, or an empty string when the agent's home cannot be determined.
// The agent reads AgentSkillPath directly, so a copy here is a duplicate;
// this remains only so hey-cli can migrate and remove copies it wrote.
func (a SkillAgent) LegacySkillPath() string {
	home := a.Home()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "skills", "hey", "SKILL.md")
}

// CheckSkill checks whether the shared hey skill is installed for the agent.
func (a SkillAgent) CheckSkill() *StatusCheck {
	name := a.Name + " Skill"
	setup := "hey setup " + a.ID
	skillPath := AgentSkillPath()
	if skillPath == "" {
		return &StatusCheck{
			Name:    name,
			Status:  "warn",
			Message: "Cannot determine shared Agent Skills directory",
		}
	}
	if _, err := os.Stat(skillPath); err != nil {
		if os.IsNotExist(err) {
			return &StatusCheck{
				Name:    name,
				Status:  "fail",
				Message: "Skill not installed",
				Hint:    "Run: " + setup,
			}
		}
		return &StatusCheck{
			Name:    name,
			Status:  "warn",
			Message: "Cannot check " + a.Name + " skill",
			Hint:    "Unable to stat " + skillPath,
		}
	}
	// Presence is not health: the file must be a regular file (a symlinked
	// SKILL.md points somewhere never inspected)...
	if !RegularSkillFile(skillPath) {
		return &StatusCheck{
			Name:    name,
			Status:  "fail",
			Message: "SKILL.md at " + filepath.Dir(skillPath) + " is not a regular file",
			Hint:    "Move it aside, then run: " + setup,
		}
	}
	// ...written by hey-cli — anything else is somebody's work occupying
	// the path, not a connected integration.
	if skillDir := filepath.Dir(skillPath); !SkillDirOwned(skillDir) {
		return &StatusCheck{
			Name:    name,
			Status:  "fail",
			Message: "A skill not written by hey-cli occupies " + skillDir,
			Hint:    "Move it aside, then run: " + setup,
		}
	}
	legacyPath := a.LegacySkillPath()
	if !SameFile(skillPath, legacyPath) && RegularSkillFile(legacyPath) && SkillDirOwned(filepath.Dir(legacyPath)) {
		return &StatusCheck{
			Name:    name,
			Status:  "fail",
			Message: "Redundant managed skill installed at " + filepath.Dir(legacyPath),
			Hint:    "Run: " + setup,
		}
	}
	return &StatusCheck{
		Name:    name,
		Status:  "pass",
		Message: "Installed",
	}
}
