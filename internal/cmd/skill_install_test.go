package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func TestSkillInstallReplacesExistingLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	symlinkDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(symlinkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(symlinkDir, "hey")
	if err := os.Symlink("old-target", symlinkPath); err != nil {
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
