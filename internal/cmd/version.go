package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/version"
)

type versionCommand struct {
	cmd *cobra.Command
}

func newVersionCommand() *versionCommand {
	versionCommand := &versionCommand{}
	versionCommand.cmd = &cobra.Command{
		Use:   "version",
		Short: "Show the installed hey version",
		Long:  "Show the installed hey version, and with --json the commit, build date, Go version and where the build came from.",
		Example: `  hey version
  hey version --json`,
		Args: cobra.NoArgs,
		RunE: versionCommand.run,
	}

	return versionCommand
}

func (c *versionCommand) run(cmd *cobra.Command, args []string) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), version.Full())
		return nil
	}

	return writeOK(map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
		"go":      runtime.Version(),
		"source":  version.Source(),
	})
}
