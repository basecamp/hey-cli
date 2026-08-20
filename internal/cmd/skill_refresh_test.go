package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/skills"
)

func refreshFixture(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", "")
	return home
}

func installStaleSkill(t *testing.T, home string) string {
	t.Helper()
	skillDir := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("# stale skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillPath
}

func TestRefreshSkillsSkipsDevBuilds(t *testing.T) {
	refreshFixture(t)
	stubVersion(t, "dev")
	if refreshSkillsIfVersionChanged() {
		t.Error("dev builds must never refresh skills")
	}
}

func TestRefreshSkillsUpdatesInstalledCopiesOnce(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "1.2.3")
	skillPath := installStaleSkill(t, home)

	// A Codex copy must be refreshed too.
	codexSkill := filepath.Join(home, ".codex", "skills", "hey", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(codexSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexSkill, []byte("# stale skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !refreshSkillsIfVersionChanged() {
		t.Fatal("first run on a new version should refresh")
	}

	embedded, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{skillPath, codexSkill} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(embedded) {
			t.Errorf("%s was not refreshed", path)
		}
	}

	stamp, err := os.ReadFile(filepath.Join(filepath.Dir(skillPath), installedVersionFile))
	if err != nil || strings.TrimSpace(string(stamp)) != "1.2.3" {
		t.Errorf("installed version stamp = %q, %v", stamp, err)
	}

	sentinel, err := os.ReadFile(filepath.Join(home, ".config", "hey-cli", ".last-run-version"))
	if err != nil || strings.TrimSpace(string(sentinel)) != "1.2.3" {
		t.Errorf("sentinel = %q, %v", sentinel, err)
	}

	if refreshSkillsIfVersionChanged() {
		t.Error("second run on the same version must be a no-op")
	}
}

func TestRefreshSkillsWithoutInstallNeverInstalls(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "1.2.3")

	if refreshSkillsIfVersionChanged() {
		t.Error("nothing installed, nothing to refresh")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "hey", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("refresh must never install the skill")
	}
	// The sentinel still advances so every future run doesn't rescan.
	if _, err := os.Stat(filepath.Join(home, ".config", "hey-cli", ".last-run-version")); err != nil {
		t.Errorf("sentinel not written: %v", err)
	}
}

func TestRefreshSkillsRepairsBrokenClaudeLink(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "1.2.3")
	installStaleSkill(t, home)

	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(claudeSkills, "hey")
	if err := os.Symlink("does-not-exist", linkPath); err != nil {
		t.Fatal(err)
	}

	if !refreshSkillsIfVersionChanged() {
		t.Fatal("refresh should run")
	}
	if _, err := os.Stat(filepath.Join(linkPath, "SKILL.md")); err != nil {
		t.Errorf("broken Claude link was not repaired: %v", err)
	}
}
