package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func emailPostingArgs(kind *string, positional cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := positional(cmd, args); err != nil {
			return err
		}

		switch *kind {
		case "":
			return apierr.ErrUsageHint(
				"--kind is required for email thread actions",
				"Use a kind=topic record from `hey box view <box> --json` or `hey label view <label> --json`, or an email result from `hey search --json`; pass `--kind topic`.",
			)
		case "topic":
			return nil
		case "world/post":
			return apierr.ErrUsageHint(
				fmt.Sprintf("hey %s does not manage HEY World posts", cmd.Name()),
				"hey-cli only manages email threads. Pass `--kind topic` for an email thread.",
			)
		default:
			return apierr.ErrUsageHint(
				fmt.Sprintf("hey %s only manages email threads; unsupported kind %q", cmd.Name(), *kind),
				"Use a kind=topic record from `hey box view <box> --json` or `hey label view <label> --json`, or an email result from `hey search --json`; pass `--kind topic`.",
			)
		}
	}
}
