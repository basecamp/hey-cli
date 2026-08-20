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
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/tui"
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
	jqFlag      string
	versionFlag bool
	verboseFlag int
	baseURL     string
	accountFlag string
	cfg         *config.Config
	authMgr     *auth.Manager
	writer      *output.Writer
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hey",
		Short: "Read, send, and organize HEY email, contacts, and calendars from your terminal.",
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
			jqRequested := cmd.Flags().Changed("jq")
			format := output.FormatFromFlags(jsonFlag || jqRequested, quietFlag, idsOnly, countFlag, markdownF, styledFlag, agentFlag)
			writer = output.New(output.Options{
				Format:   format,
				Stdout:   cmd.OutOrStdout(),
				Stderr:   cmd.ErrOrStderr(),
				JQFilter: jqFlag,
			})
			if versionFlag {
				if jqRequested {
					return output.ErrJQNotSupported("the version command")
				}
				return nil
			}
			if err := validateJQFlags(cmd, jqFlag, jqRequested, idsOnly, countFlag); err != nil {
				return err
			}

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
			if accountFlag != "" {
				if err := cfg.SetFromFlag("account_id", accountFlag); err != nil {
					return err
				}
			}

			if err := ensureLocalConfigTrusted(cmd); err != nil {
				return err
			}

			cleanupUpgradeSidecars()

			if os.Getenv("HEY_DEBUG") != "" && verboseFlag == 0 {
				verboseFlag = 1
			}

			configDir := config.ConfigDir()
			httpClient := &http.Client{Timeout: 30 * time.Second}
			authMgr = auth.NewManager(cfg.BaseURL, httpClient, configDir)
			initSDK(authMgr, cfg.BaseURL)

			migrateOldCredentials(configDir)

			if authMgr.IsAuthenticated() && commandUsesAccountScope(cmd) {
				if err := selectConfiguredAccount(cmd.Context()); err != nil {
					return err
				}
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if versionFlag {
				fmt.Fprintf(cmd.OutOrStdout(), "hey version %s\n", version.Version)
				return nil
			}
			if !stdinIsTerminal() || !stdoutIsTerminal() {
				return cmd.Help()
			}
			if err := requireAuth(); err != nil {
				return err
			}
			return tui.Run(rootSDK, sdk, cfg.AccountID)
		},
	}

	root.CompletionOptions.HiddenDefaultCmd = true

	root.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output JSON with metadata")
	root.PersistentFlags().BoolVar(&htmlOutput, "html", false, "Output raw HTML (for commands that return HTML content)")
	root.PersistentFlags().BoolVar(&quietFlag, "quiet", false, "Output result data only")
	root.PersistentFlags().BoolVar(&idsOnly, "ids-only", false, "Output only IDs, one per line")
	root.PersistentFlags().BoolVar(&countFlag, "count", false, "Output only the count of results")
	root.PersistentFlags().BoolVar(&markdownF, "markdown", false, "Output as Markdown")
	root.PersistentFlags().BoolVar(&styledFlag, "styled", false, "Force styled output even when piped")
	root.PersistentFlags().BoolVar(&agentFlag, "agent", false, "Agent mode (JSON envelope, no TTY formatting)")
	_ = root.PersistentFlags().MarkHidden("agent") // flag is registered immediately above
	root.PersistentFlags().StringVar(&baseURL, "base-url", "", "Override server URL")
	root.PersistentFlags().StringVar(&accountFlag, "account", "", "Select a linked mail account ID or all")
	root.PersistentFlags().CountVarP(&verboseFlag, "verbose", "v", "Show request details")
	root.PersistentFlags().BoolVar(&statsFlag, "stats", false, "Include request stats in response meta")
	root.PersistentFlags().StringVar(&jqFlag, "jq", "", "Filter JSON with a built-in jq expression")
	root.Flags().BoolVar(&versionFlag, "version", false, "Show version")

	// Override help with styled categories and curated flags
	root.SetHelpFunc(customHelpFunc(root.HelpFunc()))

	root.AddCommand(newAuthCommand().cmd)
	root.AddCommand(newAccountsCommand().cmd)
	root.AddCommand(newBoxesCommand().cmd)
	root.AddCommand(newBoxCommand().cmd)
	root.AddCommand(newLabelsCommand().cmd)
	root.AddCommand(newLabelCommand().cmd)
	root.AddCommand(newSearchCommand().cmd)
	root.AddCommand(newContactsCommand().cmd)
	root.AddCommand(newThreadsCommand().cmd)
	root.AddCommand(newAttachmentsCommand().cmd)
	root.AddCommand(newReplyCommand().cmd)
	root.AddCommand(newBulkReplyCommand().cmd)
	root.AddCommand(newForwardCommand().cmd)
	root.AddCommand(newComposeCommand().cmd)
	root.AddCommand(newDraftsCommand().cmd)
	root.AddCommand(newCalendarsCommand().cmd)
	root.AddCommand(newRecordingsCommand().cmd)
	root.AddCommand(newTodoCommand().cmd)
	root.AddCommand(newHabitCommand().cmd)
	root.AddCommand(newTimetrackCommand().cmd)
	root.AddCommand(newJournalCommand().cmd)
	root.AddCommand(newWatchCommand().cmd)
	root.AddCommand(newSeenCommand().cmd)
	root.AddCommand(newUnseenCommand().cmd)
	root.AddCommand(newMoveCommand().cmd)
	root.AddCommand(newTrashCommand().cmd)
	root.AddCommand(newSpamCommand().cmd)
	root.AddCommand(newIgnoreCommand().cmd)
	root.AddCommand(newStopIgnoringCommand().cmd)
	root.AddCommand(newSetupCommand())
	root.AddCommand(newTuiCommand().cmd)
	root.AddCommand(newSkillCommand().cmd)
	root.AddCommand(newCommandsCommand())
	root.AddCommand(newCompletionCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newConfigCommand().cmd)
	root.AddCommand(newUpgradeCommand().cmd)
	root.AddCommand(newVersionCommand().cmd)

	return root
}

func commandUsesAccountScope(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return true
	}
	switch parts[1] {
	case "accounts", "auth", "commands", "completion", "config", "doctor", "setup", "skill", "upgrade", "version":
		return false
	default:
		return true
	}
}

func Execute() {
	root := newRootCmd()

	err := root.Execute()
	if err != nil {
		err = normalizeCobraError(err)
		if writer == nil {
			writer = output.New(output.Options{
				Format:   output.FormatFromFlags(jsonFlag || jqFlag != "", quietFlag, idsOnly, countFlag, markdownF, styledFlag, agentFlag),
				JQFilter: jqFlag,
			})
		}
		if writer.IsStyled() && strings.HasPrefix(err.Error(), "Usage:") {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(output.ExitCodeFor(err))
		}
		writer.Err(err)
		os.Exit(output.ExitCodeFor(err))
	}
}

func validateJQFlags(cmd *cobra.Command, filter string, requested, ids, count bool) error {
	if !requested {
		return nil
	}
	if filter == "" {
		return output.ErrJQValidation(errors.New("expression cannot be empty"))
	}
	if err := output.ValidateJQFilter(filter); err != nil {
		return err
	}
	if ids {
		return output.ErrJQConflict("--ids-only")
	}
	if count {
		return output.ErrJQConflict("--count")
	}

	switch cmd.CommandPath() {
	case "hey":
		return output.ErrJQNotSupported("the interactive app")
	case "hey auth token":
		return output.ErrJQNotSupported("the auth token command")
	case "hey completion":
		return output.ErrJQNotSupported("the completion command")
	case "hey skill":
		return output.ErrJQNotSupported("the skill display command")
	case "hey tui":
		return output.ErrJQNotSupported("the interactive app")
	default:
		return nil
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

	if err := cfg.SaveBaseURL(old.BaseURL); err != nil {
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
	var requests int
	var latency time.Duration
	if sdkStats != nil {
		requests = sdkStats.RequestCount()
		latency = sdkStats.TotalLatency()
	}
	return output.WithMeta("stats", map[string]any{
		"requests":   requests,
		"latency_ms": latency.Milliseconds(),
	})
}

// writeOK wraps writer.OK and always injects statsOption() so every command
// response includes request stats when --stats is set.
func writeOK(data any, opts ...output.ResponseOption) error {
	return writer.OK(data, append(opts, statsOption())...)
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
	addFlag := func(f *pflag.Flag) {
		flags = append(flags, map[string]string{
			"name":      f.Name,
			"shorthand": f.Shorthand,
			"usage":     f.Usage,
			"default":   f.DefValue,
		})
	}
	cmd.LocalFlags().VisitAll(addFlag)
	cmd.InheritedFlags().VisitAll(addFlag)
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

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	_ = enc.Encode(info)
}
