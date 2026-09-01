package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/tui"
	"github.com/basecamp/hey-cli/internal/version"
)

type setupCommand struct {
	cmd           *cobra.Command
	skipAgents    bool
	skipOmarchy   bool
	silentSuccess bool
}

func newSetupCommand() *setupCommand {
	setupCommand := &setupCommand{}
	setupCommand.cmd = &cobra.Command{
		Use:   "setup",
		Short: "Set up HEY for first use",
		Long: `Sign in, connect detected coding agents, and install the Omarchy integration when
applicable. Use --skip-agents or --skip-omarchy to leave either integration unchanged.
--silent-success keeps authentication visible, shows an installation spinner, and ends
with SETUP COMPLETE. Machine and non-interactive runs never touch the desktop —
hey setup omarchy does that explicitly. Set HEY_SETUP_VERBOSE=1 to show the detailed setup checklist, agent
installation progress, Omarchy status, and keybinding hint.`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"agent_notes": "Runs the first-run wizard: OAuth sign-in, a look at the linked accounts, coding-agent setup, and the Omarchy integration when detected. --skip-agents and --skip-omarchy leave those integrations unchanged. --silent-success keeps required authentication and failure output, shows an installation spinner, and ends with SETUP COMPLETE. HEY_NONINTERACTIVE=1 suppresses interactive sign-in, but unskipped agent setup still writes skill files and the wizard persists the onboarded flag — there is no read-only mode; use `hey doctor` to inspect without changing anything. --json changes only the output format: a terminal on stdin (an allocated PTY included) still starts browser OAuth and waits for it. Logged out and non-interactive reports status incomplete with a remediation breadcrumb.",
		},
		RunE: setupCommand.run,
	}

	for _, sub := range newSetupAgentCommands() {
		setupCommand.cmd.AddCommand(sub)
	}
	setupCommand.cmd.AddCommand(newSetupAgentsCommand())
	setupCommand.cmd.AddCommand(newSetupOmarchyCommand().cmd)
	setupCommand.cmd.Flags().BoolVar(&setupCommand.skipAgents, "skip-agents", false, "Leave coding-agent integrations unchanged")
	setupCommand.cmd.Flags().BoolVar(&setupCommand.skipOmarchy, "skip-omarchy", false, "Leave the Omarchy integration unchanged")
	setupCommand.cmd.Flags().BoolVar(&setupCommand.silentSuccess, "silent-success", false, "Show setup activity and end a successful run with SETUP COMPLETE")

	return setupCommand
}

func (c *setupCommand) run(cmd *cobra.Command, _ []string) error {
	if err := rejectListOnlyFormats("the setup wizard"); err != nil {
		return err
	}
	if c.silentSuccess && (!writer.IsStyled() || !interactiveStdio()) {
		return apierr.ErrUsage("--silent-success requires an interactive terminal with styled output")
	}
	return runSetupWizard(cmd, wizardOptions{
		full:          true,
		skipAgents:    c.skipAgents,
		skipOmarchy:   c.skipOmarchy,
		silentSuccess: c.silentSuccess,
	})
}

// rejectListOnlyFormats fails fast on --ids-only and --count: setup results
// are not list data, and the writer's late refusal would otherwise land
// after OAuth ran, agents were connected and onboarded was persisted —
// side effects dressed up as a failed command.
func rejectListOnlyFormats(command string) error {
	switch writer.EffectiveFormat() {
	case output.FormatIDs:
		return apierr.ErrUsage("--ids-only is not supported by " + command)
	case output.FormatCount:
		return apierr.ErrUsage("--count is not supported by " + command)
	default:
		return nil
	}
}

const setupVerboseEnv = "HEY_SETUP_VERBOSE"

var setupSpinnerInterval = 80 * time.Millisecond

// wizardOptions tunes the first-run wizard. A full run performs every
// unskipped step; a lite run (a later logged-out bare `hey` once onboarded)
// only signs in.
type wizardOptions struct {
	full          bool
	skipAgents    bool
	skipOmarchy   bool
	silentSuccess bool
}

// wizardIdentity is the signed-in HEY identity reported in the envelope.
type wizardIdentity struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// wizardResult is the wizard's outcome, rendered as prose in a terminal and
// as the envelope otherwise.
type wizardResult struct {
	Version        string            `json:"version"`
	Status         string            `json:"status"` // "complete" or "incomplete"
	Identity       *wizardIdentity   `json:"identity,omitempty"`
	Accounts       []accountListItem `json:"accounts,omitempty"`
	SkillInstalled bool              `json:"skill_installed"`
	// AgentsSkipped reports that --skip-agents left coding-agent integrations unchanged.
	AgentsSkipped bool `json:"agents_skipped,omitempty"`
	// OmarchySkipped reports that --skip-omarchy left the desktop integration unchanged.
	OmarchySkipped bool `json:"omarchy_skipped,omitempty"`
	// CompletionsInstalled reports the shell completion step, which asks
	// nothing and fails quietly — a shell hey cannot write to is not a reason
	// for setup to be incomplete.
	CompletionsInstalled bool         `json:"completions_installed"`
	Agents               []agentCheck `json:"agents"`
	Issues               []agentIssue `json:"issues"`
	// Omarchy reports the desktop step, which runs only in a full, styled,
	// interactive, signed-in wizard on Omarchy — machine and non-interactive
	// runs skip it and are pointed at `hey setup omarchy` instead.
	Omarchy *omarchyOutcome `json:"omarchy,omitempty"`
}

// omarchyOutcome is the wizard's Omarchy step, mirrored into the envelope.
type omarchyOutcome struct {
	Steps []omarchyStep `json:"steps"`
}

// setupWizard carries one wizard run: the command it prints through and what
// it has learned so far.
type setupWizard struct {
	cmd      *cobra.Command
	opts     wizardOptions
	styled   bool
	verbose  bool
	nextStep int
	result   wizardResult
	outcome  agentSetupOutcome
}

// runSetupWizard is the entry point shared by `hey setup` and bare `hey`.
func runSetupWizard(cmd *cobra.Command, opts wizardOptions) error {
	wizard := &setupWizard{
		cmd:      cmd,
		opts:     opts,
		styled:   writer.IsStyled(),
		verbose:  os.Getenv(setupVerboseEnv) == "1",
		nextStep: 1,
		result:   wizardResult{Version: version.Version, Status: "complete"},
	}
	return wizard.run()
}

func (s *setupWizard) run() error {
	if s.narrates() {
		s.welcome(s.cmd.OutOrStdout())
	}

	signedIn, err := s.signIn()
	if err != nil {
		return err
	}
	if signedIn {
		s.greet()
	} else {
		s.result.Status = "incomplete"
		s.result.Issues = append(s.result.Issues, agentIssue{Check: "Not logged in", Hint: "Run: hey auth login"})
	}

	stopSilentSpinner := func() {}
	if s.opts.silentSuccess {
		stopSilentSpinner = startSetupSpinner(s.cmd.OutOrStdout(), "Installing HEY…", true)
	}
	defer stopSilentSpinner()

	if s.opts.full {
		s.result.CompletionsInstalled = s.installCompletions()
		if s.opts.skipAgents {
			s.result.AgentsSkipped = true
		} else {
			s.outcome = s.setupAgents()
		}
	} else if signedIn {
		// A lite wizard that just signed in gets the same one-line Omarchy
		// hook as `hey auth login`; the full wizard runs the real step below.
		ensureOmarchyBarPluginAfterLogin(s.cmd.ErrOrStderr())
	}
	s.result.SkillInstalled = baselineSkillInstalled()
	s.result.Agents = s.outcome.Checks
	s.result.Issues = append(s.result.Issues, s.outcome.Issues...)
	if statusFromOutcome(s.outcome) == "incomplete" {
		s.result.Status = "incomplete"
	}
	if s.opts.full {
		if s.opts.skipOmarchy {
			s.result.OmarchySkipped = true
		} else {
			s.setupOmarchy(signedIn)
		}
	}

	stopSilentSpinner()
	s.persistOnboarded()
	return s.summary()
}

// welcome prints the wordmark and a short intro (styled runs only).
func (s *setupWizard) welcome(w io.Writer) {
	fmt.Fprintln(w, tui.RenderWordmark(!colorDisabled))
	fmt.Fprintln(w)
	fmt.Fprintln(w, bold.format("HEY! It's the command-line interface!"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Let's get you set up. It'll only take a moment")
	fmt.Fprintln(w)
}

// printStep gives each displayed setup stage the next consecutive number.
func (s *setupWizard) printStep(w io.Writer, label string) {
	fmt.Fprintln(w, bold.format(fmt.Sprintf("Step %d: %s", s.nextStep, label)))
	s.nextStep++
}

func (s *setupWizard) narrates() bool {
	return s.styled && !s.opts.silentSuccess
}

// signIn makes sure we are authenticated. Reports whether we are. Sign-in
// runs only when somebody can see it through: stdin is a terminal and
// HEY_NONINTERACTIVE is not engaged. Otherwise — a piped `hey setup --json`,
// an agent harness on a pseudo-terminal — it reports "not logged in" instead
// of parking a six-minute browser wait.
func (s *setupWizard) signIn() (bool, error) {
	if authMgr.IsAuthenticated() {
		return true, nil
	}
	if !canSignInInteractively() {
		return false, nil
	}

	if s.narrates() {
		w := s.cmd.OutOrStdout()
		s.printStep(w, "Sign in")
		fmt.Fprintln(w)
		if err := loginInteractively(w); err != nil {
			return false, err
		}
		fmt.Fprintln(w)
		return true, nil
	}

	if err := loginInteractively(s.cmd.ErrOrStderr()); err != nil {
		return false, err
	}
	return true, nil
}

// canSignInInteractively reports whether the OAuth flow may start: a human on
// stdin, prompts not disabled. Deliberately looser than interactiveStdio() —
// `hey setup --json` from a terminal still signs in, with progress on stderr.
func canSignInInteractively() bool {
	return stdinIsTerminal() && !config.NonInteractiveEnv()
}

// loginInteractively runs the OAuth flow with progress on out, then selects
// the configured mail account (PersistentPreRunE skipped that while we were
// logged out). Shared with requireAuth's sign-in prompt; a var so tests can
// stand in for the browser wait.
var loginInteractively = func(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	logger := func(msg string) {
		for _, line := range strings.Split(strings.Trim(msg, "\n"), "\n") {
			if line == "" {
				fmt.Fprintln(out)
			} else {
				fmt.Fprintln(out, line)
			}
		}
	}
	if err := authMgr.Login(ctx, auth.LoginOptions{Logger: logger}); err != nil {
		return apierr.ErrAuth(fmt.Sprintf("login failed: %v", err))
	}
	// Replaced credentials orphan whatever the old ones cached.
	clearHTTPCache(out)
	return selectConfiguredAccount(context.Background())
}

// greet records the signed-in identity and linked accounts, then confirms
// the identity in styled output. Nothing is chosen or persisted here.
func (s *setupWizard) greet() {
	w := s.cmd.OutOrStdout()

	identity, err := rootSDK.Identity().GetIdentity(s.cmd.Context())
	if err != nil || identity == nil {
		// Stored credentials that HEY rejects mean we are not signed in at
		// all, however present they are — surface that instead of reporting
		// a complete setup that cannot run a single data command. Other
		// failures (offline, transient) keep the best-effort greeting.
		if isAuthError(err) {
			// The remediation must match the credential actually in use:
			// HEY_TOKEN outranks anything hey auth login saves, so sending
			// the user there would repeat the same failure forever.
			issue := agentIssue{Check: "Stored sign-in rejected", Hint: "Run: hey auth login"}
			if os.Getenv("HEY_TOKEN") != "" {
				issue = agentIssue{Check: "HEY_TOKEN rejected", Hint: "Update or unset HEY_TOKEN"}
			}
			if s.narrates() {
				fmt.Fprintln(w, warning.format(issue.Check+" by HEY — "+issue.Hint))
				fmt.Fprintln(w)
			}
			s.result.Status = "incomplete"
			s.result.Issues = append(s.result.Issues, issue)
			return
		}
		if s.narrates() {
			fmt.Fprintln(w, success.format("Signed in."))
			fmt.Fprintln(w)
		}
		return
	}

	s.result.Identity = &wizardIdentity{Name: identity.Name, Email: identity.PrimaryContact.EmailAddress}
	s.result.Accounts = linkedAccountList(identity, cfg.AccountID)

	if !s.narrates() {
		return
	}
	fmt.Fprintln(w, success.format(identityGreeting(identity)))
	fmt.Fprintln(w)
}

// isAuthError reports whether err is HEY rejecting our credentials.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	converted := apierr.FromSDK(err)
	var cliErr *apierr.Error
	return errors.As(converted, &cliErr) && cliErr.Code == apierr.CodeAuth
}

func identityGreeting(identity *generated.Identity) string {
	name := terminal.SanitizeLine(identity.Name)
	email := terminal.SanitizeLine(identity.PrimaryContact.EmailAddress)
	switch {
	case name != "" && email != "":
		return fmt.Sprintf("Signed in as %s (%s)", name, email)
	case email != "":
		return "Signed in as " + email
	case name != "":
		return "Signed in as " + name
	default:
		return "Signed in."
	}
}

// installCompletions puts shell completions where the running shell reads
// them from. It asks nothing: an install through mise, the installer script or
// a tarball registers completions nowhere, and a user who never learns they
// were missing is exactly who this step is for. Every refusal — a shell hey
// cannot name, a completion file somebody else wrote — is left to
// `hey shell-completion install` to explain, and never fails setup.
func (s *setupWizard) installCompletions() bool {
	env := completionEnvResolver()
	shell, err := env.resolveShell(nil)
	if err != nil {
		return false
	}
	if packagedCompletionFinder(shell) != "" {
		return false
	}
	target, err := env.target(shell)
	if err != nil {
		return false
	}
	script, err := env.script(s.cmd.Root(), shell)
	if err != nil {
		return false
	}
	if _, err := installCompletion(target, script, false); err != nil {
		return false
	}

	if s.narrates() {
		w := s.cmd.OutOrStdout()
		fmt.Fprintln(w, statusLine(true, "Shell completions installed for "+shell))
		if target.Hint != "" {
			fmt.Fprintln(w, target.Hint)
		}
		fmt.Fprintln(w)
	}
	return true
}

// setupAgents connects every detected coding agent. Styled runs narrate the
// work while machine runs report the same outcome in their envelope.
func (s *setupWizard) setupAgents() agentSetupOutcome {
	agents := harness.DetectedAgents()
	if len(agents) == 0 {
		return agentSetupOutcome{}
	}

	w := s.cmd.OutOrStdout()
	if s.narrates() {
		s.printStep(w, "Coding agents")
		fmt.Fprintln(w)
	}

	// One pre-setup snapshot drives both the all-good gate and the checklist
	// rendered in the summary for the paths that do not run setup.
	preChecks := snapshotAgentChecks(agents)
	if baselineSkillInstalled() && len(issuesFromChecks(preChecks)) == 0 {
		if s.narrates() {
			for _, a := range agents {
				fmt.Fprintln(w, statusLine(true, a.Name+" connected"))
			}
			fmt.Fprintln(w)
		}
		return agentSetupOutcome{Checks: preChecks}
	}

	if s.narrates() {
		var names []string
		for _, a := range agents {
			names = append(names, a.Name)
		}
		fmt.Fprintf(w, "Detected: %s\n", joinNames(names))
		fmt.Fprintln(w)
		if s.verbose {
			fmt.Fprintln(w, "This will:")
			step := 1
			fmt.Fprintf(w, "%d. Install the HEY agent skill to ~/.agents/skills/hey/\n", step)
			step++
			for _, a := range agents {
				handler, ok := agentSetupHandlers[a.ID]
				if !ok {
					continue
				}
				for _, label := range handler.Labels {
					fmt.Fprintf(w, "%d. %s\n", step, label)
					step++
				}
			}
			fmt.Fprintln(w)
		}
	}

	stopSpinner := startSetupSpinner(w, "Installing agent skill…", s.narrates() && !s.verbose && interactiveStdio())
	_, skillErr := installSkillFiles()
	var issues []agentIssue
	if skillErr != nil {
		issues = append(issues, agentIssue{Check: "Agent skill", Hint: "Run: hey skill install"})
	}
	if s.narrates() && s.verbose {
		printSkillInstallResult(w, skillErr)
	}

	for _, a := range agents {
		handler, ok := agentSetupHandlers[a.ID]
		if !ok {
			continue
		}
		var handlerErr error
		if s.narrates() && s.verbose {
			if handler.Run != nil {
				handlerErr = handler.Run(s.cmd) // interactive handlers warn and continue
			}
		} else if handler.RunNonInteractive != nil {
			handlerErr = handler.RunNonInteractive(s.cmd)
		}
		// A handler failure is an issue in its own right: the snapshot below
		// catches most of them, but not every refusal leaves a failing check.
		if handlerErr != nil {
			issues = append(issues, agentIssue{Agent: a.Name, Check: a.Name + " setup failed", Hint: handlerErr.Error()})
		}
	}
	stopSpinner()
	if s.narrates() && !s.verbose {
		printSkillInstallResult(w, skillErr)
	}

	// Re-snapshot after setup ran so failed installs surface as issues rather
	// than a silent "complete". The same snapshot renders the checklist, so
	// status and checklist can never disagree.
	postChecks := snapshotAgentChecks(agents)
	issues = append(issues, issuesFromChecks(postChecks)...)

	if s.narrates() {
		fmt.Fprintln(w)
	}
	return agentSetupOutcome{Checks: postChecks, Issues: issues}
}

func printSkillInstallResult(w io.Writer, err error) {
	if err != nil {
		fmt.Fprintln(w, warning.format(fmt.Sprintf("Skill install failed: %s", err)))
		return
	}
	fmt.Fprintln(w, statusLine(true, "Agent skill installed"))
}

// startSetupSpinner animates one terminal line while concise agent setup runs.
// stop clears that line so the durable success or failure status replaces it.
func startSetupSpinner(w io.Writer, label string, enabled bool) func() {
	if !enabled {
		return func() {}
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	done := make(chan struct{})
	stopped := make(chan struct{})
	fmt.Fprintf(w, "\r%s %s", frames[0], label)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(setupSpinnerInterval)
		defer ticker.Stop()
		frame := 1
		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(w, "\r%s %s", frames[frame%len(frames)], label)
				frame++
			case <-done:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
			fmt.Fprint(w, "\r\x1b[2K")
		})
	}
}

// setupOmarchy installs the Omarchy desktop pieces and enables the bar plugin
// in a full, styled, interactive, signed-in wizard. Machine and
// non-interactive runs skip it entirely; the envelope points at
// hey setup omarchy instead (wizardBreadcrumbs).
func (s *setupWizard) setupOmarchy(signedIn bool) {
	// Stored-but-rejected credentials are not a login: greet recorded the
	// auth issue, and the desktop step waits for a sign-in that works.
	if !signedIn || hasAuthIssue(s.result.Issues) || !s.styled || !interactiveStdio() {
		return
	}
	env := liveOmarchyEnv()
	if !env.detected() || !filepath.IsAbs(env.home) {
		return
	}
	w := s.cmd.OutOrStdout()
	steps := omarchySetup{env: env, forcePlugin: true}.apply()
	s.result.Omarchy = &omarchyOutcome{Steps: steps}
	if s.narrates() {
		s.printStep(w, "Omarchy desktop")
		fmt.Fprintln(w)
	}

	failed := false
	for _, step := range steps {
		if step.Status == "failed" || (step.Status == "skipped" && step.attempted) {
			failed = true
			s.result.Status = "incomplete"
			s.result.Issues = append(s.result.Issues, agentIssue{Check: "Omarchy " + step.Name, Hint: "Run: hey setup omarchy"})
		}
	}
	if s.narrates() {
		bar := stepNamed(steps, "bar plugin")
		switch {
		case bar.Status == "failed" || (bar.Status == "skipped" && bar.attempted):
			fmt.Fprintln(w, warning.format("Bar plugin: "+bar.Detail))
		case failed:
			fmt.Fprintln(w, statusLine(false, "Omarchy desktop setup needs attention"))
		case bar.Status == "installed":
			fmt.Fprintln(w, statusLine(true, "HEY is in your Omarchy bar"))
		default:
			fmt.Fprintln(w, statusLine(true, "Omarchy desktop connected"))
		}
		if s.verbose {
			fmt.Fprintln(w)
			fmt.Fprintln(w, strings.TrimRight(omarchyKeybindHint, "\n"))
		}
		fmt.Fprintln(w)
	}
}

// persistOnboarded records that the wizard ran, so a later logged-out bare
// `hey` runs the lite wizard. Best-effort: a read-only config dir only costs
// a repeat of the agent step next time.
func (s *setupWizard) persistOnboarded() {
	if cfg.Onboarded {
		return
	}
	if err := cfg.SaveOnboarded(true); err != nil {
		fmt.Fprintf(s.cmd.ErrOrStderr(), "warning: could not save onboarding state: %v\n", err)
	}
}

// summary closes the wizard: a checklist and next steps in a terminal, the
// envelope otherwise.
func (s *setupWizard) summary() error {
	if s.opts.silentSuccess {
		w := s.cmd.OutOrStdout()
		if s.result.Status == "complete" {
			fmt.Fprintln(w, success.format("SETUP COMPLETE"))
			return nil
		}
		fmt.Fprintln(w, warning.format("SETUP INCOMPLETE"))
		fmt.Fprintln(w)
		printWizardIssues(w, s.result.Issues)
		return nil
	}
	if s.styled {
		showWizardSuccess(s.cmd.OutOrStdout(), s.result, s.outcome, s.verbose, s.nextStep)
		return nil
	}
	if s.result.Agents == nil {
		s.result.Agents = []agentCheck{}
	}
	if s.result.Issues == nil {
		s.result.Issues = []agentIssue{}
	}
	return writeOK(s.result,
		output.WithSummary(wizardSummaryLine(s.result)),
		output.WithBreadcrumbs(wizardBreadcrumbs(s.result)...),
	)
}

// showWizardSuccess renders an optional detailed checklist, remediation for
// incomplete steps, and the commands that finish the setup flow.
func showWizardSuccess(w io.Writer, result wizardResult, outcome agentSetupOutcome, verbose bool, nextStep int) {
	if verbose {
		fmt.Fprintln(w, statusLine(!hasAuthIssue(result.Issues), "Signed in"))
		if result.AgentsSkipped {
			fmt.Fprintln(w, "Coding agent setup skipped")
		} else if outcome.Skipped {
			fmt.Fprintln(w, "Coding agent setup skipped — run: hey setup")
		} else {
			for _, check := range outcome.Checks {
				fmt.Fprintln(w, statusLine(check.Status == "pass", check.Name))
			}
		}
		if result.OmarchySkipped {
			fmt.Fprintln(w, "Omarchy setup skipped")
		} else if result.Omarchy != nil {
			ok := true
			for _, step := range result.Omarchy.Steps {
				// The same predicate that files the issue: a checkmark beside a
				// step listed under "needs attention" would contradict itself.
				if step.Status == "failed" || step.failure != nil || (step.Status == "skipped" && step.attempted) {
					ok = false
				}
			}
			fmt.Fprintln(w, statusLine(ok, "Omarchy desktop"))
			bar := stepNamed(result.Omarchy.Steps, "bar plugin")
			barLine := bar.Status
			if bar.Detail != "" {
				barLine += " — " + bar.Detail
			}
			fmt.Fprintln(w, "Bar plugin: "+barLine)
			desktop := "launcher entry, menu row and theme template in place"
			for _, step := range result.Omarchy.Steps {
				if step.Name != "bar plugin" && step.Status == "failed" {
					desktop = step.Name + " failed: " + step.Detail
				}
			}
			fmt.Fprintln(w, "Desktop: "+desktop)
		}
		fmt.Fprintln(w)
	}

	printWizardIssues(w, result.Issues)

	title := "Try it out!"
	if nextStep > 1 {
		title = fmt.Sprintf("Step %d: Try it out!", nextStep)
	}
	fmt.Fprintln(w, bold.format(title))
	fmt.Fprintln(w)
	examples := []struct{ cmd, desc string }{
		{"hey hey", "Open TUI"},
		{"hey box list", "List your boxes"},
		{"hey box view imbox", "Read your Imbox"},
		{`hey search "quarterly planning"`, "Search your mail"},
	}
	width := 0
	for _, ex := range examples {
		width = max(width, len(ex.cmd))
	}
	for _, ex := range examples {
		fmt.Fprintf(w, "%s%s  %s\n", ex.cmd, strings.Repeat(" ", width-len(ex.cmd)), italicPlain.format(ex.desc))
	}
	fmt.Fprintln(w)
}

func printWizardIssues(w io.Writer, issues []agentIssue) {
	if len(issues) == 0 {
		return
	}
	fmt.Fprintln(w, "Some steps need attention:")
	for _, issue := range issues {
		// Check names usually already carry the agent (e.g. "Claude Code
		// Plugin"); only prefix when they don't.
		label := issue.Check
		if issue.Agent != "" && !strings.HasPrefix(issue.Check, issue.Agent) {
			label = issue.Agent + " — " + issue.Check
		}
		line := label
		if issue.Hint != "" {
			line += ": " + issue.Hint
		}
		fmt.Fprintln(w, warning.format(line))
	}
	fmt.Fprintln(w, "Then verify with: hey doctor")
	fmt.Fprintln(w)
}

func hasIssue(issues []agentIssue, check string) bool {
	for _, issue := range issues {
		if issue.Check == check {
			return true
		}
	}
	return false
}

// hasAuthIssue reports whether the wizard could not establish a working
// login — missing or rejected. Both mean the same thing to a caller: repair
// authentication before anything else.
func hasAuthIssue(issues []agentIssue) bool {
	return hasIssue(issues, "Not logged in") || hasIssue(issues, "Stored sign-in rejected") || hasIssue(issues, "HEY_TOKEN rejected")
}

// wizardSummaryLine builds a concise summary for the output envelope.
func wizardSummaryLine(result wizardResult) string {
	headline := "Setup complete"
	if result.Status == "incomplete" {
		headline = "Setup finished with issues"
	}
	if result.Identity != nil && result.Identity.Email != "" {
		return fmt.Sprintf("%s - %s", headline, terminal.SanitizeLine(result.Identity.Email))
	}
	return headline
}

// wizardBreadcrumbs returns next-step breadcrumbs based on wizard outcome.
func wizardBreadcrumbs(result wizardResult) []output.Breadcrumb {
	var crumbs []output.Breadcrumb
	switch {
	case hasIssue(result.Issues, "HEY_TOKEN rejected"):
		// HEY_TOKEN outranks anything hey auth login saves: the structured
		// remediation must point at the environment or it loops forever.
		crumbs = []output.Breadcrumb{
			{Action: "fix_token", Command: "unset HEY_TOKEN", Description: "Remove or replace the rejected environment token"},
			{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"},
		}
	case hasAuthIssue(result.Issues):
		crumbs = []output.Breadcrumb{
			{Action: "login", Command: "hey auth login", Description: "Authenticate with HEY"},
			{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"},
		}
	default:
		crumbs = []output.Breadcrumb{
			{Action: "open", Command: "hey tui", Description: "Open the app"},
			{Action: "boxes", Command: "hey box list", Description: "List your boxes"},
		}
		if result.Status == "incomplete" {
			crumbs = append(crumbs, output.Breadcrumb{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"})
		}
	}
	// A machine or non-interactive run on Omarchy never touches the desktop
	// itself — logged out included, where the automatic hook can never run —
	// so the explicit command rides along in every branch.
	if result.Omarchy == nil && !result.OmarchySkipped && liveOmarchyEnv().detected() {
		crumbs = append(crumbs, output.Breadcrumb{Action: "omarchy", Command: "hey setup omarchy", Description: "Put HEY in your Omarchy bar"})
	}
	return crumbs
}
