package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/client"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/version"
)

var (
	jsonOutput  bool
	htmlOutput  bool
	agentOutput bool
	baseURL     string
	cfg         *config.Config
	authMgr     *auth.Manager
	apiClient   *client.Client
)

var rootCmd = &cobra.Command{
	Use:   "hey",
	Short: "CLI for HEY",
	Long: `A CLI for HEY
⠀⠀⠀⠀⠀⠀⣰⠲⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⡟⢳⡀⣏⠀⠘⣆⠀⠀⠀⠀⠀⣤⣤⡄⠀⠀⢠⣤⣤⣤⣤⣤⣤⣤⣤⠀⠀⢀⣤⣤⡄⠀
⠀⣴⢄⢳⠀⠹⣿⠀⠀⠸⣆⠴⠒⢢⡀⢻⣿⡇⠀⠀⢸⣿⣿⡟⠛⠛⠛⢿⣿⣇⠀⣼⣿⡟⠀⠀
⠀⢻⠈⠻⣧⠀⠹⣇⠀⢰⣿⠀⠀⠀⡇⢸⣿⣷⣶⣶⣾⣿⣿⣷⣶⣶⠀⠈⢿⣿⣼⣿⡟⠀⠀⠀
⣶⠺⣧⡀⠙⢧⠀⠉⠀⣸⢸⡆⠀⢸⠁⣼⣿⡏⠉⠉⢹⣿⣿⡏⠉⠉⠀⠀⠈⣿⣿⡟⠀⠀⠀⠀
⠘⣆⠈⠳⠀⠀⠀⠀⠀⢻⢸⠇⢀⡏⠀⣿⣿⡇⠀⠀⢸⣿⣿⣿⣶⣶⣶⡆⠀⣿⣿⡇⠀⠀⠀⠀
⠀⠈⠳⣄⡀⠀⠀⠀⠀⠈⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠉⠙⠚⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
	`,
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
		migrateOldCredentials()

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func Execute() {
	rootCmd.Version = version.Full()
	rootCmd.SetVersionTemplate("{{.Version}}\n")

	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output raw JSON")
	rootCmd.PersistentFlags().BoolVar(&htmlOutput, "html", false, "Output raw HTML (for commands that return HTML content)")
	rootCmd.PersistentFlags().BoolVar(&agentOutput, "agent", false, "")
	_ = rootCmd.PersistentFlags().MarkHidden("agent")
	rootCmd.PersistentFlags().StringVar(&baseURL, "base-url", "", "Override server URL")

	rootCmd.AddCommand(newAuthCommand().cmd)
	rootCmd.AddCommand(newBoxesCommand().cmd)
	rootCmd.AddCommand(newBoxCommand().cmd)
	rootCmd.AddCommand(newTopicCommand().cmd)
	rootCmd.AddCommand(newReplyCommand().cmd)
	rootCmd.AddCommand(newComposeCommand().cmd)
	rootCmd.AddCommand(newDraftsCommand().cmd)
	rootCmd.AddCommand(newCalendarsCommand().cmd)
	rootCmd.AddCommand(newRecordingsCommand().cmd)
	rootCmd.AddCommand(newTodoCommand().cmd)
	rootCmd.AddCommand(newHabitCommand().cmd)
	rootCmd.AddCommand(newTimetrackCommand().cmd)
	rootCmd.AddCommand(newJournalCommand().cmd)
	rootCmd.AddCommand(newTuiCommand().cmd)
	rootCmd.AddCommand(newSkillCommand().cmd)

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if agentOutput {
			agentHelpFunc(cmd, args)
		} else {
			defaultHelp(cmd, args)
		}
	})

	err := rootCmd.Execute()
	if err != nil {
		if jsonOutput {
			obj := map[string]interface{}{"error": err.Error()}
			var apiErr *client.APIError
			if errors.As(err, &apiErr) {
				obj["error"] = apiErr.Message
				obj["status"] = apiErr.StatusCode
			}
			_ = json.NewEncoder(os.Stdout).Encode(obj)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		os.Exit(1)
	}
}

func agentHelpFunc(cmd *cobra.Command, _ []string) {
	type flagInfo struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	type subInfo struct {
		Name string `json:"name"`
	}
	type surface struct {
		Name        string     `json:"name"`
		Flags       []flagInfo `json:"flags,omitempty"`
		Subcommands []subInfo  `json:"subcommands,omitempty"`
	}

	var flags []flagInfo
	seen := make(map[string]bool)
	addFlags := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Hidden || seen[f.Name] {
				return
			}
			seen[f.Name] = true
			flags = append(flags, flagInfo{Name: f.Name, Type: f.Value.Type()})
		})
	}
	addFlags(cmd.LocalFlags())
	addFlags(cmd.InheritedFlags())
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })

	var subs []subInfo
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			subs = append(subs, subInfo{Name: c.Name()})
		}
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })

	s := surface{
		Name:        cmd.Name(),
		Flags:       flags,
		Subcommands: subs,
	}
	_ = json.NewEncoder(os.Stdout).Encode(s)
}

func requireAuth() error {
	if !authMgr.IsAuthenticated() {
		return errNotLoggedIn
	}
	return nil
}

// migrateOldCredentials migrates credentials from the old config.json format
// to the new credential store (keyring or credentials.json).
func migrateOldCredentials() {
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
