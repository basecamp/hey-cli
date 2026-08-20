package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
	"github.com/basecamp/hey-cli/skills"
)

const (
	skillFilename        = "SKILL.md"
	installedVersionFile = ".installed-version"

	// ownershipMarkerFile marks a skill directory as written by hey-cli.
	// Replacement and automatic refresh require it: a directory that merely
	// looks like ours (a user-authored skill happens to be one SKILL.md too)
	// must never be destroyed or rewritten.
	ownershipMarkerFile = ".managed-by-hey-cli"
)

// writeOwnershipMarker stamps dir as hey-cli-managed. Best-effort: a failed
// stamp only means later automatic maintenance declines to touch the dir.
func writeOwnershipMarker(dir string) {
	content := []byte("This skill is managed by hey-cli. Manual edits will be overwritten on upgrade.\n")
	_ = os.WriteFile(filepath.Join(dir, ownershipMarkerFile), content, 0o644) // #nosec G306 -- not a secret
}

// ownedSkillDir reports whether hey-cli wrote the skill directory.
func ownedSkillDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ownershipMarkerFile))
	return err == nil
}

func newSkillInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the hey skill globally for your coding agents",
		Long:  "Copies the embedded SKILL.md to ~/.agents/skills/hey/, links it into ~/.claude/skills/hey when Claude Code is installed, and copies it for Codex when Codex is installed.",
		RunE:  runSkillInstall,
	}
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	skillPath, err := installSkillFiles()
	if err != nil {
		return output.ErrAPI(0, err.Error())
	}

	result := map[string]string{"skill_path": skillPath}
	lines := []string{"Installed hey skill to ~/.agents/skills/hey/SKILL.md"}

	if harness.DetectClaude() {
		notice, linkErr := linkSkillToClaude()
		if linkErr != nil {
			return output.ErrAPI(0, linkErr.Error())
		}
		home, _ := os.UserHomeDir()
		result["symlink_path"] = filepath.Join(home, ".claude", "skills", harness.ClaudePluginName)
		if notice != "" {
			result["notice"] = notice
			lines = append(lines, "Copied skill to ~/.claude/skills/hey ("+notice+")")
		} else {
			lines = append(lines, "Symlinked ~/.claude/skills/hey → ../../.agents/skills/hey")
		}
	}

	if harness.DetectCodex() {
		codexPath, codexErr := installSkillToCodex()
		if codexErr != nil {
			return output.ErrAPI(0, codexErr.Error())
		}
		result["codex_skill_path"] = codexPath
		lines = append(lines, "Copied skill to "+codexPath)
	}

	if writer.IsStyled() {
		for _, line := range lines {
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
		return nil
	}

	return writeOK(result, output.WithSummary("hey skill installed"))
}

// installSkillFiles writes the embedded SKILL.md to ~/.agents/skills/hey/ and
// stamps the installed version. Returns the path to the installed file.
func installSkillFiles() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}

	skillDir := filepath.Join(home, ".agents", "skills", "hey")
	skillFile := filepath.Join(skillDir, skillFilename)

	data, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("reading embedded skill: %w", err)
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil { // #nosec G301 -- installed skills contain public documentation
		return "", fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(skillFile, data, 0o644); err != nil { // #nosec G306 -- installed skills are intentionally user-readable
		return "", fmt.Errorf("writing skill file: %w", err)
	}

	// Best-effort: stamp installed version so doctor can spot staleness, and
	// ownership so replacement and refresh can prove this directory is ours.
	_ = os.WriteFile(filepath.Join(skillDir, installedVersionFile), []byte(version.Version), 0o644) // #nosec G306 -- not a secret
	writeOwnershipMarker(skillDir)

	return skillFile, nil
}

// linkSkillToClaude creates a symlink at ~/.claude/skills/hey pointing to the
// baseline skill directory, falling back to a file copy where symlinks are
// unavailable. A populated real directory at that path is an error: user
// content is never overwritten. Returns a human-readable notice when the copy
// fallback was used.
func linkSkillToClaude() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}

	skillDir := filepath.Join(home, ".agents", "skills", "hey")
	symlinkDir := filepath.Join(home, ".claude", "skills")
	symlinkPath := filepath.Join(symlinkDir, harness.ClaudePluginName)

	if err := os.MkdirAll(symlinkDir, 0o755); err != nil { // #nosec G301 -- standard user-level skills directory
		return "", fmt.Errorf("creating symlink directory: %w", err)
	}
	if err := removeExistingSkillLink(symlinkPath); err != nil {
		return "", err
	}

	if err := makeSkillSymlink(filepath.Join("..", "..", ".agents", "skills", "hey"), symlinkPath); err != nil {
		// Fallback (e.g. Windows without symlink privilege): copy the files.
		notice := fmt.Sprintf("symlink failed (%v), copied files instead", err)
		if copyErr := copySkillFiles(skillDir, symlinkPath); copyErr != nil {
			return "", fmt.Errorf("creating symlink: %w (copy fallback also failed: %w)", err, copyErr)
		}
		return notice, nil
	}
	return "", nil
}

// makeSkillSymlink is a seam so tests can exercise the symlink-less fallback.
var makeSkillSymlink = os.Symlink

// removeExistingSkillLink clears the way for a fresh Claude skill link. The
// interesting case is a real directory: a prior run's copy fallback leaves
// one behind, and it must be replaceable on the next run (os.Remove fails on
// a non-empty directory). Only a directory carrying our ownership marker is
// removed — shape is not ownership, so a user-authored skill that happens to
// be a single SKILL.md stays untouched and errors instead.
func removeExistingSkillLink(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	if !isManagedSkillCopy(path) {
		return fmt.Errorf("removing existing skill link: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("removing existing skill copy: %w", err)
	}
	return nil
}

// isManagedSkillCopy reports whether path is a plain directory hey-cli's copy
// fallback wrote: it carries the ownership marker and nothing but the files
// we write. Both conditions are required — the marker proves provenance, and
// the allowlist keeps anything a user added alongside it safe.
func isManagedSkillCopy(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	sawMarker := false
	for _, entry := range entries {
		if entry.IsDir() {
			return false
		}
		switch entry.Name() {
		case ownershipMarkerFile:
			sawMarker = true
		case skillFilename, installedVersionFile:
		default:
			return false
		}
	}
	return sawMarker
}

// installSkillToCodex copies the embedded SKILL.md into Codex's skills
// directory ($CODEX_HOME or ~/.codex). Codex does not follow the shared
// ~/.agents layout, so this is a copy rather than a link.
func installSkillToCodex() (string, error) {
	skillPath := harness.CodexSkillPath()
	if skillPath == "" {
		return "", fmt.Errorf("cannot determine Codex home directory")
	}

	data, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("reading embedded skill: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil { // #nosec G301 -- installed skills contain public documentation
		return "", fmt.Errorf("creating Codex skill directory: %w", err)
	}
	if err := os.WriteFile(skillPath, data, 0o644); err != nil { // #nosec G306 -- installed skills are intentionally user-readable
		return "", fmt.Errorf("writing Codex skill file: %w", err)
	}
	writeOwnershipMarker(filepath.Dir(skillPath))
	return skillPath, nil
}

// baselineSkillInstalled reports whether ~/.agents/skills/hey/SKILL.md exists.
func baselineSkillInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".agents", "skills", "hey", skillFilename))
	return err == nil
}

// installedSkillVersion reads the .installed-version stamp from the baseline
// skill directory. Returns "" if absent or unreadable.
func installedSkillVersion() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "hey", installedVersionFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func copySkillFiles(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil { // #nosec G301 -- installed skills contain public documentation
		return err
	}
	// Claim the directory first: removeExistingSkillLink already proved dst is
	// ours to (re)create, and marking before copying means a partially copied
	// directory is still recognized and replaced on the next attempt.
	writeOwnershipMarker(dst)
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			return fmt.Errorf("skill directory contains subdirectory %q; copy fallback only supports flat files", e.Name())
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name())) // #nosec G304 -- reading the baseline skill directory we wrote
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil { // #nosec G306 -- installed skills are intentionally user-readable
			return err
		}
	}
	return nil
}
