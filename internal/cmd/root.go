package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/client"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

var (
	jsonFlag    bool
	htmlOutput  bool
	quietFlag   bool
	idsOnly     bool
	countFlag   bool
	markdownF   bool
	styledFlag  bool
	agentFlag   bool
	statsFlag   bool
	verboseFlag int
	baseURL     string
	cfg         *config.Config
	authMgr     *auth.Manager
	apiClient   *client.Client
	writer      *output.Writer
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
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
			format := output.FormatFromFlags(jsonFlag, quietFlag, idsOnly, countFlag, markdownF, styledFlag, agentFlag)
			writer = output.New(output.Options{Format: format})

			var err error
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			if baseURL != "" {
				if err := cfg.SetFromFlag("base_url", baseURL); err != nil {
					return err
				}
			}

			if os.Getenv("HEY_DEBUG") != "" && verboseFlag == 0 {
				verboseFlag = 1
			}

			configDir := config.ConfigDir()
			httpClient := &http.Client{Timeout: 30 * time.Second}
			authMgr = auth.NewManager(cfg.BaseURL, httpClient, configDir)
			apiClient = client.New(cfg.BaseURL, authMgr)

			if verboseFlag > 0 {
				apiClient.Logger = os.Stderr
			}

			migrateOldCredentials(configDir)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.CompletionOptions.HiddenDefaultCmd = true

	root.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output as JSON envelope")
	root.PersistentFlags().BoolVar(&htmlOutput, "html", false, "Output raw HTML (for commands that return HTML content)")
	root.PersistentFlags().BoolVar(&quietFlag, "quiet", false, "Output raw data without envelope")
	root.PersistentFlags().BoolVar(&idsOnly, "ids-only", false, "Output only IDs, one per line")
	root.PersistentFlags().BoolVar(&countFlag, "count", false, "Output only the count of results")
	root.PersistentFlags().BoolVar(&markdownF, "markdown", false, "Output as Markdown")
	root.PersistentFlags().BoolVar(&styledFlag, "styled", false, "Force styled output even when piped")
	root.PersistentFlags().BoolVar(&agentFlag, "agent", false, "Agent mode (JSON envelope, no TTY formatting)")
	root.PersistentFlags().StringVar(&baseURL, "base-url", "", "Override server URL")
	root.PersistentFlags().CountVarP(&verboseFlag, "verbose", "v", "Increase verbosity (request logging)")
	root.PersistentFlags().BoolVar(&statsFlag, "stats", false, "Include request stats in response meta")

	root.Version = version.Version
	root.SetVersionTemplate("hey version {{.Version}}\n")

	// Override help to support --help --agent for structured JSON
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if agentFlag {
			printAgentHelp(cmd)
			return
		}
		defaultHelp(cmd, args)
	})

	root.AddCommand(newAuthCommand().cmd)
	root.AddCommand(newBoxesCommand().cmd)
	root.AddCommand(newBoxCommand().cmd)
	root.AddCommand(newTopicCommand().cmd)
	root.AddCommand(newReplyCommand().cmd)
	root.AddCommand(newComposeCommand().cmd)
	root.AddCommand(newDraftsCommand().cmd)
	root.AddCommand(newCalendarsCommand().cmd)
	root.AddCommand(newRecordingsCommand().cmd)
	root.AddCommand(newTodoCommand().cmd)
	root.AddCommand(newHabitCommand().cmd)
	root.AddCommand(newTimetrackCommand().cmd)
	root.AddCommand(newJournalCommand().cmd)
	root.AddCommand(newSetupCommand())
	root.AddCommand(newTuiCommand().cmd)
	root.AddCommand(newSkillCommand().cmd)
	root.AddCommand(newCommandsCommand())
	root.AddCommand(newCompletionCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newConfigCommand().cmd)

	return root
}

func Execute() {
	root := newRootCmd()

	err := root.Execute()
	if err != nil {
		err = normalizeCobraError(err)
		if writer == nil {
			writer = output.New(output.Options{Format: output.FormatJSON})
		}
		writer.Err(err)
		os.Exit(output.ExitCodeFor(err))
	}
}

func requireAuth() error {
	if !authMgr.IsAuthenticated() {
		return output.ErrAuth("not logged in — run `hey auth login` first")
	}
	return nil
}

// migrateOldCredentials migrates credentials from the old config.json format
// to the new credential store (keyring or credentials.json).
func migrateOldCredentials(_ string) {
	old, err := config.LoadOld()
	if err != nil {
		return
	}

	if old.AccessToken == "" && old.SessionCookie == "" {
		return
	}

	store := authMgr.GetStore()
	credKey := authMgr.CredentialKey()

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

	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update config after migration: %v\n", err)
	}

	fmt.Fprintln(os.Stderr, "notice: credentials migrated from config.json to credential store")
}

func normalizeCobraError(err error) error {
	var e *output.Error
	if errors.As(err, &e) {
		return err
	}
	if isCobraParseError(err) {
		return output.ErrUsageHint(err.Error(), "Run 'hey --help' for usage information")
	}
	return err
}

func isCobraParseError(err error) bool {
	msg := err.Error()
	patterns := []string{
		"unknown flag",
		"unknown shorthand flag",
		"unknown command",
		"required flag",
		"arg(s)",
		"invalid argument",
		"flag needs an argument",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

func statsOption() output.ResponseOption {
	if !statsFlag {
		return func(*output.Response) {}
	}
	return output.WithMeta("stats", map[string]any{
		"requests":   apiClient.RequestCount(),
		"latency_ms": apiClient.TotalLatency().Milliseconds(),
	})
}

func printAgentHelp(cmd *cobra.Command) {
	info := map[string]any{
		"name":  cmd.Name(),
		"use":   cmd.Use,
		"short": cmd.Short,
	}
	if cmd.Long != "" {
		info["long"] = cmd.Long
	}
	if notes, ok := cmd.Annotations["agent_notes"]; ok {
		info["agent_notes"] = notes
	}

	var flags []map[string]string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags = append(flags, map[string]string{
			"name":      f.Name,
			"shorthand": f.Shorthand,
			"usage":     f.Usage,
			"default":   f.DefValue,
		})
	})
	if len(flags) > 0 {
		info["flags"] = flags
	}

	var subs []map[string]string
	for _, sub := range cmd.Commands() {
		if sub.Hidden || !sub.IsAvailableCommand() {
			continue
		}
		subs = append(subs, map[string]string{
			"name":  sub.Name(),
			"short": sub.Short,
		})
	}
	if len(subs) > 0 {
		info["subcommands"] = subs
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(info)
}
