package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/harness"
)

// runRemoveAgentSetup removes the coding-agent integrations written by hey.
// Canonical paths without hey's ownership marker remain untouched.
func runRemoveAgentSetup(cmd *cobra.Command) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("getting home directory: %v", err))
	}

	var removed []string
	var failures []string

	if harness.InstalledPluginVersion() != "" {
		claudePath := harness.FindClaudeBinary()
		if claudePath == "" {
			failures = append(failures, "Claude Code plugin: claude binary not found")
		} else if out, uninstallErr := runClaudeStep(cmd.Context(), claudeInstallTimeout, claudePath, "plugin", "uninstall", harness.ClaudeExpectedPluginKey); uninstallErr != nil {
			failures = append(failures, "Claude Code plugin: "+agentCommandFailure(out, uninstallErr))
		} else {
			removed = append(removed, "Claude Code plugin")
		}
	}

	claudeSkill := filepath.Join(home, ".claude", "skills", harness.ClaudePluginName)
	if exists(claudeSkill) {
		if removeErr := removeExistingSkillLink(claudeSkill); removeErr != nil {
			var unmanaged *unmanagedSkillDirError
			if !errors.As(removeErr, &unmanaged) {
				failures = append(failures, "Claude Code skill: "+removeErr.Error())
			}
		} else {
			removed = append(removed, "Claude Code skill")
		}
	}

	if codexSkill := harness.CodexSkillPath(); codexSkill != "" {
		if didRemove, removeErr := removeOwnedSkillFiles(filepath.Dir(codexSkill)); removeErr != nil {
			failures = append(failures, "Codex skill: "+removeErr.Error())
		} else if didRemove {
			removed = append(removed, "Codex skill")
		}
	}

	baseline := filepath.Join(home, ".agents", "skills", "hey")
	if didRemove, removeErr := removeOwnedSkillFiles(baseline); removeErr != nil {
		failures = append(failures, "agent skill: "+removeErr.Error())
	} else if didRemove {
		removed = append(removed, "agent skill")
	}

	if len(failures) > 0 {
		return &apierr.Error{
			Code:    "setup_remove_failed",
			Message: "coding-agent integration removal incomplete: " + strings.Join(failures, "; "),
			Hint:    "Resolve the reported item, then run: hey setup agents --remove",
		}
	}

	if removed == nil {
		removed = []string{}
	}
	return writeMutation(cmd, "Coding-agent integrations removed", map[string]any{"removed": removed})
}

// removeOwnedSkillFiles removes only files hey writes from a marked skill
// directory. Additional files remain in place and make the directory
// user-owned once the marker is gone.
func removeOwnedSkillFiles(dir string) (bool, error) {
	if !ownedSkillDir(dir) {
		return false, nil
	}

	paths := []string{
		filepath.Join(dir, skillFilename),
		filepath.Join(dir, installedVersionFile),
		filepath.Join(dir, ownershipMarkerFile),
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspecting %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("%s is not a regular file", path)
		}
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("removing %s: %w", path, err)
		}
	}
	_ = os.Remove(dir) // Additional user files keep the now-unmanaged directory in place.
	return true, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
