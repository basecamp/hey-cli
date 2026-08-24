package cmd

import (
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func newShellCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell-completion",
		Short: "Set up tab completion for your shell",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: install, generate. install writes the script where the shell reads it and is the usual way in; generate prints it to stdout for a package or a dotfile to place itself.",
		},
	}

	cmd.AddCommand(newCompletionInstallCommand().cmd)
	cmd.AddCommand(newCompletionGenerateCommand())

	return cmd
}

func newCompletionGenerateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "generate <bash|zsh|fish|powershell>",
		Short: "Print a completion script to stdout",
		Long: `Print hey's completion script for a shell to stdout.

This is the by-hand route, for a package that places the file itself or a dotfile
that sources it. To just make tab completion work, use hey shell-completion install
— it writes the script where your shell already looks.

Load it for the current session:

  bash   $ source <(hey shell-completion generate bash)
  zsh    $ source <(hey shell-completion generate zsh)
  fish   $ hey shell-completion generate fish | source
  pwsh   PS> hey shell-completion generate powershell | Out-String | Invoke-Expression

Keep it for every session by writing it where the shell reads completions from.
Those directories differ per shell and per machine, and a file outside them is read
by nothing — hey shell-completion install works out the right one and reports it.`,
		Example: `  hey shell-completion generate bash
  hey shell-completion generate zsh > "$HOME/.local/share/zsh/site-functions/_hey"`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return apierr.ErrUsage("unsupported shell: " + args[0])
			}
		},
	}
}
