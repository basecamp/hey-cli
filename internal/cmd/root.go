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

	"github.com/basecamp/hey-cli/internal/apierr"
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

// runTUI is a seam so tests can observe root routing without a terminal.
var runTUI = tui.Run

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "hey",
		Short:         "Read, send, and organize HEY email, contacts, and calendars from your terminal.",
		Long:          "A CLI for HEY\n" + tui.Wordmark,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			jqRequested := cmd.Flags().Changed("jq")
			format := output.FormatFromFlags(jsonFlag || jqRequested, quietFlag, idsOnly, countFlag, markdownF, styledFlag, agentFlag)
			if htmlOutput {
				format = output.FormatHTML
			}
			writer = output.New(output.Options{
				Format:   format,
				Stdout:   cmd.OutOrStdout(),
				Stderr:   cmd.ErrOrStderr(),
				JQFilter: jqFlag,
			})
			if isHelpReference(cmd) {
				return nil
			}
			if err := validateHTMLFlag(cmd); err != nil {
				return err
			}
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
			if commandIgnoresLocalConfig(cmd) {
				// setup omarchy never fails on configuration: a malformed global
				// file leaves it on the baseline defaults, which is all it needs
				// to edit fixed desktop paths — so --remove still works.
				if cfg, err = config.LoadGlobal(); err != nil {
					cfg, err = config.Defaults(), nil
				}
			} else {
				cfg, err = config.Load()
			}
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

			// The agent-local setup subcommands and skill commands never read
			// credentials and never rewrite the global config, so they defer
			// migration — the installer runs them with HEY_NO_KEYRING=1, which
			// would otherwise move legacy tokens into plaintext. Every other
			// command migrates first: anything that rewrites config.json
			// (config set, trust-local, the wizard's onboarded flag) would
			// silently DELETE legacy embedded credentials otherwise, because
			// fileConfig has no fields to carry them through the rewrite.
			if !commandSkipsCredentialMigration(cmd) {
				migrateOldCredentials(configDir)
			}

			if authMgr.IsAuthenticated() && commandUsesAccountScope(cmd) {
				if err := selectConfiguredAccount(cmd.Context()); err != nil {
					return err
				}
			}

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if !isHelpReference(cmd) {
				maybeRefreshSkills(cmd)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if versionFlag {
				fmt.Fprintf(cmd.OutOrStdout(), "hey version %s\n", version.Version)
				return nil
			}
			// First run: an interactive, logged-out bare `hey` gets the
			// onboarding wizard (lite once onboarded — sign-in only). Every
			// other bare `hey` prints help; the app lives at `hey tui`.
			if interactiveStdio() && !machineReadableOutput(cmd) && !authMgr.IsAuthenticated() {
				return runSetupWizard(cmd, wizardOptions{full: !cfg.Onboarded})
			}
			return cmd.Help()
		},
	}

	root.CompletionOptions.HiddenDefaultCmd = true

	root.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output JSON with metadata")
	root.PersistentFlags().BoolVar(&htmlOutput, "html", false, "Write the original HTML to a pipe or file (thread read, journal read, contact show, contact note show)")
	root.PersistentFlags().BoolVar(&quietFlag, "quiet", false, "Output result data only")
	root.PersistentFlags().BoolVar(&idsOnly, "ids-only", false, "Output only IDs, one per line")
	root.PersistentFlags().BoolVar(&countFlag, "count", false, "Output only the count of results")
	root.PersistentFlags().BoolVar(&markdownF, "markdown", false, "Output Markdown: a table for a listing, a document for a thread")
	root.PersistentFlags().BoolVar(&styledFlag, "styled", false, "Human rendering, bodies as rendered Markdown — the default on a terminal; forces it when piped")
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
	root.AddCommand(newLoginCommand())
	root.AddCommand(newLogoutCommand())
	root.AddCommand(newAccountsCommand().cmd)
	root.AddCommand(newBoxCommand().cmd)
	root.AddCommand(newLabelCommand().cmd)
	root.AddCommand(newCollectionCommand().cmd)
	root.AddCommand(newWorkflowCommand().cmd)
	root.AddCommand(newClipCommand().cmd)
	root.AddCommand(newSnippetCommand().cmd)
	root.AddCommand(newSearchCommand().cmd)
	root.AddCommand(newContactsCommand().cmd)
	root.AddCommand(newScreenerCommand().cmd)
	root.AddCommand(newThreadCommand())
	root.AddCommand(newShareCommand().cmd)
	root.AddCommand(newUnshareCommand().cmd)
	root.AddCommand(newAttachmentCommand())
	root.AddCommand(newReplyCommand().cmd)
	root.AddCommand(newBulkReplyCommand().cmd)
	root.AddCommand(newForwardCommand().cmd)
	root.AddCommand(newComposeCommand().cmd)
	root.AddCommand(newDraftCommand())
	root.AddCommand(newCalendarCommand())
	root.AddCommand(newEventsCommand().cmd)
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
	root.AddCommand(newSetupCommand().cmd)
	root.AddCommand(newTuiCommand().cmd)
	root.AddCommand(newHeyCommand().cmd)
	root.AddCommand(newSkillCommand().cmd)
	root.AddCommand(newHelpTopicCommands()...)
	root.AddCommand(newCommandsCommand())
	root.AddCommand(newShellCompletionCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newConfigCommand().cmd)
	root.AddCommand(newUpgradeCommand().cmd)
	root.AddCommand(newVersionCommand().cmd)
	configureHelpCommand(root)

	return root
}

// commandSkipsCredentialMigration lists the commands safe to run without
// first migrating legacy config.json credentials: they neither use
// credentials nor rewrite the global config file.
func commandSkipsCredentialMigration(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "skill":
		return true
	case "setup":
		return len(parts) > 2 // the wizard itself persists onboarded; subcommands touch only agent files
	default:
		return false
	}
}

func commandUsesAccountScope(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return true
	}
	switch parts[1] {
	case "account", "auth", "commands", "config", "doctor", "login", "logout", "setup", "shell-completion", "skill", "upgrade", "version":
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
			// An error Cobra raised before the pre-run — an unknown flag — gets the
			// writer the pre-run would have built, --html included, so an --html
			// invocation's error reads as text on stderr rather than as JSON.
			format := output.FormatFromFlags(jsonFlag || jqFlag != "", quietFlag, idsOnly, countFlag, markdownF, styledFlag, agentFlag)
			if htmlOutput || htmlRequested(os.Args[1:]) {
				format = output.FormatHTML
			}
			writer = output.New(output.Options{Format: format, JQFilter: jqFlag})
		}
		if writer.IsStyled() && strings.HasPrefix(err.Error(), "Usage:") {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(output.ExitCodeFor(err))
		}
		writer.Err(err)
		os.Exit(output.ExitCodeFor(err))
	}
}

// htmlRequested reports --html among raw arguments. Parsing stops at the first unknown
// flag, so when the error is the flag before --html the parsed boolean is still false;
// the arguments themselves say what was asked for.
func htmlRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--html" || arg == "--html=true" || arg == "--html=1" {
			return true
		}
	}
	return false
}

// htmlCommands are the commands that read something HEY holds as HTML, and so the only
// ones --html means anything to.
var htmlCommands = map[string]bool{
	"hey thread read":       true,
	"hey journal read":      true,
	"hey contact show":      true,
	"hey contact note show": true,
}

// validateHTMLFlag settles what --html may be combined with, before any configuration
// is read or any request made. It is a format of its own, so every other output
// selector conflicts with it; it writes markup, so a terminal is refused with the
// redirect that was meant; and it is only offered by the commands that have HTML.
func validateHTMLFlag(cmd *cobra.Command) error {
	if !htmlOutput {
		return nil
	}
	for _, selector := range []struct {
		set  bool
		flag string
	}{
		{jsonFlag, "--json"},
		{markdownF, "--markdown"},
		{quietFlag, "--quiet"},
		{idsOnly, "--ids-only"},
		{countFlag, "--count"},
		{styledFlag, "--styled"},
		{agentFlag, "--agent"},
		{statsFlag, "--stats"},
		{cmd.Flags().Changed("jq"), "--jq"},
	} {
		if selector.set {
			return apierr.ErrUsage("cannot use --html with " + selector.flag)
		}
	}
	if !htmlCommands[cmd.CommandPath()] {
		return apierr.ErrUsage("--html is not supported by " + cmd.CommandPath())
	}
	if stdoutIsTerminal() {
		return apierr.ErrUsageHint("--html writes raw HTML, which a terminal would show as markup",
			fmt.Sprintf("redirect it to a file or a pipe: %s --html > out.html", cmd.CommandPath()))
	}
	return nil
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
	case "hey auth token":
		return output.ErrJQNotSupported("the auth token command")
	case "hey shell-completion generate":
		return output.ErrJQNotSupported("the shell-completion generate command")
	case "hey setup":
		return output.ErrJQNotSupported("the setup wizard")
	case "hey skill":
		return output.ErrJQNotSupported("the skill display command")
	case "hey tui", "hey hey":
		return output.ErrJQNotSupported("the interactive app")
	default:
		return nil
	}
}

// askToSignIn is the prompt requireAuth shows a logged-out user at a
// terminal. A seam so tests can answer it.
var askToSignIn = func() (bool, error) {
	return tui.Confirm("Not logged in. Sign in now?", true)
}

// requireAuth gates data commands on a login. At an interactive terminal it
// offers to sign in on the spot (prompt and OAuth progress on stderr, so
// stdout stays data); declined, piped or machine-output runs get the auth
// error and exit code 3 instead.
func requireAuth() error {
	if authMgr.IsAuthenticated() {
		return nil
	}
	if writer.IsStyled() && interactiveStdio() {
		if yes, err := askToSignIn(); err == nil && yes {
			return loginInteractively(os.Stderr)
		}
	}
	return apierr.ErrAuth("Not logged in")
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

	// The legacy fields carry their own server: migrate (and later scrub)
	// only when it is the server this run is talking to. With HEY_BASE_URL
	// pointed elsewhere, the current store key says nothing about the legacy
	// credential — saving it there would misfile it, and scrubbing it would
	// delete the only copy for its real origin.
	legacyOrigin := strings.TrimRight(old.BaseURL, "/")
	if legacyOrigin == "" {
		legacyOrigin = strings.TrimRight(config.Defaults().BaseURL, "/")
	}
	if legacyOrigin != credKey {
		return
	}

	if existing, err := store.Load(credKey); err == nil {
		// A prior run already migrated. If its scrub failed, the legacy
		// fields are still in config.json (we just loaded them above) —
		// retry the scrub now, but only when the stored record is actually
		// usable: an empty record must never destroy the one recoverable
		// copy of the credentials.
		if existing.AccessToken != "" || existing.SessionCookie != "" {
			if scrubErr := config.ScrubLegacyCredentials(); scrubErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not remove migrated credentials from config.json: %v\n", scrubErr)
			}
		}
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
		// Leave the legacy fields in place: config rewrites preserve keys
		// they do not own, so the credentials survive until a later run
		// migrates them successfully.
		fmt.Fprintf(os.Stderr, "warning: could not migrate credentials: %v\n", err)
		return
	}

	// Only after the store confirmed the save do the embedded secrets leave
	// the config file.
	if err := config.ScrubLegacyCredentials(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove migrated credentials from config.json: %v\n", err)
	}
	if err := cfg.SaveBaseURL(old.BaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update config after migration: %v\n", err)
	}

	fmt.Fprintln(os.Stderr, "notice: credentials migrated from config.json to credential store")
}

func normalizeCobraError(err error) error {
	var e *apierr.Error
	if errors.As(err, &e) {
		return err
	}
	if isCobraParseError(err) {
		return apierr.ErrUsageHint(err.Error(), "Run 'hey --help' for usage information")
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
	if usage, ok := cmd.Annotations[compatibilityUsageAnnotation]; ok {
		info["compatibility_usage"] = usage
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
