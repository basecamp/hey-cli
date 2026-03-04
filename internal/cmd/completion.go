package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for hey.

To load completions:

Bash:
  $ source <(hey completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ hey completion bash > /etc/bash_completion.d/hey
  # macOS:
  $ hey completion bash > $(brew --prefix)/etc/bash_completion.d/hey

Zsh:
  $ source <(hey completion zsh)

  # To load completions for each session, execute once:
  $ hey completion zsh > "${fpath[1]}/_hey"

Fish:
  $ hey completion fish | source

  # To load completions for each session, execute once:
  $ hey completion fish > ~/.config/fish/completions/hey.fish

PowerShell:
  PS> hey completion powershell | Out-String | Invoke-Expression
`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(os.Stdout)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return output.ErrUsage("unsupported shell: " + args[0])
			}
		},
	}

	return cmd
}
