package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

func emailPostingArgs(kind *string, positional cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := positional(cmd, args); err != nil {
			return err
		}

		switch *kind {
		case "":
			return output.ErrUsageHint(
				"--kind is required for email thread actions",
				"Pass `--kind topic` from `hey box <box> --json` or `hey search --json`.",
			)
		case "topic":
			return nil
		case "world/post":
			return output.ErrUsageHint(
				fmt.Sprintf("hey %s does not manage HEY World posts", cmd.Name()),
				"hey-cli only manages email threads. Pass `--kind topic` for an email thread.",
			)
		default:
			return output.ErrUsageHint(
				fmt.Sprintf("hey %s only manages email threads; unsupported kind %q", cmd.Name(), *kind),
				"Pass `--kind topic` from `hey box <box> --json` or `hey search --json`.",
			)
		}
	}
}
