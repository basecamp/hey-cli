package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/basecamp/hey-cli/internal/output"
)

func newCommandsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "commands",
		Short: "List all available commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			catalog := walkCommands(cmd.Root(), "")

			if writer.IsStyled() {
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"Command", "Description"})
				for _, entry := range catalog {
					table.addRow([]string{entry["path"].(string), entry["short"].(string)})
				}
				table.print()
				return nil
			}

			return writer.OK(catalog, statsOption(),
				output.WithSummary("Available commands"),
			)
		},
	}
}

func walkCommands(cmd *cobra.Command, prefix string) []map[string]any {
	var result []map[string]any

	for _, child := range cmd.Commands() {
		if child.Hidden || !child.IsAvailableCommand() {
			continue
		}

		path := prefix + child.Name()

		entry := map[string]any{
			"name":  child.Name(),
			"path":  path,
			"short": child.Short,
		}

		if notes, ok := child.Annotations["agent_notes"]; ok {
			entry["agent_notes"] = notes
		}

		var flags []map[string]string
		child.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			flags = append(flags, map[string]string{
				"name":      f.Name,
				"shorthand": f.Shorthand,
				"usage":     f.Usage,
				"default":   f.DefValue,
			})
		})
		if len(flags) > 0 {
			entry["flags"] = flags
		}

		subs := walkCommands(child, path+" ")
		if len(subs) > 0 {
			entry["subcommands"] = subs
		}

		result = append(result, entry)
	}

	return result
}
