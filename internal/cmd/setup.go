package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
	cmd *cobra.Command
}

func newSetupCommand() *setupCommand {
	setupCommand := &setupCommand{}
	setupCommand.cmd = &cobra.Command{
		Use:   "setup",
		Short: "Set up HEY for first use",
		Long:  "Sign in and connect your coding agents.",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			"agent_notes": "Runs the first-run wizard: OAuth sign-in, a look at the linked accounts, and coding-agent setup. HEY_NONINTERACTIVE=1 suppresses OAuth and every prompt, but the wizard still connects detected agents (writing skill files) and persists the onboarded flag — there is no read-only mode; use `hey doctor` to inspect without changing anything. --json changes only the output format: a terminal on stdin (an allocated PTY included) still starts browser OAuth and waits for it. Logged out and non-interactive reports status incomplete with a remediation breadcrumb.",
		},
		RunE: setupCommand.run,
	}

	for _, sub := range newSetupAgentCommands() {
		setupCommand.cmd.AddCommand(sub)
	}
	setupCommand.cmd.AddCommand(newSetupAgentsCommand())
	setupCommand.cmd.AddCommand(newSetupOmarchyCommand().cmd)

	return setupCommand
}

func (c *setupCommand) run(cmd *cobra.Command, _ []string) error {
	if err := rejectListOnlyFormats("the setup wizard"); err != nil {
		return err
	}
	return runSetupWizard(cmd, wizardOptions{full: true})
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

// wizardOptions tunes the first-run wizard. full runs every step; a lite run
// (a later logged-out bare `hey` once onboarded) only signs in.
type wizardOptions struct {
	full bool
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
	Agents         []agentCheck      `json:"agents"`
	Issues         []agentIssue      `json:"issues"`
}

// setupWizard carries one wizard run: the command it prints through and what
// it has learned so far.
type setupWizard struct {
	cmd     *cobra.Command
	opts    wizardOptions
	styled  bool
	result  wizardResult
	outcome agentSetupOutcome
}

// confirmAgentSetup is the wizard's one prompt, a seam so tests can answer it.
var confirmAgentSetup = func() (bool, error) {
	return tui.Confirm("  Set up HEY for your coding agents?", true)
}

// runSetupWizard is the entry point shared by `hey setup` and bare `hey`.
func runSetupWizard(cmd *cobra.Command, opts wizardOptions) error {
	wizard := &setupWizard{
		cmd:    cmd,
		opts:   opts,
		styled: writer.IsStyled(),
		result: wizardResult{Version: version.Version, Status: "complete"},
	}
	return wizard.run()
}

func (s *setupWizard) run() error {
	if s.styled {
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

	if s.opts.full {
		s.outcome = s.setupAgents()
	}
	s.result.SkillInstalled = baselineSkillInstalled()
	s.result.Agents = s.outcome.Checks
	s.result.Issues = append(s.result.Issues, s.outcome.Issues...)
	if statusFromOutcome(s.outcome) == "incomplete" {
		s.result.Status = "incomplete"
	}

	s.persistOnboarded()
	return s.summary()
}

// welcome prints the wordmark and a short intro (styled runs only).
func (s *setupWizard) welcome(w io.Writer) {
	fmt.Fprintln(w, tui.RenderWordmark(!colorDisabled))
	fmt.Fprintln(w)
	fmt.Fprintln(w, bold.format("Welcome to HEY"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "The command-line interface for HEY (v%s).\n", version.Version)
	fmt.Fprintln(w, "Let's get you set up. This will only take a moment.")
	fmt.Fprintln(w)
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

	if s.styled {
		w := s.cmd.OutOrStdout()
		fmt.Fprintln(w, bold.format("  Step 1: Sign in"))
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
// logged out). Shared with requireAuth's sign-in prompt.
func loginInteractively(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	logger := func(msg string) {
		for _, line := range strings.Split(strings.Trim(msg, "\n"), "\n") {
			if line == "" {
				fmt.Fprintln(out)
			} else {
				fmt.Fprintln(out, "  "+line)
			}
		}
	}
	if err := authMgr.Login(ctx, auth.LoginOptions{Logger: logger}); err != nil {
		return apierr.ErrAuth(fmt.Sprintf("login failed: %v", err))
	}
	return selectConfiguredAccount(context.Background())
}

// greet looks up who signed in and shows the linked accounts. Read-only:
// HEY is natively multi-account and "all" is the default, so nothing is
// chosen or persisted here.
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
			if s.styled {
				fmt.Fprintln(w, warning.format("  "+issue.Check+" by HEY — "+issue.Hint))
				fmt.Fprintln(w)
			}
			s.result.Status = "incomplete"
			s.result.Issues = append(s.result.Issues, issue)
			return
		}
		if s.styled {
			fmt.Fprintln(w, success.format("  Signed in."))
			fmt.Fprintln(w)
		}
		return
	}

	s.result.Identity = &wizardIdentity{Name: identity.Name, Email: identity.PrimaryContact.EmailAddress}
	s.result.Accounts = linkedAccountList(identity, cfg.AccountID)

	if !s.styled {
		return
	}
	fmt.Fprintln(w, success.format("  "+identityGreeting(identity)))
	// accounts[0] is the "All Accounts" filter; a single linked account
	// needs no list.
	if len(s.result.Accounts) > 2 {
		for _, account := range s.result.Accounts[1:] {
			label := terminal.SanitizeLine(account.Name)
			if account.Email != "" {
				label += " (" + terminal.SanitizeLine(account.Email) + ")"
			}
			fmt.Fprintln(w, muted.format("    • "+label))
		}
		if cfg.AccountID == config.AllAccounts {
			fmt.Fprintln(w, muted.format("    Using All Accounts — hey accounts use <id> to default to one"))
		} else {
			fmt.Fprintln(w, muted.format("    Default mail account: "+cfg.AccountID))
		}
	}
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

// setupAgents offers to connect detected coding agents. In a styled run the
// user confirms first; a machine run just does it — there is nobody to ask.
func (s *setupWizard) setupAgents() agentSetupOutcome {
	agents := harness.DetectedAgents()
	if len(agents) == 0 {
		return agentSetupOutcome{}
	}

	w := s.cmd.OutOrStdout()

	// One pre-setup snapshot drives both the all-good gate and the checklist
	// rendered in the summary for the paths that do not run setup.
	preChecks := snapshotAgentChecks(agents)
	if baselineSkillInstalled() && len(issuesFromChecks(preChecks)) == 0 {
		if s.styled {
			for _, a := range agents {
				fmt.Fprintln(w, statusLine(true, a.Name+" connected"))
			}
			fmt.Fprintln(w)
		}
		return agentSetupOutcome{Checks: preChecks}
	}

	if s.styled {
		fmt.Fprintln(w, bold.format("  Step 2: Coding agents"))
		fmt.Fprintln(w)

		var names []string
		for _, a := range agents {
			names = append(names, a.Name)
		}
		fmt.Fprintf(w, "  Detected: %s\n", joinNames(names))
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  This will:")
		step := 1
		fmt.Fprintln(w, muted.format(fmt.Sprintf("    %d. Install the HEY agent skill to ~/.agents/skills/hey/", step)))
		step++
		for _, a := range agents {
			handler, ok := agentSetupHandlers[a.ID]
			if !ok {
				continue
			}
			for _, label := range handler.Labels {
				fmt.Fprintln(w, muted.format(fmt.Sprintf("    %d. %s", step, label)))
				step++
			}
		}
		fmt.Fprintln(w)

		// The prompt runs only when it can be answered: styled output alone
		// does not prove a human (HEY_NONINTERACTIVE on a PTY, --styled while
		// piped). Without one, proceed with the prompt's default answer —
		// exactly what the machine-mode wizard does.
		if interactiveStdio() {
			install, confirmErr := confirmAgentSetup()
			if confirmErr != nil || !install {
				fmt.Fprintln(w, muted.format("  You can set up agents later:"))
				for _, a := range agents {
					if _, ok := agentSetupHandlers[a.ID]; ok {
						fmt.Fprintln(w, bold.format("    hey setup "+a.ID))
					}
				}
				fmt.Fprintln(w)
				// Skipped carries the snapshot for the checklist but records no
				// issues, so a deliberate skip stays "complete".
				return agentSetupOutcome{Skipped: true, Checks: preChecks}
			}
		}
		fmt.Fprintln(w)
	}

	var issues []agentIssue
	if _, err := installSkillFiles(); err != nil {
		if s.styled {
			fmt.Fprintln(w, warning.format(fmt.Sprintf("  Skill install failed: %s", err)))
		}
		issues = append(issues, agentIssue{Check: "Agent skill", Hint: "Run: hey skill install"})
	} else if s.styled {
		fmt.Fprintln(w, statusLine(true, "Agent skill installed"))
	}

	for _, a := range agents {
		handler, ok := agentSetupHandlers[a.ID]
		if !ok {
			continue
		}
		var handlerErr error
		if s.styled {
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

	// Re-snapshot after setup ran so failed installs surface as issues rather
	// than a silent "complete". The same snapshot renders the checklist, so
	// status and checklist can never disagree.
	postChecks := snapshotAgentChecks(agents)
	issues = append(issues, issuesFromChecks(postChecks)...)

	if s.styled {
		fmt.Fprintln(w)
	}
	return agentSetupOutcome{Checks: postChecks, Issues: issues}
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
	if s.styled {
		showWizardSuccess(s.cmd.OutOrStdout(), s.result, s.outcome)
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

// successHeadline returns the completion banner. When a step left unresolved
// issues the banner is honest about it rather than claiming "Setup complete!".
func successHeadline(status string, issueCount int) string {
	if status != "incomplete" {
		return "Setup complete!"
	}
	if issueCount == 1 {
		return "Setup finished — 1 step needs attention"
	}
	return fmt.Sprintf("Setup finished — %d steps need attention", issueCount)
}

// showWizardSuccess renders the completion checklist, remediation for
// anything that did not complete, and example commands.
func showWizardSuccess(w io.Writer, result wizardResult, outcome agentSetupOutcome) {
	divider := muted.format("─────────────────────────────────")

	headline := success
	if result.Status == "incomplete" {
		headline = warning
	}

	fmt.Fprintln(w, divider)
	fmt.Fprintln(w, headline.format("  "+successHeadline(result.Status, len(result.Issues))))
	fmt.Fprintln(w, divider)
	fmt.Fprintln(w)

	fmt.Fprintln(w, statusLine(!hasAuthIssue(result.Issues), "Signed in"))
	if outcome.Skipped {
		fmt.Fprintln(w, muted.format("  Coding agent setup skipped — run: hey setup"))
	} else {
		for _, check := range outcome.Checks {
			fmt.Fprintln(w, statusLine(check.Status == "pass", check.Name))
		}
	}
	fmt.Fprintln(w)

	if len(result.Issues) > 0 {
		fmt.Fprintln(w, "  Some steps need attention:")
		for _, issue := range result.Issues {
			// Check names usually already carry the agent (e.g. "Claude Code
			// Plugin"); only prefix when they don't.
			label := issue.Check
			if issue.Agent != "" && !strings.HasPrefix(issue.Check, issue.Agent) {
				label = issue.Agent + " — " + issue.Check
			}
			line := "    " + label
			if issue.Hint != "" {
				line += ": " + issue.Hint
			}
			fmt.Fprintln(w, warning.format(line))
		}
		fmt.Fprintln(w, muted.format("    Then verify with: hey doctor"))
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "  Try these commands:")
	fmt.Fprintln(w)
	examples := []struct{ cmd, desc string }{
		{"hey tui", "Open the app"},
		{"hey boxes", "List your boxes"},
		{"hey box imbox", "Read your Imbox"},
		{`hey search "quarterly planning"`, "Search your mail"},
	}
	width := 0
	for _, ex := range examples {
		width = max(width, len(ex.cmd))
	}
	for _, ex := range examples {
		fmt.Fprintf(w, "    %s%s  %s\n", bold.format(ex.cmd), strings.Repeat(" ", width-len(ex.cmd)), muted.format(ex.desc))
	}
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
		return fmt.Sprintf("%s - %s", headline, result.Identity.Email)
	}
	return headline
}

// wizardBreadcrumbs returns next-step breadcrumbs based on wizard outcome.
func wizardBreadcrumbs(result wizardResult) []output.Breadcrumb {
	if hasIssue(result.Issues, "HEY_TOKEN rejected") {
		// HEY_TOKEN outranks anything hey auth login saves: the structured
		// remediation must point at the environment or it loops forever.
		return []output.Breadcrumb{
			{Action: "fix_token", Command: "unset HEY_TOKEN", Description: "Remove or replace the rejected environment token"},
			{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"},
		}
	}
	if hasAuthIssue(result.Issues) {
		return []output.Breadcrumb{
			{Action: "login", Command: "hey auth login", Description: "Authenticate with HEY"},
			{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"},
		}
	}
	crumbs := []output.Breadcrumb{
		{Action: "open", Command: "hey tui", Description: "Open the app"},
		{Action: "boxes", Command: "hey boxes", Description: "List your boxes"},
	}
	if result.Status == "incomplete" {
		crumbs = append(crumbs, output.Breadcrumb{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"})
	}
	return crumbs
}
