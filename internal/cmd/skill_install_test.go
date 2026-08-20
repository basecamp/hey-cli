package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func TestSkillInstallReplacesExistingLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
