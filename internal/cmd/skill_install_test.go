package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

// A stale hey-cli link (canonical target, baseline since removed) is ours to
// replace. A link to anywhere else is the user's — see the refusal test below.
func TestSkillInstallReplacesExistingLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	symlinkDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(symlinkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(symlinkDir, "hey")
	if err := os.Symlink(claudeSkillLinkTarget, symlinkPath); err != nil {
		t.Fatal(err)
	}

	previousWriter := writer
	t.Cleanup(func() { writer = previousWriter })
	var stdout bytes.Buffer
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &stdout})
	if err := runSkillInstall(newSkillInstallCommand(), nil); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("installed Claude skill mode = %v, want symlink", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "hey", "SKILL.md")); err != nil {
		t.Fatalf("installed skill: %v", err)
	}
}

func TestSkillInstallReportsExistingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	symlinkPath := filepath.Join(home, ".claude", "skills", "hey")
	if err := os.MkdirAll(symlinkPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(symlinkPath, "keep"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousWriter := writer
	t.Cleanup(func() { writer = previousWriter })
	writer = output.New(output.Options{Format: output.FormatJSON})
	if err := runSkillInstall(newSkillInstallCommand(), nil); err == nil {
		t.Fatal("install replaced a non-empty skill directory")
	}
	if _, err := os.Stat(filepath.Join(symlinkPath, "keep")); err != nil {
		t.Fatalf("existing skill content changed: %v", err)
	}
}

// Where symlinks are unavailable (Windows without the privilege), the copy
// fallback leaves a real, populated directory at ~/.claude/skills/hey. The
// next install must replace that directory — our own files only — while the
// existing test above pins that unknown user content is still refused.
func TestSkillInstallCopyFallbackIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	origSymlink := makeSkillSymlink
	makeSkillSymlink = func(string, string) error { return errors.New("symlinks unavailable") }
	t.Cleanup(func() { makeSkillSymlink = origSymlink })

	previousWriter := writer
	t.Cleanup(func() { writer = previousWriter })
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &bytes.Buffer{}})

	for run := 1; run <= 2; run++ {
		if err := runSkillInstall(newSkillInstallCommand(), nil); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		copyPath := filepath.Join(home, ".claude", "skills", "hey")
		info, err := os.Lstat(copyPath)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			t.Fatalf("run %d: expected a copied directory, got mode %v", run, info.Mode())
		}
		if _, err := os.Stat(filepath.Join(copyPath, "SKILL.md")); err != nil {
			t.Fatalf("run %d: copied skill missing: %v", run, err)
		}
	}
}

// Shape is not ownership: a user-authored ~/.claude/skills/hey holding only a
// SKILL.md — the exact shape our copy fallback produces, minus the marker —
// must be refused and preserved, not silently replaced by a symlink.
func TestSkillInstallRefusesUnmarkedSkillOnlyDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	skillDir := filepath.Join(home, ".claude", "skills", "hey")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "# my own hey skill"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	previousWriter := writer
	t.Cleanup(func() { writer = previousWriter })
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &bytes.Buffer{}})

	if err := runSkillInstall(newSkillInstallCommand(), nil); err == nil {
		t.Fatal("install replaced a user-authored skill directory")
	}
	info, err := os.Lstat(skillDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("user directory replaced: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil || string(data) != custom {
		t.Fatalf("user skill content changed: %q, %v", data, err)
	}
}

func jsonWriter(t *testing.T) {
	t.Helper()
	previousWriter := writer
	t.Cleanup(func() { writer = previousWriter })
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &bytes.Buffer{}})
}

func agentHome(t *testing.T, dirs ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// Only hey-cli's own canonical symlink may be replaced. A user's symlink to
// their own skill, or a regular file, is refused and left exactly as found.
func TestSkillInstallRefusesForeignLinkOrFile(t *testing.T) {
	for name, plant := range map[string]func(path string) error{
		"foreign symlink": func(path string) error { return os.Symlink("../../my-skills/hey", path) },
		"regular file":    func(path string) error { return os.WriteFile(path, []byte("# mine"), 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			home := agentHome(t, ".claude/skills")
			path := filepath.Join(home, ".claude", "skills", "hey")
			if err := plant(path); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(path)
			jsonWriter(t)

			if err := runSkillInstall(newSkillInstallCommand(), nil); err == nil {
				t.Fatal("install replaced user state")
			}
			after, err := os.Lstat(path)
			if err != nil || after.Mode() != before.Mode() {
				t.Fatalf("user state disturbed: %v, %v", after, err)
			}
			if before.Mode()&os.ModeSymlink != 0 {
				if target, _ := os.Readlink(path); target != "../../my-skills/hey" {
					t.Errorf("symlink retargeted to %q", target)
				}
			}
		})
	}
}

// The canonical baseline and Codex paths are conventions agents share, so a
// hand-authored skill can legitimately live there. Every install path — the
// explicit command and the installer's automatic `setup agents` alike — must
// refuse an unmarked one rather than overwrite it and then claim it.
func TestSkillInstallRefusesUnmarkedBaselineAndCodexSkills(t *testing.T) {
	home := agentHome(t, ".codex")
	custom := "# my own hey skill\n"
	baseline := filepath.Join(home, ".agents", "skills", "hey")
	codex := filepath.Join(home, ".codex", "skills", "hey")
	for _, dir := range []string{baseline, codex} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(custom), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var unmanaged *unmanagedSkillDirError
	if _, err := installSkillFiles(); !errors.As(err, &unmanaged) {
		t.Fatalf("installSkillFiles error = %v, want unmanaged refusal", err)
	}
	if _, err := installSkillToCodex(); !errors.As(err, &unmanaged) {
		t.Fatalf("installSkillToCodex error = %v, want unmanaged refusal", err)
	}
	for _, dir := range []string{baseline, codex} {
		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil || string(data) != custom {
			t.Errorf("%s changed: %q, %v", dir, data, err)
		}
		if ownedSkillDir(dir) {
			t.Errorf("%s was claimed", dir)
		}
	}
}

// An empty directory at the install path is not user content and is claimed.
func TestClaimSkillDirAcceptsEmptyAndMarkedDirectories(t *testing.T) {
	home := agentHome(t)
	empty := filepath.Join(home, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := claimSkillDir(empty); err != nil || !ownedSkillDir(empty) {
		t.Errorf("empty dir: %v, owned=%v", err, ownedSkillDir(empty))
	}
	if err := claimSkillDir(empty); err != nil {
		t.Errorf("re-claiming our own dir: %v", err)
	}
	missing := filepath.Join(home, "missing", "hey")
	if err := claimSkillDir(missing); err != nil || !ownedSkillDir(missing) {
		t.Errorf("missing dir: %v, owned=%v", err, ownedSkillDir(missing))
	}
}

// A marker only proves the directory; each file write still refuses to
// follow a symlink planted inside it — a link's target was never inspected,
// and truncating it would destroy a file we do not own.
func TestSkillInstallRefusesSymlinkedSkillFileInManagedDir(t *testing.T) {
	home := agentHome(t, ".codex")
	precious := filepath.Join(home, "precious.md")
	if err := os.WriteFile(precious, []byte("# do not truncate"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".agents", "skills", "hey"),
		filepath.Join(home, ".codex", "skills", "hey"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMarker(dir)
		if err := os.Symlink(precious, filepath.Join(dir, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := installSkillFiles(); err == nil {
		t.Error("baseline install wrote through a symlinked SKILL.md")
	}
	if _, err := installSkillToCodex(); err == nil {
		t.Error("Codex install wrote through a symlinked SKILL.md")
	}
	if data, _ := os.ReadFile(precious); string(data) != "# do not truncate" {
		t.Errorf("symlink target was truncated: %q", data)
	}
}

// A marker planted as a symlink or directory confers no ownership, so the
// unmanaged-content refusal still protects the regular SKILL.md next to it.
func TestClaimSkillDirRejectsNonRegularMarker(t *testing.T) {
	home := agentHome(t)
	dir := filepath.Join(home, "skills", "hey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "# my own hey skill"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ownershipMarkerFile), 0o755); err != nil {
		t.Fatal(err)
	}

	var unmanaged *unmanagedSkillDirError
	if err := claimSkillDir(dir); !errors.As(err, &unmanaged) {
		t.Fatalf("claimSkillDir = %v, want unmanaged refusal", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != custom {
		t.Errorf("user skill changed: %q", got)
	}
}

// The copy-fallback removal predicate applies the regular-file rule to every
// entry: a symlink planted in the marker's name authorizes nothing, and the
// user's SKILL.md beside it survives Claude setup.
func TestRemoveExistingSkillLinkRejectsSymlinkedMarker(t *testing.T) {
	home := agentHome(t)
	dir := filepath.Join(home, ".claude", "skills", "hey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "# my own hey skill"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "marker-target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ownershipMarkerFile)); err != nil {
		t.Fatal(err)
	}

	var unmanaged *unmanagedSkillDirError
	if err := removeExistingSkillLink(dir); !errors.As(err, &unmanaged) {
		t.Fatalf("removeExistingSkillLink = %v, want unmanaged refusal", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != custom {
		t.Errorf("user skill deleted: %q", got)
	}
}

// A user file added to the managed baseline is not carried into the Claude
// copy, so consecutive fallback installs stay repeatable.
func TestCopyFallbackSkipsUserFilesInBaseline(t *testing.T) {
	home := agentHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	origSymlink := makeSkillSymlink
	makeSkillSymlink = func(string, string) error { return errors.New("symlinks unavailable") }
	t.Cleanup(func() { makeSkillSymlink = origSymlink })
	jsonWriter(t)

	if err := runSkillInstall(newSkillInstallCommand(), nil); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// The user adds their own file to the managed baseline.
	baseline := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.WriteFile(filepath.Join(baseline, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	for run := 2; run <= 3; run++ {
		if err := runSkillInstall(newSkillInstallCommand(), nil); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "hey", "notes.txt")); !os.IsNotExist(err) {
		t.Error("user file was copied into the Claude skill directory")
	}
}

// Claude is never linked to a baseline the install paths refused to claim.
func TestLinkSkillToClaudeRequiresManagedBaseline(t *testing.T) {
	home := agentHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	var unmanaged *unmanagedSkillDirError
	if _, err := linkSkillToClaude(); !errors.As(err, &unmanaged) {
		t.Fatalf("linkSkillToClaude = %v, want unmanaged refusal", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "hey")); !os.IsNotExist(err) {
		t.Error("a link to unmanaged content was created")
	}
}
