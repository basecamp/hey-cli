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
	if err != nil || !strings.HasPrefix(string(sentinel), "1.2.3\n") {
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

// Refresh never touches symlinks: a dangling ~/.claude/skills/hey — a user's
// link to a volume that is merely unmounted right now — survives untouched,
// even when a marked baseline exists next to it.
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
	if err != nil || !strings.HasPrefix(string(sentinel), "9.9.9\n") {
		t.Errorf("sentinel = %q, %v", sentinel, err)
	}
}

// Refresh never writes through a symlink, even inside a marked directory: a
// SKILL.md that is itself a link, or a skill directory that is a link, points
// somewhere hey-cli never inspected.
func TestRefreshSkillsNeverWritesThroughSymlinks(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")

	// Marked baseline whose SKILL.md is a link to the user's real file.
	baseline := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.MkdirAll(baseline, 0o755); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMarker(baseline)
	custom := "# the user's real skill\n"
	realSkill := filepath.Join(home, "real-skill.md")
	if err := os.WriteFile(realSkill, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSkill, filepath.Join(baseline, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	// Codex skill directory that is itself a link to a user directory.
	elsewhere := writeSkillFixture(t, filepath.Join(home, "elsewhere"), custom, true)
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(elsewhere), filepath.Join(home, ".codex", "skills", "hey")); err != nil {
		t.Fatal(err)
	}

	if refreshSkillsIfVersionChanged() {
		t.Error("nothing writable: refresh must report no work done")
	}
	for _, path := range []string{realSkill, elsewhere} {
		if got, _ := os.ReadFile(path); string(got) != custom {
			t.Errorf("%s was written through a symlink", path)
		}
	}
}

// With no config directory at all there is nowhere to keep the sentinel, and
// it must not land in the current directory.
func TestRefreshSkillsSkipsWithoutConfigDir(t *testing.T) {
	stubVersion(t, "9.9.9")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	cwd := t.TempDir()
	t.Chdir(cwd)

	if refreshSkillsIfVersionChanged() {
		t.Error("refresh should not run without a config dir")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".last-run-version")); !os.IsNotExist(err) {
		t.Error("sentinel written into the current directory")
	}
}

// The sentinel tracks the active Codex home: a marked, stale skill in a
// Codex home that was inactive during the first post-upgrade run is
// refreshed as soon as that home becomes active, not at the next release.
func TestRefreshSkillsRescansWhenCodexHomeChanges(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")
	installStaleSkill(t, home)

	homeA := t.TempDir()
	t.Setenv("CODEX_HOME", homeA)
	if !refreshSkillsIfVersionChanged() {
		t.Fatal("first run should refresh")
	}
	if refreshSkillsIfVersionChanged() {
		t.Fatal("same home: second run is a no-op")
	}

	homeB := t.TempDir()
	staleB := writeSkillFixture(t, filepath.Join(homeB, "skills", "hey"), "# stale skill", true)
	t.Setenv("CODEX_HOME", homeB)
	if !refreshSkillsIfVersionChanged() {
		t.Fatal("switching Codex homes should rescan")
	}
	data, err := os.ReadFile(staleB)
	if err != nil || string(data) == "# stale skill" {
		t.Errorf("skill in the newly active Codex home was not refreshed: %v", err)
	}
	if refreshSkillsIfVersionChanged() {
		t.Error("stable again: refresh must be a no-op")
	}
}

// The sentinel gets the same no-follow rule as every other file this feature
// writes: a symlink planted at its path is never truncated.
func TestRefreshSkillsNeverWritesThroughSymlinkedSentinel(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")

	configDir := filepath.Join(home, ".config", "hey-cli")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(home, "precious")
	if err := os.WriteFile(precious, []byte("do not truncate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, filepath.Join(configDir, ".last-run-version")); err != nil {
		t.Fatal(err)
	}

	refreshSkillsIfVersionChanged()
	if got, _ := os.ReadFile(precious); string(got) != "do not truncate" {
		t.Errorf("sentinel write followed a symlink: %q", got)
	}
}

// A refresh whose sentinel cannot be written is not complete: reporting it
// as one would repeat the update notice on every subsequent command.
func TestRefreshSkillsSentinelFailureIsNotCompletion(t *testing.T) {
	home := refreshFixture(t)
	stubVersion(t, "9.9.9")
	installStaleSkill(t, home)

	// The sentinel's parent exists but is unwritable.
	configDir := filepath.Join(home, ".config", "hey-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(configDir, 0o700) })

	if refreshSkillsIfVersionChanged() {
		t.Error("a refresh without a sentinel must not report completion")
	}
}
