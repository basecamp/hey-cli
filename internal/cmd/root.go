package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"hey-cli/internal/client"
	"hey-cli/internal/config"
	"hey-cli/internal/tui"
)

var (
	jsonOutput bool
	baseURL    string
	cfg        *config.Config
	apiClient  *client.Client
)

var rootCmd = &cobra.Command{
	Use:          "hey",
	Short:        "CLI for the Haystack (HEY) email service",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load()
		if err != nil {
			return err
		}
		if baseURL != "" {
			cfg.BaseURL = baseURL
		}
		apiClient = client.New(cfg)
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		return tui.Run(apiClient)
	},
}

func Execute() {
	rootCmd.CompletionOptions.HiddenDefaultCmd = true

	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output raw JSON")
	rootCmd.PersistentFlags().StringVar(&baseURL, "base-url", "", "Override server URL")

	rootCmd.AddCommand(newLoginCommand().cmd)
	rootCmd.AddCommand(newLogoutCommand().cmd)
	rootCmd.AddCommand(newStatusCommand().cmd)
	rootCmd.AddCommand(newBoxesCommand().cmd)
	rootCmd.AddCommand(newBoxCommand().cmd)
	rootCmd.AddCommand(newTopicCommand().cmd)
	rootCmd.AddCommand(newEntryCommand().cmd)
	rootCmd.AddCommand(newReplyCommand().cmd)
	rootCmd.AddCommand(newComposeCommand().cmd)
	rootCmd.AddCommand(newDraftsCommand().cmd)
	rootCmd.AddCommand(newCalendarsCommand().cmd)
	rootCmd.AddCommand(newRecordingsCommand().cmd)
	rootCmd.AddCommand(newTodoCommand().cmd)
	rootCmd.AddCommand(newHabitCommand().cmd)
	rootCmd.AddCommand(newTimetrackCommand().cmd)
	rootCmd.AddCommand(newJournalCommand().cmd)

	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func requireAuth() error {
	if !cfg.IsLoggedIn() {
		return errNotLoggedIn
	}
	return nil
}
