package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGrokByHomeDirectory(t *testing.T) {
	home := tempHome(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GROK_HOME", "")

	if DetectGrok() {
		t.Error("no ~/.grok and no binary should not detect Grok")
	}
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !DetectGrok() {
		t.Error("~/.grok directory should detect Grok")
	}
}

func TestGrokHomeHonorsEnvOverride(t *testing.T) {
	home := tempHome(t)

	t.Setenv("GROK_HOME", "")
	if got, want := GrokHome(), filepath.Join(home, ".grok"); got != want {
		t.Errorf("GrokHome() = %q, want %q", got, want)
	}

	override := t.TempDir()
	t.Setenv("GROK_HOME", override)
	if got := GrokHome(); got != override {
		t.Errorf("GrokHome() = %q, want %q", got, override)
	}
	if got, want := GrokSkillPath(), filepath.Join(override, "skills", "hey", "SKILL.md"); got != want {
		t.Errorf("GrokSkillPath() = %q, want %q", got, want)
	}
}

func TestCheckGrokSkill(t *testing.T) {
	home := tempHome(t)
	t.Setenv("GROK_HOME", "")

	check := CheckGrokSkill()
	if check.Status != "fail" || check.Hint != "Run: hey setup grok" {
		t.Errorf("missing skill: %+v", check)
	}

	skillDir := filepath.Join(home, ".grok", "skills", "hey")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# hey"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Present but unmarked is somebody else's skill occupying the path —
	// never reported as a connected integration.
	if check := CheckGrokSkill(); check.Status != "fail" || check.Hint != "Move it aside, then run: hey setup grok" {
		t.Errorf("unmanaged skill: %+v", check)
	}

	if err := os.WriteFile(filepath.Join(skillDir, SkillOwnershipMarker), []byte("hey-cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	if check := CheckGrokSkill(); check.Status != "pass" {
		t.Errorf("managed skill: %+v", check)
	}
}
