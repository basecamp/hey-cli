package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type configCommand struct {
	cmd *cobra.Command
}

func newConfigCommand() *configCommand {
	configCommand := &configCommand{}
	configCommand.cmd = &cobra.Command{
		Use:   "config",
		Short: "View and change settings",
	}

	configCommand.cmd.AddCommand(newConfigShowCommand())
	configCommand.cmd.AddCommand(newConfigSetCommand())
	configCommand.cmd.AddCommand(newConfigTrustLocalCommand())
	configCommand.cmd.AddCommand(newConfigUntrustLocalCommand())
	configCommand.cmd.AddCommand(newConfigTrustedLocalsCommand())

	return configCommand
}

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value in the global config",
		Example: `  hey config set base_url http://app.hey.localhost:3003
  hey config set base_url https://app.hey.com
  hey config set vim_mode true`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			switch key {
			case "base_url":
				if err := cfg.SetFromFlag(key, value); err != nil {
					return err
				}
				if err := cfg.SaveBaseURL(cfg.BaseURL); err != nil {
					return err
				}
			case "onboarded":
				onboarded, err := strconv.ParseBool(value)
				if err != nil {
					return apierr.ErrUsage(fmt.Sprintf("onboarded must be true or false (got %q)", value))
				}
				if err := cfg.SaveOnboarded(onboarded); err != nil {
					return err
				}
			case "vim_mode":
				enabled, err := strconv.ParseBool(value)
				if err != nil {
					return apierr.ErrUsage(fmt.Sprintf("vim_mode must be true or false (got %q)", value))
				}
				if err := config.SaveVimMode(enabled); err != nil {
					return err
				}
				cfg.VimMode = enabled
			default:
				return apierr.ErrUsage(fmt.Sprintf("unknown config key: %s (available: base_url, onboarded, vim_mode)", key))
			}

			summary := fmt.Sprintf("Set %s = %s", key, value)
			return writeMutationLine(cmd, summary, summary, map[string]string{"key": key, "value": value})
		},
	}
}

func newConfigTrustLocalCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trust-local",
		Short: "Trust this repository's local HEY settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			local := cfg.LocalConfig()
			if local == nil {
				return apierr.ErrUsage("no local .hey/config.json with a server or account setting was found")
			}
			if err := cfg.TrustLocalConfig(); err != nil {
				return err
			}
			return writeMutationLine(cmd,
				fmt.Sprintf("Trusted local HEY configuration: %s", local.Path),
				"Trusted local HEY configuration",
				cfg.LocalConfig())
		},
	}
}

func newConfigUntrustLocalCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "untrust-local",
		Short: "Remove trust for this repository's local HEY settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			local := cfg.LocalConfig()
			if local == nil {
				return apierr.ErrUsage("no local .hey/config.json with a server or account setting was found")
			}
			if err := cfg.UntrustLocalConfig(); err != nil {
				return err
			}
			return writeMutationLine(cmd,
				fmt.Sprintf("Removed trust for local HEY configuration: %s", local.Path),
				"Removed trust for local HEY configuration",
				cfg.LocalConfig())
		},
	}
}

func newConfigTrustedLocalsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trusted-locals",
		Short: "List trusted repository-local HEY settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			trusted, err := config.TrustedLocalConfigs()
			if err != nil {
				return err
			}
			if writer.IsStyled() {
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"Configuration", "Server", "Account", "Digest"})
				for _, local := range trusted {
					digest := local.Digest
					if len(digest) > 12 {
						digest = digest[:12]
					}
					table.addRow([]string{
						terminal.SanitizeLine(local.Path),
						terminal.SanitizeLine(local.ServerOrigin),
						terminal.SanitizeLine(local.AccountID),
						terminal.SanitizeLine(digest),
					})
				}
				table.print()
				return nil
			}
			return writeOK(trusted, output.WithSummary(fmt.Sprintf("%d trusted local configurations", len(trusted))))
		},
	}
}

func newConfigShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current configuration with sources",
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries := []map[string]string{
				{
					"key":    "base_url",
					"value":  cfg.BaseURL,
					"source": string(cfg.SourceOf("base_url")),
				},
				{
					"key":    "account_id",
					"value":  cfg.AccountID,
					"source": string(cfg.SourceOf("account_id")),
				},
				{
					"key":    "vim_mode",
					"value":  strconv.FormatBool(cfg.VimMode),
					"source": "global",
				},
				{
					"key":    "onboarded",
					"value":  strconv.FormatBool(cfg.Onboarded),
					"source": "global",
				},
			}
			if local := cfg.LocalConfig(); local != nil {
				trust := "untrusted"
				if local.Trusted {
					trust = "trusted"
				}
				entries = append(entries, map[string]string{
					"key":    "local_config_trust",
					"value":  trust,
					"source": "local",
				})
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
