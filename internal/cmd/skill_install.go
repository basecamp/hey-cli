package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
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
		return output.ErrAPI(0, fmt.Sprintf("getting home directory: %v", err))
	}

	skillDir := filepath.Join(home, ".agents", "skills", "hey")
	skillFile := filepath.Join(skillDir, "SKILL.md")
	symlinkDir := filepath.Join(home, ".claude", "skills")
	symlinkPath := filepath.Join(symlinkDir, "hey")

	// Read embedded SKILL.md
	data, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		return output.ErrAPI(0, fmt.Sprintf("reading embedded skill: %v", err))
	}

	// Create skill directory and write file
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return output.ErrAPI(0, fmt.Sprintf("creating skill directory: %v", err))
	}
	if err := os.WriteFile(skillFile, data, 0o644); err != nil {
		return output.ErrAPI(0, fmt.Sprintf("writing skill file: %v", err))
	}

	// Create symlink directory and symlink
	if err := os.MkdirAll(symlinkDir, 0o755); err != nil {
		return output.ErrAPI(0, fmt.Sprintf("creating symlink directory: %v", err))
	}
	// Remove existing symlink/file if present
	os.Remove(symlinkPath)
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "hey"), symlinkPath); err != nil {
		return output.ErrAPI(0, fmt.Sprintf("creating symlink: %v", err))
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Installed hey skill to ~/.agents/skills/hey/SKILL.md")
		fmt.Fprintln(cmd.OutOrStdout(), "Symlinked ~/.claude/skills/hey → ../../.agents/skills/hey")
		return nil
	}

	return writeOK(map[string]string{
		"skill_path":   skillFile,
		"symlink_path": symlinkPath,
	}, output.WithSummary("hey skill installed"))
}
