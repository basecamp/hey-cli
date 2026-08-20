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

// A dangling ~/.claude/skills/hey that hey-cli did not write — a user's link
// to a volume that is merely unmounted right now — must survive the refresh
// untouched, even when a marked baseline exists next to it.
func TestRefreshSkillsPreservesForeignBrokenClaudeLink(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")
	installStaleSkill(t, home)

	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(claudeSkills, "hey")
	foreign := "../../../Volumes/offline/custom-hey-skill"
	if err := os.Symlink(foreign, linkPath); err != nil {
		t.Fatal(err)
	}

	if !refreshSkillsIfVersionChanged() {
		t.Fatal("the marked baseline should still refresh")
	}
	target, err := os.Readlink(linkPath)
	if err != nil || target != foreign {
		t.Errorf("foreign broken link was replaced: target = %q, %v", target, err)
	}
}

// The provenance gate itself: only the canonical target over a marked
// baseline counts as ours.
func TestClaudeSkillLinkIsOurs(t *testing.T) {
	home := refreshFixture(t)
	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(claudeSkills, "hey")
	relink := func(target string) {
		t.Helper()
		_ = os.Remove(linkPath)
		if err := os.Symlink(target, linkPath); err != nil {
			t.Fatal(err)
		}
	}

	relink(claudeSkillLinkTarget)
	if claudeSkillLinkIsOurs(linkPath) {
		t.Error("canonical target over an unmarked baseline is not ours")
	}

	installStaleSkill(t, home) // marks the baseline
	if !claudeSkillLinkIsOurs(linkPath) {
		t.Error("canonical target over a marked baseline is ours")
	}

	relink("../../../Volumes/offline/custom-hey-skill")
	if claudeSkillLinkIsOurs(linkPath) {
		t.Error("a foreign target is never ours, marker or not")
	}

	relink(filepath.Join(home, ".agents", "skills", "hey")) // same place, absolute spelling
	if claudeSkillLinkIsOurs(linkPath) {
		t.Error("only the exact canonical relative target is ours")
	}
}

// Our own canonical link over a marked baseline passes through the refresh
// intact and still resolves afterwards.
func TestRefreshSkillsKeepsCanonicalClaudeLink(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")
	installStaleSkill(t, home)

	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(claudeSkills, "hey")
	if err := os.Symlink(claudeSkillLinkTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	if !refreshSkillsIfVersionChanged() {
		t.Fatal("refresh should run")
	}
	if target, err := os.Readlink(linkPath); err != nil || target != claudeSkillLinkTarget {
		t.Errorf("canonical link disturbed: %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(linkPath, "SKILL.md")); err != nil {
		t.Errorf("canonical link no longer resolves: %v", err)
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
