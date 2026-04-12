package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type configCommand struct {
	cmd *cobra.Command
}

var configKeys = []string{"base_url", "default_sender"}

func newConfigCommand() *configCommand {
	configCommand := &configCommand{}
	configCommand.cmd = &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	configCommand.cmd.AddCommand(newConfigShowCommand())
	configCommand.cmd.AddCommand(newConfigSetCommand())
	configCommand.cmd.AddCommand(newConfigGetCommand())
	configCommand.cmd.AddCommand(newConfigUnsetCommand())

	return configCommand
}

// normalizeConfigKey converts hyphens to underscores for config key lookup.
func normalizeConfigKey(key string) string {
	return strings.ReplaceAll(key, "-", "_")
}

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value in the global config",
		Example: `  hey config set base_url http://app.hey.localhost:3003
  hey config set base_url https://app.hey.com
  hey config set default_sender erik@parrotapp.com`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := normalizeConfigKey(args[0]), args[1]

			switch key {
			case "base_url", "default_sender":
				if err := cfg.SetFromFlag(key, value); err != nil {
					return err
				}
			default:
				return output.ErrUsage(fmt.Sprintf("unknown config key: %s (available: %s)", key, strings.Join(configKeys, ", ")))
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

func newConfigGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Example: `  hey config get default_sender
  hey config get base_url`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := normalizeConfigKey(args[0])

			var value string
			switch key {
			case "base_url":
				value = cfg.BaseURL
			case "default_sender":
				value = cfg.DefaultSender
			default:
				return output.ErrUsage(fmt.Sprintf("unknown config key: %s (available: %s)", key, strings.Join(configKeys, ", ")))
			}

			if writer.IsStyled() {
				if value == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "%s is not set\n", key)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), value)
				}
				return nil
			}
			return writeOK(map[string]string{"key": key, "value": value},
				output.WithSummary(value),
			)
		},
	}
}

func newConfigUnsetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Clear a configuration value",
		Example: `  hey config unset default_sender`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := normalizeConfigKey(args[0])

			switch key {
			case "default_sender":
				cfg.UnsetField(key)
			default:
				return output.ErrUsage(fmt.Sprintf("cannot unset key: %s (unsettable keys: default_sender)", key))
			}

			if err := cfg.Save(); err != nil {
				return err
			}

			if writer.IsStyled() {
				fmt.Fprintf(cmd.OutOrStdout(), "Unset %s\n", key)
				return nil
			}
			return writeOK(map[string]string{"key": key},
				output.WithSummary(fmt.Sprintf("Unset %s", key)),
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
				{
					"key":    "default_sender",
					"value":  cfg.DefaultSender,
					"source": string(cfg.SourceOf("default_sender")),
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
