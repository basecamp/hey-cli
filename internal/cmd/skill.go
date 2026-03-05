package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/skills"
)

type skillCommand struct {
	cmd *cobra.Command
}

func newSkillCommand() *skillCommand {
	sc := &skillCommand{}
	sc.cmd = &cobra.Command{
		Use:   "skill",
		Short: "Manage the embedded agent skill",
		Long:  "Print or install the SKILL.md embedded in this binary.",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := skills.FS.ReadFile("hey/SKILL.md")
			if err != nil {
				return fmt.Errorf("reading embedded skill: %w", err)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	sc.cmd.AddCommand(newSkillInstallCommand())
	return sc
}
