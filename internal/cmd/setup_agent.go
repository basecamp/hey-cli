package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
)

// agentSetupHandler describes what a single agent's setup step does and how to run it.
type agentSetupHandler struct {
	Labels            []string                   // what this will do, for the wizard's "This will:" list
	Run               func(*cobra.Command) error // interactive setup: prints progress, warns and continues
	RunNonInteractive func(*cobra.Command) error // non-interactive setup: silent, returns the failure
}

// agentSetupError is a setup failure with manual remediation commands the
// user (or agent) can run themselves.
type agentSetupError struct {
	Summary string
	Manual  []string
}

func (e *agentSetupError) Error() string { return e.Summary }

// agentCheck is one agent health check captured in a single post-setup
// snapshot. Both the completion status and the rendered checklist derive
// from the same snapshot so they can never disagree.
type agentCheck struct {
	Agent  string `json:"agent"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Hint   string `json:"hint,omitempty"`
}

// agentIssue is a single unresolved problem after agent setup ran. Hint
// carries the failing check's own remediation so reporting stays specific.
type agentIssue struct {
	Agent string `json:"agent,omitempty"`
	Check string `json:"check"`
	Hint  string `json:"hint,omitempty"`
}

// agentSetupOutcome reports what happened during the agent-setup step.
// Issues are authoritative for the wizard's completion status; Skipped is
// metadata only (a deliberate skip records no issues, so it stays complete).
type agentSetupOutcome struct {
	Skipped bool
	Checks  []agentCheck
	Issues  []agentIssue
}

// agentSetupHandlers maps agent ID → setup handler.
var agentSetupHandlers = map[string]agentSetupHandler{
	"claude": {
		Labels: []string{
			"Add the " + harness.ClaudeMarketplaceSource + " marketplace to Claude Code",
			"Install the " + harness.ClaudeExpectedPluginKey + " plugin for Claude Code",
			"Link the skill into ~/.claude/skills/hey",
		},
		Run:               runClaudeSetup,
		RunNonInteractive: runClaudeSetupNonInteractive,
	},
	"codex": {
		Labels: []string{
			"Install the shared HEY skill for Codex",
		},
		Run:               runCodexSetup,
		RunNonInteractive: runCodexSetupNonInteractive,
	},
}

// runAgentCommand is the subprocess seam for agent CLIs (claude, codex) so
// tests never spawn a real one. Output is captured, not streamed: the wizard
// prints its own status lines and surfaces the tool's output only on failure.
var runAgentCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name comes from harness.Find*Binary
	command.Env = commandEnvironment(os.Environ(), agentCommandEnvironment(ctx))
	// Bound Wait, not just the process: a wrapper script's grandchild can hold
	// the output pipes open after the timeout kills the child.
	command.WaitDelay = time.Second
	return command.CombinedOutput()
}

type agentCommandEnvironmentKey struct{}

func agentCommandEnvironment(ctx context.Context) []string {
	env, _ := ctx.Value(agentCommandEnvironmentKey{}).([]string)
	return env
}

// commandEnvironment applies command-scoped overrides without changing the
// process environment shared by concurrent commands.
func commandEnvironment(base, overrides []string) []string {
	env := append([]string(nil), base...)
	for _, override := range overrides {
		key, _, ok := strings.Cut(override, "=")
		if !ok {
			continue
		}
		replaced := false
		for i, value := range env {
			existingKey, _, hasValue := strings.Cut(value, "=")
			if hasValue && strings.EqualFold(existingKey, key) {
				env[i] = override
				replaced = true
			}
		}
		if !replaced {
			env = append(env, override)
		}
	}
	return env
}

const (
	// claudeMarketplaceTimeout bounds marketplace add/update so a hung clone
	// can't stall setup indefinitely.
	claudeMarketplaceTimeout = 60 * time.Second
	claudeInstallTimeout     = 120 * time.Second
)

// newSetupAgentCommands generates `hey setup <agent>` from the registry.
func newSetupAgentCommands() []*cobra.Command {
	var cmds []*cobra.Command
	for _, a := range harness.AllAgents() {
		agent := a
		handler, ok := agentSetupHandlers[agent.ID]
		if !ok {
			continue
		}
		cmds = append(cmds, &cobra.Command{
			Use:   agent.ID,
			Short: fmt.Sprintf("Connect %s to HEY", agent.Name),
			Long:  fmt.Sprintf("Install the HEY agent skill and set up the %s integration so %s can work with your HEY mail.", agent.Name, agent.Name),
			Args:  cobra.NoArgs,
			Annotations: map[string]string{
				"agent_notes": "Non-interactive when output is piped or --json is set. Succeeds with {plugin_installed, agent_detected}; a not-detected or not-connected outcome is an error envelope (code setup_incomplete, manual steps in the hint) with a nonzero exit.",
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runSetupAgent(cmd, agent, handler)
			},
		})
	}
	return cmds
}

func runSetupAgent(cmd *cobra.Command, agent harness.AgentInfo, handler agentSetupHandler) error {
	if err := rejectListOnlyFormats("hey setup " + agent.ID); err != nil {
		return err
	}
	_, skillErr := installSkillFiles()

	var setupErrors, manualCommands []string
	if skillErr != nil {
		setupErrors = append(setupErrors, fmt.Sprintf("skill install: %s", skillErr))
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		if skillErr != nil {
			fmt.Fprintln(w, warning.format(fmt.Sprintf("Skill install failed: %s", skillErr)))
		} else {
			fmt.Fprintln(w, statusLine(true, "Agent skill installed"))
		}
		if handler.Run != nil {
			if err := handler.Run(cmd); err != nil {
				return err
			}
		}
		// Interactive handlers warn and continue (the wizard needs that), so
		// the verdict comes from a fresh health snapshot, not from the
		// handler's return value: never tell the user to start a session
		// against an integration that is not connected.
		if skillErr == nil && agent.Detect != nil && agent.Detect() && agentChecksPass(agent) {
			fmt.Fprintln(w, "Start a new "+agent.Name+" session to use HEY commands.")
			return nil
		}
		return &apierr.Error{
			Code:    "setup_incomplete",
			Message: agent.Name + " not connected",
			Hint:    "Run: hey doctor",
		}
	}

	if handler.RunNonInteractive != nil {
		if err := handler.RunNonInteractive(cmd); err != nil {
			setupErrors = append(setupErrors, err.Error())
			var setupErr *agentSetupError
			if errors.As(err, &setupErr) {
				manualCommands = setupErr.Manual
			}
		}
	}

	detected := agent.Detect != nil && agent.Detect()
	installed := detected && agentChecksPass(agent)

	summary := agent.Name + " connected"
	switch {
	case !detected:
		summary = agent.Name + " not detected"
	case !installed:
		summary = agent.Name + " not connected"
	}

	result := map[string]any{
		"plugin_installed": installed,
		"agent_detected":   detected,
	}
	if len(setupErrors) > 0 {
		result["errors"] = setupErrors
		// Setup errors mean we cannot claim success even if checks pass.
		if installed {
			result["plugin_installed"] = false
			summary = agent.Name + " not connected"
		}
	}
	if len(manualCommands) > 0 {
		result["manual_commands"] = manualCommands
	}

	// An explicitly requested integration that did not connect is a failed
	// command: automation keys off the exit status, not just the fields.
	// The aggregate `setup agents` keeps its own in-band contract — its core
	// outcome is the skill, and per-agent detail rides in the envelope.
	if !installed || len(setupErrors) > 0 {
		hint := "Run: hey doctor"
		if len(manualCommands) > 0 {
			hint = strings.Join(manualCommands, "; ")
		}
		return &apierr.Error{Code: "setup_incomplete", Message: summary, Hint: hint}
	}

	breadcrumbs := []output.Breadcrumb{
		{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"},
	}
	for i, manual := range manualCommands {
		breadcrumbs = append(breadcrumbs, output.Breadcrumb{
			Action:      fmt.Sprintf("manual_step_%d", i+1),
			Command:     manual,
			Description: "Manual setup step",
		})
	}

	return writeOK(result,
		output.WithSummary(summary),
		output.WithBreadcrumbs(breadcrumbs...),
	)
}

// --- Claude Code ---

// runClaudeSetup performs the Claude Code setup for the interactive wizard.
// Failures warn and continue — the post-setup snapshot turns them into issues.
func runClaudeSetup(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	progress := func(message string) {
		fmt.Fprintln(w, message)
	}

	if err := installClaudePlugin(cmd.Context(), progress); err != nil {
		fmt.Fprintln(w, warning.format("Claude Code setup failed: "+err.Error()))
		var setupErr *agentSetupError
		if errors.As(err, &setupErr) && len(setupErr.Manual) > 0 {
			fmt.Fprintln(w, "Try manually:")
			for _, manual := range setupErr.Manual {
				fmt.Fprintln(w, bold.format(manual))
			}
		}
		fmt.Fprintln(w, "Then verify with: hey doctor")
		return nil
	}

	fmt.Fprintln(w, statusLine(true, "Claude Code plugin installed"))
	fmt.Fprintln(w, statusLine(true, "Claude Code skill linked"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tip: enable auto-update to stay current with new CLI releases:")
	fmt.Fprintln(w, harness.AutoUpdateHint)
	return nil
}

func runClaudeSetupNonInteractive(cmd *cobra.Command) error {
	return installClaudePlugin(cmd.Context(), func(string) {})
}

// installClaudePlugin registers the 37signals marketplace, installs the hey
// plugin and links the skill. The marketplace refresh matters: `marketplace
// add` no-ops on an already-registered marketplace, so a stale cached entry
// would otherwise strand `plugin install` on an old source.
func installClaudePlugin(parent context.Context, progress func(string)) error {
	// Never fabricate ~/.claude: linking the skill on a machine without Claude
	// Code would make every later detection report it installed.
	if !harness.DetectClaude() {
		return &agentSetupError{
			Summary: "Claude Code not detected — install Claude Code, then run: hey setup claude",
			Manual:  claudeManualCommands(),
		}
	}

	// The skill link first: it needs no binary, and a repaired link is worth
	// having even when the plugin install cannot run.
	var linkErr error
	if _, err := linkSkillToClaude(); err != nil {
		linkErr = fmt.Errorf("skill link: %w", err)
	}

	if harness.CheckClaudePlugin().Status != "pass" {
		claudePath := harness.FindClaudeBinary()
		if claudePath == "" {
			return &agentSetupError{
				Summary: "Claude Code binary not found — install the plugin from inside Claude Code",
				Manual:  claudeManualCommands(),
			}
		}

		progress("Registering " + harness.ClaudeMarketplaceName + " marketplace…")
		// Best-effort: already registered is the common case and not an error.
		_, _ = runClaudePluginStep(parent, claudeMarketplaceTimeout, claudePath, "plugin", "marketplace", "add", harness.ClaudeMarketplaceSource)
		progress("Refreshing " + harness.ClaudeMarketplaceName + " marketplace…")
		_, _ = runClaudePluginStep(parent, claudeMarketplaceTimeout, claudePath, "plugin", "marketplace", "update", harness.ClaudeMarketplaceName)

		progress("Installing " + harness.ClaudeExpectedPluginKey + " plugin…")
		out, err := runClaudePluginStep(parent, claudeInstallTimeout, claudePath, "plugin", "install", harness.ClaudeExpectedPluginKey)
		if err != nil {
			return claudeSetupError("plugin install failed: " + agentCommandFailure(out, err))
		}
		if harness.CheckClaudePlugin().Status != "pass" {
			return claudeSetupError("plugin install did not register " + harness.ClaudeExpectedPluginKey)
		}
	}

	return linkErr
}

func runClaudeStep(parent context.Context, timeout time.Duration, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return runAgentCommand(ctx, path, args...)
}

// runClaudePluginStep clones the public Claude plugin repositories over
// HTTPS independently of global Git URL rewrites. Claude accepts HTTPS
// marketplace sources and also emits SSH-shaped GitHub URLs for plugins, so
// this command-scoped Git configuration maps both forms onto HTTPS.
func runClaudePluginStep(parent context.Context, timeout time.Duration, path string, args ...string) ([]byte, error) {
	env := []string{
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_0=git@github.com:",
		"GIT_CONFIG_KEY_1=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_1=ssh://git@github.com/",
	}
	ctx := context.WithValue(parent, agentCommandEnvironmentKey{}, env)
	return runClaudeStep(ctx, timeout, path, args...)
}

func claudeSetupError(summary string) *agentSetupError {
	return &agentSetupError{Summary: summary, Manual: claudeManualCommands()}
}

// claudeManualCommands are the manual install steps. The `marketplace
// update` step is essential: `marketplace add` no-ops on an already-
// registered marketplace, so without it a stale entry would survive.
func claudeManualCommands() []string {
	return []string{
		"claude plugin marketplace add " + harness.ClaudeMarketplaceSource,
		"claude plugin marketplace update " + harness.ClaudeMarketplaceName,
		"claude plugin install " + harness.ClaudeExpectedPluginKey,
	}
}

func agentCommandFailure(out []byte, err error) string {
	message := strings.TrimSpace(string(out))
	if len(message) > 500 {
		message = message[:500]
	}
	if message == "" {
		return err.Error()
	}
	return message
}

// --- Codex ---

// runCodexSetup connects Codex to the shared agent skill.
func runCodexSetup(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	path, err := installCodexSkill()
	if err != nil {
		fmt.Fprintln(w, warning.format("Codex skill install failed: "+err.Error()))
		fmt.Fprintln(w, "Then verify with: hey doctor")
		return nil //nolint:nilerr // warn and continue; the post-setup snapshot reports the failure
	}
	fmt.Fprintln(w, statusLine(true, "Codex skill installed ("+path+")"))
	return nil
}

func runCodexSetupNonInteractive(*cobra.Command) error {
	_, err := installCodexSkill()
	return err
}

// installCodexSkill is the Codex handler's one step. The caller installs the
// shared baseline first; this removes any older hey-cli-managed Codex copy so
// Codex discovers only one skill. Like Claude, it never fabricates the agent.
func installCodexSkill() (string, error) {
	if !harness.DetectCodex() {
		return "", &agentSetupError{
			Summary: "Codex not detected — install Codex, then run: hey setup codex",
			Manual:  []string{"hey setup codex"},
		}
	}
	path := harness.AgentSkillPath()
	if path == "" {
		return "", fmt.Errorf("cannot determine shared Agent Skills directory")
	}
	if !baselineSkillInstalled() {
		return "", fmt.Errorf("shared HEY skill is not installed")
	}
	if _, err := migrateLegacyCodexSkill(); err != nil {
		return "", err
	}
	return path, nil
}

// --- Shared helpers ---

// statusLine renders a ✓/✗ checklist line.
func statusLine(ok bool, label string) string {
	if ok {
		return success.format("✓ " + label)
	}
	return warning.format("✗ " + label)
}

// snapshotAgentChecks captures every check across the given agents in one pass.
func snapshotAgentChecks(agents []harness.AgentInfo) []agentCheck {
	var out []agentCheck
	for _, a := range agents {
		if a.Checks == nil {
			continue
		}
		for _, c := range a.Checks() {
			out = append(out, agentCheck{Agent: a.Name, Name: c.Name, Status: c.Status, Hint: c.Hint})
		}
	}
	return out
}

// issuesFromChecks returns one issue per non-passing check in the snapshot.
func issuesFromChecks(checks []agentCheck) []agentIssue {
	var issues []agentIssue
	for _, c := range checks {
		if c.Status != "pass" {
			issues = append(issues, agentIssue{Agent: c.Agent, Check: c.Name, Hint: c.Hint})
		}
	}
	return issues
}

// statusFromOutcome maps an agent-setup outcome to a wizard status. Issues
// are authoritative: any observed failure means "incomplete", and Skipped
// never suppresses one. A deliberate skip records no issues, so it stays
// "complete".
func statusFromOutcome(o agentSetupOutcome) string {
	if len(o.Issues) > 0 {
		return "incomplete"
	}
	return "complete"
}

// agentChecksPass reports whether every health check for the agent passes.
func agentChecksPass(agent harness.AgentInfo) bool {
	if agent.Checks == nil {
		return false
	}
	checks := agent.Checks()
	if len(checks) == 0 {
		return false
	}
	for _, c := range checks {
		if c.Status != "pass" {
			return false
		}
	}
	return true
}

// joinNames joins names with commas and "and".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}
