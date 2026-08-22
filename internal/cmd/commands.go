package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/basecamp/hey-cli/internal/output"
)

const (
	compatibilityForAnnotation   = "compatibility_for"
	compatibilityUsageAnnotation = "compatibility_usage"
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
				for _, entry := range flattenCommandCatalog(catalog) {
					path, _ := entry["path"].(string)
					table.addRow([]string{path, commandCatalogDescription(entry)})
				}
				table.print()
				return nil
			}

			return writeOK(catalog,
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
		if canonical, ok := child.Annotations[compatibilityForAnnotation]; ok {
			entry["compatibility_for"] = canonical
		}
		if usage, ok := child.Annotations[compatibilityUsageAnnotation]; ok {
			entry["compatibility_usage"] = usage
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

func flattenCommandCatalog(entries []map[string]any) []map[string]any {
	var flattened []map[string]any
	for _, entry := range entries {
		flattened = append(flattened, entry)
		if children, ok := entry["subcommands"].([]map[string]any); ok {
			flattened = append(flattened, flattenCommandCatalog(children)...)
		}
	}
	return flattened
}

func commandCatalogDescription(entry map[string]any) string {
	description, _ := entry["short"].(string)
	if canonical, ok := entry["compatibility_for"].(string); ok {
		return description + " (compatibility for hey " + canonical + ")"
	}
	if usage, ok := entry["compatibility_usage"].(string); ok {
		return description + " (also accepts compatibility form hey " + usage + ")"
	}
	return description
}
