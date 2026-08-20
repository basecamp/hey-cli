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

// installStaleSkill lays down a hey-cli-written baseline install (marker and
// all) whose content is stale.
func installStaleSkill(t *testing.T, home string) string {
	t.Helper()
	return writeSkillFixture(t, filepath.Join(home, ".agents", "skills", "hey"), "# stale skill", true)
}

// writeSkillFixture writes dir/SKILL.md with the given content, marked as
// hey-cli-managed or not.
func writeSkillFixture(t *testing.T, dir, content string, managed bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if managed {
		writeOwnershipMarker(dir)
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
	codexSkill := writeSkillFixture(t, filepath.Join(home, ".codex", "skills", "hey"), "# stale skill", true)

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

// Refresh must prove ownership before rewriting: a SKILL.md in a directory
// without our marker is somebody else's skill that shares the name, and a new
// release running any command must leave it byte-for-byte alone.
func TestRefreshSkillsPreservesUnmanagedSkills(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")

	custom := "# my own hey skill\n\nhand-authored, not hey-cli's\n"
	locations := []string{
		writeSkillFixture(t, filepath.Join(home, ".agents", "skills", "hey"), custom, false),
		writeSkillFixture(t, filepath.Join(home, ".claude", "skills", "hey"), custom, false),
		writeSkillFixture(t, filepath.Join(home, ".codex", "skills", "hey"), custom, false),
	}

	if refreshSkillsIfVersionChanged() {
		t.Error("nothing owned: refresh must report no work done")
	}
	for _, path := range locations {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != custom {
			t.Errorf("%s was rewritten without ownership", path)
		}
	}
	// The sentinel still advances — leaving foreign skills alone is this
	// version's finished state, not a failure to retry.
	sentinel, err := os.ReadFile(filepath.Join(home, ".config", "hey-cli", ".last-run-version"))
	if err != nil || strings.TrimSpace(string(sentinel)) != "9.9.9" {
		t.Errorf("sentinel = %q, %v", sentinel, err)
	}
}
