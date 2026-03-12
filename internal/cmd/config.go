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
	configCommand.cmd.AddCommand(newConfigSetCommand())

	return configCommand
}

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value in the global config",
		Example: `  hey config set base_url http://app.hey.localhost:3003
  hey config set base_url https://app.hey.com`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			switch key {
			case "base_url":
				if err := cfg.SetFromFlag(key, value); err != nil {
					return err
				}
			default:
				return output.ErrUsage(fmt.Sprintf("unknown config key: %s (available: base_url)", key))
			}

			if err := cfg.Save(); err != nil {
				return err
			}

			if writer.IsStyled() {
				fmt.Fprintf(cmd.OutOrStdout(), "Set %s = %s\n", key, value)
				return nil
			}
			return writeOK(map[string]string{"key": key, "value": value},
				output.WithSummary(fmt.Sprintf("Set %s = %s", key, value)),
			)
		},
	}
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

			return writeOK(entries,
				output.WithSummary(fmt.Sprintf("%d configuration values", len(entries))),
			)
		},
	}
}
