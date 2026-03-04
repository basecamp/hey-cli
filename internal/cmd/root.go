package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"hey-cli/internal/auth"
	"hey-cli/internal/client"
	"hey-cli/internal/config"
	"hey-cli/internal/tui"
)

var (
	jsonOutput bool
	baseURL    string
	cfg        *config.Config
	authMgr    *auth.Manager
	apiClient  *client.Client
)

var rootCmd = &cobra.Command{
	Use:           "hey",
	Short:         "CLI for the Haystack (HEY) email service",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load()
		if err != nil {
			return err
		}
		if baseURL != "" {
			cfg.BaseURL = baseURL
		}

		configDir := config.ConfigDir()
		httpClient := &http.Client{Timeout: 30 * time.Second}
		authMgr = auth.NewManager(cfg.BaseURL, httpClient, configDir)
		apiClient = client.New(cfg.BaseURL, authMgr)

		// Migrate old config credentials to new store
		migrateOldCredentials(configDir)

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

	rootCmd.AddCommand(newAuthCommand().cmd)
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
		if jsonOutput {
			obj := map[string]interface{}{"error": err.Error()}
			var apiErr *client.APIError
			if errors.As(err, &apiErr) {
				obj["error"] = apiErr.Message
				obj["status"] = apiErr.StatusCode
			}
			json.NewEncoder(os.Stdout).Encode(obj)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		os.Exit(1)
	}
}

func requireAuth() error {
	if !authMgr.IsAuthenticated() {
		return errNotLoggedIn
	}
	return nil
}

// migrateOldCredentials migrates credentials from the old config.json format
// to the new credential store (keyring or credentials.json).
func migrateOldCredentials(configDir string) {
	old, err := config.LoadOld()
	if err != nil {
		return
	}

	// Check if old config has credentials worth migrating
	if old.AccessToken == "" && old.SessionCookie == "" {
		return
	}

	store := authMgr.GetStore()
	credKey := authMgr.CredentialKey()

	// Don't overwrite existing credentials in the new store
	if _, err := store.Load(credKey); err == nil {
		return
	}

	creds := &auth.Credentials{
		AccessToken:  old.AccessToken,
		RefreshToken: old.RefreshToken,
	}
	if old.TokenExpiry > 0 {
		creds.ExpiresAt = old.TokenExpiry
	}
	if old.SessionCookie != "" {
		creds.SessionCookie = old.SessionCookie
	}

	if err := store.Save(credKey, creds); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not migrate credentials: %v\n", err)
		return
	}

	// Re-save config without secrets
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update config after migration: %v\n", err)
	}

	fmt.Fprintln(os.Stderr, "notice: credentials migrated from config.json to credential store")
}
