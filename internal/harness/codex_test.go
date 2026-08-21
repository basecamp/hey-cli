package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectCodexByHomeDirectory(t *testing.T) {
	home := tempHome(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")

	if DetectCodex() {
		t.Error("no ~/.codex and no binary should not detect Codex")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !DetectCodex() {
		t.Error("~/.codex directory should detect Codex")
	}
}

func TestCodexHomeHonorsEnvOverride(t *testing.T) {
	home := tempHome(t)

	t.Setenv("CODEX_HOME", "")
	if got, want := CodexHome(), filepath.Join(home, ".codex"); got != want {
		t.Errorf("CodexHome() = %q, want %q", got, want)
	}

	override := t.TempDir()
	t.Setenv("CODEX_HOME", override)
	if got := CodexHome(); got != override {
		t.Errorf("CodexHome() = %q, want %q", got, override)
	}
	if got, want := CodexSkillPath(), filepath.Join(override, "skills", "hey", "SKILL.md"); got != want {
		t.Errorf("CodexSkillPath() = %q, want %q", got, want)
	}
}

func TestCheckCodexSkill(t *testing.T) {
	home := tempHome(t)
	t.Setenv("CODEX_HOME", "")

	check := CheckCodexSkill()
	if check.Status != "fail" || check.Hint != "Run: hey setup codex" {
		t.Errorf("missing skill: %+v", check)
	}

	skillDir := filepath.Join(home, ".codex", "skills", "hey")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# hey"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Present but unmarked is somebody else's skill occupying the path —
	// never reported as a connected integration.
	if check := CheckCodexSkill(); check.Status != "fail" || check.Hint != "Move it aside, then run: hey setup codex" {
		t.Errorf("unmanaged skill: %+v", check)
	}

	if err := os.WriteFile(filepath.Join(skillDir, SkillOwnershipMarker), []byte("hey-cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	if check := CheckCodexSkill(); check.Status != "pass" {
		t.Errorf("managed skill: %+v", check)
	}
}
