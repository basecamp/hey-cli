package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/skills"
)

func newSkillInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the hey skill globally for Claude",
		Long:  "Copies the embedded SKILL.md to ~/.agents/skills/hey/ and creates a symlink in ~/.claude/skills/hey.",
		RunE:  runSkillInstall,
	}
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	skillDir := filepath.Join(home, ".agents", "skills", "hey")
	skillFile := filepath.Join(skillDir, "SKILL.md")
	symlinkDir := filepath.Join(home, ".claude", "skills")
	symlinkPath := filepath.Join(symlinkDir, "hey")

	// Read embedded SKILL.md
	data, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		return fmt.Errorf("reading embedded skill: %w", err)
	}

	// Create skill directory and write file
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(skillFile, data, 0o644); err != nil {
		return fmt.Errorf("writing skill file: %w", err)
	}

	// Create symlink directory and symlink
	if err := os.MkdirAll(symlinkDir, 0o755); err != nil {
		return fmt.Errorf("creating symlink directory: %w", err)
	}
	// Remove existing symlink/file if present
	os.Remove(symlinkPath)
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "hey"), symlinkPath); err != nil {
		return fmt.Errorf("creating symlink: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Installed hey skill to ~/.agents/skills/hey/SKILL.md")
	fmt.Fprintln(cmd.OutOrStdout(), "Symlinked ~/.claude/skills/hey → ../../.agents/skills/hey")
	return nil
}
