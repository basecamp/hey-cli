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
	// Replacement, automatic refresh, and the harness health checks all
	// require it: a directory that merely looks like ours (a user-authored
	// skill happens to be one SKILL.md too) is never destroyed, rewritten,
	// or reported as a working integration.
	ownershipMarkerFile = harness.SkillOwnershipMarker
)

// unmanagedSkillDirError reports a skill directory hey-cli did not write.
type unmanagedSkillDirError struct{ dir string }

func (e *unmanagedSkillDirError) Error() string {
	return fmt.Sprintf("%s exists but was not written by hey-cli; move it aside to let hey install its skill there", e.dir)
}

// claimSkillDir is the one gate every skill write goes through. It creates
// and marks a missing (or empty) directory, accepts one that already carries
// our marker, and refuses anything else — a populated directory without the
// marker is somebody's hand-authored skill, and neither an explicit install
// nor the installer's automatic handoff may overwrite it or claim it.
func claimSkillDir(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil { // #nosec G301 -- installed skills contain public documentation
			return fmt.Errorf("creating skill directory: %w", mkErr)
		}
	case err != nil:
		return fmt.Errorf("inspecting skill directory: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		// A symlink here is a user's arrangement (or our own Claude link,
		// which is never a write target). Writing through it would land in
		// a directory we never inspected.
		return &unmanagedSkillDirError{dir: dir}
	case !info.IsDir():
		return &unmanagedSkillDirError{dir: dir}
	case !ownedSkillDir(dir):
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("inspecting skill directory: %w", err)
		}
		if len(entries) > 0 {
			return &unmanagedSkillDirError{dir: dir}
		}
	}
	writeOwnershipMarker(dir)
	return nil
}

// writeOwnershipMarker stamps dir as hey-cli-managed. Only claimSkillDir
// calls it, after proving the directory is ours to claim.
func writeOwnershipMarker(dir string) {
	content := []byte("This skill is managed by hey-cli. Manual edits will be overwritten on upgrade.\n")
	_ = writeSkillFile(filepath.Join(dir, ownershipMarkerFile), content)
}

// writeSkillFile writes one skill file, refusing to write through a symlink
// or any other non-regular file: a link's target was never inspected by the
// ownership gate, so following it could truncate a file we do not own — even
// inside a directory that carries our marker.
func writeSkillFile(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return &unmanagedSkillDirError{dir: path}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspecting %s: %w", path, err)
	}
	return os.WriteFile(path, data, 0o644) // #nosec G306 G703 -- fixed skill locations under the user's own home, gated by claimSkillDir and the Lstat above
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

	if err := claimSkillDir(skillDir); err != nil {
		return "", err
	}
	if err := writeSkillFile(skillFile, data); err != nil {
		return "", fmt.Errorf("writing skill file: %w", err)
	}

	// Best-effort: stamp installed version so doctor can spot staleness.
	_ = writeSkillFile(filepath.Join(skillDir, installedVersionFile), []byte(version.Version))

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

	if err := makeSkillSymlink(claudeSkillLinkTarget, symlinkPath); err != nil {
		// Fallback (e.g. Windows without symlink privilege): copy the files.
		notice := fmt.Sprintf("symlink failed (%v), copied files instead", err)
		if copyErr := copySkillFiles(skillDir, symlinkPath); copyErr != nil {
			return "", fmt.Errorf("creating symlink: %w (copy fallback also failed: %w)", err, copyErr)
		}
		return notice, nil
	}
	return "", nil
}

// claudeSkillLinkTarget is the relative target every hey-cli-written
// ~/.claude/skills/hey symlink points at. It doubles as provenance: a link
// with any other target was not written by hey-cli.
var claudeSkillLinkTarget = filepath.Join("..", "..", ".agents", "skills", "hey")

// makeSkillSymlink is a seam so tests can exercise the symlink-less fallback.
var makeSkillSymlink = os.Symlink

// removeExistingSkillLink clears the way for a fresh Claude skill link,
// removing only what hey-cli itself wrote: our canonical symlink, or the
// directory a prior copy fallback left (marker plus allowlisted files). A
// user's own symlink, a regular file, or a populated unmarked directory is
// their state and errors instead — shape is not ownership.
func removeExistingSkillLink(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting existing skill link: %w", err)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		if target, err := os.Readlink(path); err != nil || target != claudeSkillLinkTarget {
			return &unmanagedSkillDirError{dir: path}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing existing skill link: %w", err)
		}
	case info.IsDir():
		if !isManagedSkillCopy(path) {
			return &unmanagedSkillDirError{dir: path}
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing existing skill copy: %w", err)
		}
	default:
		return &unmanagedSkillDirError{dir: path}
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
	if err := claimSkillDir(filepath.Dir(skillPath)); err != nil {
		return "", err
	}
	if err := writeSkillFile(skillPath, data); err != nil {
		return "", fmt.Errorf("writing Codex skill file: %w", err)
	}
	return skillPath, nil
}

// baselineSkillInstalled reports whether ~/.agents/skills/hey/SKILL.md is a
// healthy install: a regular file in a directory hey-cli owns — the same
// shape and ownership rules every write path enforces, so a state install
// refuses (a symlinked SKILL.md, an unmarked hand-authored skill) is never
// simultaneously reported healthy.
func baselineSkillInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	skillDir := filepath.Join(home, ".agents", "skills", "hey")
	return harness.RegularSkillFile(filepath.Join(skillDir, skillFilename)) && ownedSkillDir(skillDir)
}

// installedSkillVersion reads the .installed-version stamp from the baseline
// skill directory. Returns "" if absent or unreadable.
func installedSkillVersion() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "hey", installedVersionFile)) // #nosec G304 -- the fixed baseline version stamp under the user's home
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func copySkillFiles(src, dst string) error {
	// Claim the directory first: removeExistingSkillLink already proved dst is
	// ours to (re)create, and marking before copying means a partially copied
	// directory is still recognized and replaced on the next attempt.
	if err := claimSkillDir(dst); err != nil {
		return err
	}
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
		if err := writeSkillFile(filepath.Join(dst, e.Name()), data); err != nil {
			return err
		}
	}
	return nil
}
