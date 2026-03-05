package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type configCommand struct {
	cmd *cobra.Command
}

func newConfigCommand() *configCommand {
	configCommand := &configCommand{}
	configCommand.cmd = &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	configCommand.cmd.AddCommand(newConfigShowCommand())

	return configCommand
}

func newConfigShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current configuration with sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries := []map[string]string{
				{
					"key":    "base_url",
					"value":  cfg.BaseURL,
					"source": string(cfg.SourceOf("base_url")),
				},
			}

			if writer.IsStyled() {
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"Key", "Value", "Source"})
				for _, e := range entries {
					table.addRow([]string{e["key"], e["value"], e["source"]})
				}
				table.print()
				return nil
			}

			return writer.OK(entries,
				output.WithSummary(fmt.Sprintf("%d configuration values", len(entries))),
			)
		},
	}
}
