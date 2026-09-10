package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
)

// isolateAgents makes agent detection deterministic: no claude/codex binary
// on PATH and no ~/.local/bin, so only the ~/.claude and ~/.codex directories
// a test creates count. Agent CLIs are never spawned.
func isolateAgents(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	// The wizard installs shell completions too; without this it would read
	// the shell of whoever runs the tests.
	stubCompletionEnv(t, testCompletionEnv(t, "bash"))
	stubRunAgentCommand(t, func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Errorf("unexpected agent command: %s %v", name, args)
		return nil, errors.New("unexpected agent command")
	})
}

func stubRunAgentCommand(t *testing.T, fn func(context.Context, string, ...string) ([]byte, error)) {
	t.Helper()
	orig := runAgentCommand
	runAgentCommand = fn
	t.Cleanup(func() { runAgentCommand = orig })
}

func stubInteractive(t *testing.T, interactive bool) {
	t.Helper()
	orig := interactiveStdio
	interactiveStdio = func() bool { return interactive }
	t.Cleanup(func() { interactiveStdio = orig })
}

func identityServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity.json" {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":1,
			"name":"Jane Doe",
			"primary_contact":{"id":101,"email_address":"jane@example.com"},
			"accounts":[
				{"id":1,"name":"Personal","purpose":"home","status":"active"},
				{"id":2,"name":"Work","purpose":"work","status":"active"}
			],
			"all_users":[
				{"id":11,"account_id":1,"contact":{"id":101,"email_address":"jane@example.com"}},
				{"id":22,"account_id":2,"contact":{"id":202,"email_address":"jane@company.example"}}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func wizardData(t *testing.T, response output.Response) map[string]any {
	t.Helper()
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("wizard data = %T", response.Data)
	}
	return data
}

func TestSetupCommandRegistersAgentSubcommands(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{{"setup", "agents"}, {"setup", "claude"}, {"setup", "codex"}} {
		command, _, err := root.Find(path)
		if err != nil || command.Name() != path[1] {
			t.Errorf("%v not registered: %v", path, err)
		}
	}
}

func TestSetupWelcomeCopy(t *testing.T) {
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })
	var out bytes.Buffer
	wizard := setupWizard{}
	wizard.welcome(&out)
	want := "HEY! It's the command-line interface!\n\nLet's get you set up. It'll only take a moment"
	if !strings.Contains(out.String(), want) {
		t.Errorf("welcome missing %q:\n%s", want, out.String())
	}
	for _, removed := range []string{"This will only take a moment", "The command-line interface for HEY (v", "take a moment."} {
		if strings.Contains(out.String(), removed) {
			t.Errorf("welcome retained %q:\n%s", removed, out.String())
		}
	}
}

func TestSetupSkipAgentsLeavesAgentIntegrationsUnchanged(t *testing.T) {
	isolateAgents(t)
	server := identityServer(t)
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "setup", "--skip-agents")
	if err != nil {
		t.Fatalf("setup --skip-agents: %v", err)
	}
	data := wizardData(t, response)
	if data["agents_skipped"] != true {
		t.Errorf("agents_skipped = %v", data["agents_skipped"])
	}
	if agents, ok := data["agents"].([]any); !ok || len(agents) != 0 {
		t.Errorf("agents = %v", data["agents"])
	}
	if _, statErr := os.Stat(filepath.Join(configHome, ".agents", "skills", "hey", skillFilename)); !os.IsNotExist(statErr) {
		t.Errorf("agent skill was written: %v", statErr)
	}
}

func TestSetupSilentSuccessShowsSpinnerAndCompletion(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	server := identityServer(t)
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "setup", "--styled", "--silent-success")
	if err != nil {
		t.Fatalf("setup --silent-success: %v", err)
	}
	if !strings.Contains(stdout, "Installing HEY…") {
		t.Errorf("silent setup did not show installation activity: %q", stdout)
	}
	if !strings.HasSuffix(stdout, "\r\x1b[2KSETUP COMPLETE\n") {
		t.Errorf("silent setup did not clear the spinner into SETUP COMPLETE: %q", stdout)
	}
	for _, hidden := range []string{"Welcome to HEY", "Coding agents", "Try it out!"} {
		if strings.Contains(stdout, hidden) {
			t.Errorf("silent success contains narration %q: %q", hidden, stdout)
		}
	}
}

func TestSetupSilentSuccessKeepsSignInInstructions(t *testing.T) {
	stubStdinTerminal(t)
	t.Setenv("HEY_NONINTERACTIVE", "")
	// Without this the manager consults the developer's real keyring, sees them
	// signed in, and skips the stubbed login this test exists to observe.
	t.Setenv("HEY_NO_KEYRING", "1")
	previousAuthMgr := authMgr
	authMgr = auth.NewManager("http://app.hey.localhost:3003", http.DefaultClient, t.TempDir())
	t.Cleanup(func() { authMgr = previousAuthMgr })
	previousLogin := loginInteractively
	loginInteractively = func(out io.Writer) error {
		fmt.Fprintln(out, "Opening browser for authentication...")
		fmt.Fprintln(out, "If the browser doesn't open, visit: https://example.com/oauth")
		fmt.Fprintln(out, "Waiting for authentication...")
		return nil
	}
	t.Cleanup(func() { loginInteractively = previousLogin })

	cmd := newSetupCommand().cmd
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	wizard := &setupWizard{cmd: cmd, opts: wizardOptions{silentSuccess: true}, styled: true, nextStep: 1}
	signedIn, err := wizard.signIn()
	if err != nil || !signedIn {
		t.Fatalf("signIn = %v, %v", signedIn, err)
	}
	for _, want := range []string{"Opening browser", "https://example.com/oauth", "Waiting for authentication"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("silent sign-in missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Step 1") {
		t.Errorf("silent sign-in included setup narration:\n%s", stdout.String())
	}
}

func TestSetupSilentSuccessKeepsFailureGuidance(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	t.Setenv("HEY_NONINTERACTIVE", "1")
	server := quietServer(t)
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	stdout, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", false, "setup", "--styled", "--silent-success")
	if err != nil {
		t.Fatalf("setup --silent-success: %v", err)
	}
	for _, want := range []string{"SETUP INCOMPLETE", "Not logged in", "hey auth login"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("failure output missing %q:\n%s", want, stdout)
		}
	}
	for _, hidden := range []string{"Welcome to HEY", "Try it out!"} {
		if strings.Contains(stdout, hidden) {
			t.Errorf("failure output contains success narration %q:\n%s", hidden, stdout)
		}
	}
}

func TestSetupSilentSuccessRejectsMachineOutput(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	_, _, err := runAuthCommand(t, t.TempDir(), "http://app.hey.localhost:3003", "", true, "setup", "--silent-success")
	if err == nil || !strings.Contains(err.Error(), "requires an interactive terminal with styled output") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupRejectsJQ(t *testing.T) {
	isolateAgents(t)
	_, _, err := runAuthCommand(t, t.TempDir(), "http://app.hey.localhost:3003", "", false, "setup", "--jq", ".")
	if err == nil || !strings.Contains(err.Error(), "--jq is not supported by the setup wizard") {
		t.Fatalf("error = %v", err)
	}
}

// A piped `hey setup --json` with nobody at a terminal must not wait for a
// browser: it reports "not logged in" and points at `hey auth login`.
func TestSetupJSONNotLoggedInWithoutTerminalReportsIncomplete(t *testing.T) {
	isolateAgents(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	data := wizardData(t, response)
	if data["status"] != "incomplete" {
		t.Errorf("status = %v, want incomplete", data["status"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v", data["issues"])
	}
	if issue, _ := issues[0].(map[string]any); issue["check"] != "Not logged in" {
		t.Errorf("issue = %v", issues[0])
	}
	if len(response.Breadcrumbs) == 0 || response.Breadcrumbs[0].Command != "hey auth login" {
		t.Errorf("breadcrumbs = %+v", response.Breadcrumbs)
	}
	if response.Summary != "Setup finished with issues" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetupJSONSignedInReportsIdentityAndPersistsOnboarded(t *testing.T) {
	isolateAgents(t)
	server := identityServer(t)
	configHome := t.TempDir()

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	stdout, response, err := runAuthCommand(t, configHome, server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Pure JSON on stdout: exactly one envelope, nothing before or after.
	var probe map[string]any
	if decodeErr := json.NewDecoder(strings.NewReader(stdout)).Decode(&probe); decodeErr != nil || !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Fatalf("stdout is not a single JSON envelope: %q", stdout)
	}

	data := wizardData(t, response)
	if data["status"] != "complete" {
		t.Errorf("status = %v, want complete", data["status"])
	}
	identity, _ := data["identity"].(map[string]any)
	if identity["name"] != "Jane Doe" || identity["email"] != "jane@example.com" {
		t.Errorf("identity = %v", data["identity"])
	}
	accounts, _ := data["accounts"].([]any)
	if len(accounts) != 3 { // All Accounts + two linked
		t.Errorf("accounts = %v", data["accounts"])
	}
	if response.Summary != "Setup complete - jane@example.com" {
		t.Errorf("summary = %q", response.Summary)
	}
	if len(response.Breadcrumbs) == 0 || response.Breadcrumbs[0].Command != "hey tui" {
		t.Errorf("breadcrumbs = %+v", response.Breadcrumbs)
	}

	raw, err := os.ReadFile(filepath.Join(configHome, "hey-cli", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["onboarded"] != true {
		t.Errorf("onboarded not persisted: %s", raw)
	}
}

// With Claude Code detected but no binary to drive, the machine-mode wizard
// still installs the skill, never prompts, and reports the plugin as an issue.
func TestSetupJSONRunsAgentStepNonInteractively(t *testing.T) {
	isolateAgents(t)
	server := identityServer(t)
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	data := wizardData(t, response)
	if data["skill_installed"] != true {
		t.Error("baseline skill should be installed for a detected agent")
	}
	if _, err := os.Stat(filepath.Join(configHome, ".claude", "skills", "hey", "SKILL.md")); err != nil {
		t.Errorf("Claude skill link missing: %v", err)
	}
	if data["status"] != "incomplete" {
		t.Errorf("status = %v, want incomplete (plugin cannot install without a binary)", data["status"])
	}
	rawIssues := data["issues"].([]any)
	issueChecks := make([]string, 0, len(rawIssues))
	for _, raw := range rawIssues {
		issue := raw.(map[string]any)
		issueChecks = append(issueChecks, issue["check"].(string))
	}
	if !contains(issueChecks, "Claude Code Plugin") || contains(issueChecks, "Claude Code Skill") {
		t.Errorf("issues = %v", issueChecks)
	}
	agents, _ := data["agents"].([]any)
	if len(agents) == 0 {
		t.Error("agents checklist should be reported")
	}
}

// Once onboarded, a logged-out bare `hey` runs the lite wizard: sign-in only,
// no agent step even when an agent is detected.
func TestBareHeyLiteWizardSkipsAgentsWhenOnboarded(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "config", "set", "onboarded", "true"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	// No --json: that is the help route. Stdout is a buffer, so the wizard
	// renders its envelope rather than prose — and with no terminal on stdin
	// it cannot sign in.
	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false)
	if err != nil {
		t.Fatalf("bare hey: %v", err)
	}
	var response output.Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("bare hey did not render the wizard envelope: %q", stdout)
	}
	data := wizardData(t, response)
	if data["skill_installed"] != false {
		t.Error("lite wizard must not run the agent step")
	}
	if agents, _ := data["agents"].([]any); len(agents) != 0 {
		t.Errorf("lite wizard reported agents: %v", agents)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestStatusFromOutcome(t *testing.T) {
	if got := statusFromOutcome(agentSetupOutcome{}); got != "complete" {
		t.Errorf("empty outcome = %q", got)
	}
	if got := statusFromOutcome(agentSetupOutcome{Skipped: true}); got != "complete" {
		t.Errorf("skipped outcome = %q, a deliberate skip is complete", got)
	}
	if got := statusFromOutcome(agentSetupOutcome{Issues: []agentIssue{{Check: "Claude Code Plugin"}}}); got != "incomplete" {
		t.Errorf("outcome with issues = %q", got)
	}
}

func TestShowWizardSuccessText(t *testing.T) {
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	var out bytes.Buffer
	showWizardSuccess(&out, wizardResult{Status: "complete", Identity: &wizardIdentity{Email: "jane@example.com"}}, agentSetupOutcome{
		Checks: []agentCheck{{Agent: "Claude Code", Name: "Claude Code Plugin", Status: "pass"}},
	}, true, 3)
	text := out.String()
	for _, want := range []string{"✓ Signed in", "✓ Claude Code Plugin", "Step 3: Try it out!", "hey hey", "Open TUI", "hey box list", `hey search "quarterly planning"`} {
		if !strings.Contains(text, want) {
			t.Errorf("complete summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Some steps need attention") {
		t.Errorf("complete summary should not list remediation:\n%s", text)
	}

	out.Reset()
	issues := []agentIssue{{Agent: "Claude Code", Check: "Claude Code Plugin", Hint: "Run: hey setup claude"}}
	showWizardSuccess(&out, wizardResult{Status: "incomplete", Issues: issues}, agentSetupOutcome{
		Checks: []agentCheck{{Agent: "Claude Code", Name: "Claude Code Plugin", Status: "fail"}},
		Issues: issues,
	}, true, 2)
	text = out.String()
	for _, want := range []string{"✗ Claude Code Plugin", "Some steps need attention:", "Claude Code Plugin: Run: hey setup claude", "Then verify with: hey doctor", "Step 2: Try it out!"} {
		if !strings.Contains(text, want) {
			t.Errorf("incomplete summary missing %q:\n%s", want, text)
		}
	}

	out.Reset()
	showWizardSuccess(&out, wizardResult{Status: "complete"}, agentSetupOutcome{Skipped: true}, true, 1)
	if !strings.Contains(out.String(), "Coding agent setup skipped — run: hey setup") {
		t.Errorf("skipped summary:\n%s", out.String())
	}
}

func TestShowWizardSuccessConciseHidesChecklist(t *testing.T) {
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	result := wizardResult{
		Status: "complete",
		Omarchy: &omarchyOutcome{Steps: []omarchyStep{
			{Name: "bar plugin", Status: "installed", Detail: "installed and enabled; notifications off"},
			{Name: "desktop entry", Status: "installed"},
		}},
	}
	outcome := agentSetupOutcome{Checks: []agentCheck{
		{Name: "Claude Code Plugin", Status: "pass"},
		{Name: "Claude Code Skill", Status: "pass"},
		{Name: "Codex Skill", Status: "pass"},
	}}
	var out bytes.Buffer
	showWizardSuccess(&out, result, outcome, false, 1)
	for _, hidden := range []string{"Signed in", "Claude Code Plugin", "Claude Code Skill", "Codex Skill", "Omarchy desktop", "Bar plugin:", "Desktop:", "Setup complete!", "────────────────", "Try these commands:", "Step 1:"} {
		if strings.Contains(out.String(), hidden) {
			t.Errorf("concise summary contains %q:\n%s", hidden, out.String())
		}
	}
	for _, want := range []string{"Try it out!", "hey hey", "Open TUI"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("concise summary missing %q:\n%s", want, out.String())
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if (strings.Contains(line, "Try it out!") || strings.HasPrefix(strings.TrimLeft(line, " "), "hey ")) && strings.HasPrefix(line, " ") {
			t.Errorf("summary line is indented: %q", line)
		}
	}

	colorDisabled = false
	out.Reset()
	showWizardSuccess(&out, result, outcome, false, 1)
	if strings.Contains(out.String(), "\033[90m") || strings.Contains(out.String(), "\033[2m") {
		t.Errorf("summary text is dimmed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "\nhey hey ") {
		t.Errorf("summary command is not in the default color:\n%s", out.String())
	}
	if strings.Contains(out.String(), bold.format("hey hey")) {
		t.Errorf("summary command competes with the step style:\n%s", out.String())
	}
	if !strings.Contains(out.String(), italicPlain.format("Open TUI")) {
		t.Errorf("summary hint is not differentiated from its command:\n%s", out.String())
	}
}

func stubStdinTerminal(t *testing.T) {
	t.Helper()
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = orig })
}

// The wizard installs shell completions on its own. An install through mise,
// the installer script or a tarball registers them nowhere, and nobody thinks
// to go looking for something they have never had.
func TestSetupInstallsShellCompletions(t *testing.T) {
	isolateAgents(t)
	env := testCompletionEnv(t, "bash")
	stubCompletionEnv(t, env)
	server := quietServer(t)

	_, response, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	if data := wizardData(t, response); data["completions_installed"] != true {
		t.Errorf("completions_installed = %v, want true", data["completions_installed"])
	}
	target, err := env.target("bash")
	if err != nil {
		t.Fatal(err)
	}
	if !ownedCompletionFile(target.Path) {
		t.Errorf("no completion at %s", target.Path)
	}
}

// HEY_NONINTERACTIVE disables interactive sign-in even on a real PTY while
// detected agent setup continues without prompting.
func TestSetupStyledNonInteractiveNeverPromptsNorSignsIn(t *testing.T) {
	isolateAgents(t)
	stubStdinTerminal(t) // a PTY — but HEY_NONINTERACTIVE wins
	t.Setenv("HEY_NONINTERACTIVE", "1")
	server := quietServer(t)
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "setup", "--styled")
	if err != nil {
		t.Fatalf("setup --styled: %v", err)
	}
	// No OAuth wait: the run completed and reported the missing login.
	if !strings.Contains(stdout, "Some steps need attention") || !strings.Contains(stdout, "Not logged in") {
		t.Errorf("expected incomplete-step guidance, got:\n%s", stdout)
	}
	for _, want := range []string{"Step 1: Coding agents", "Step 2: Try it out!"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dynamic steps missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Step 3:") {
		t.Errorf("setup left a gap in its step numbering:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, " ") {
			t.Errorf("setup line is indented: %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(configHome, ".agents", "skills", "hey", "SKILL.md")); err != nil {
		t.Errorf("agent step should auto-proceed without a prompt: %v", err)
	}
}

func TestSetupSpinnerAnimatesAndClears(t *testing.T) {
	originalInterval := setupSpinnerInterval
	setupSpinnerInterval = time.Millisecond
	t.Cleanup(func() { setupSpinnerInterval = originalInterval })

	var out bytes.Buffer
	stop := startSetupSpinner(&out, "Installing agent skill…", true)
	time.Sleep(5 * time.Millisecond)
	stop()
	text := out.String()
	if !strings.Contains(text, "Installing agent skill…") {
		t.Errorf("spinner output = %q", text)
	}
	if !strings.HasSuffix(text, "\r\x1b[2K") {
		t.Errorf("spinner did not clear its line: %q", text)
	}

	out.Reset()
	startSetupSpinner(&out, "Installing agent skill…", false)()
	if out.Len() != 0 {
		t.Errorf("disabled spinner output = %q", out.String())
	}
}

func TestSetupStyledDetailsRequireVerboseEnv(t *testing.T) {
	for _, tt := range []struct {
		name    string
		verbose string
		want    bool
	}{
		{name: "concise by default"},
		{name: "verbose", verbose: "1", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateAgents(t)
			t.Setenv("HEY_NONINTERACTIVE", "1")
			t.Setenv(setupVerboseEnv, tt.verbose)
			binDir := t.TempDir()
			claudePath := filepath.Join(binDir, "claude")
			if err := os.WriteFile(claudePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir)
			stubRunAgentCommand(t, func(context.Context, string, ...string) ([]byte, error) {
				return nil, nil
			})

			configHome := t.TempDir()
			if err := os.MkdirAll(filepath.Join(configHome, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			origColor := colorDisabled
			colorDisabled = true
			t.Cleanup(func() { colorDisabled = origColor })

			server := quietServer(t)
			defer server.Close()
			stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "setup", "--styled")
			if err != nil {
				t.Fatalf("setup --styled: %v", err)
			}
			for _, detail := range []string{"This will:", "Registering 37signals marketplace", "Refreshing 37signals marketplace", "Installing hey@37signals plugin"} {
				if got := strings.Contains(stdout, detail); got != tt.want {
					t.Errorf("contains %q = %v, want %v:\n%s", detail, got, tt.want, stdout)
				}
			}
			if strings.Contains(stdout, "Detected: Claude Code\n\n\n") {
				t.Errorf("extra blank line after detected agents:\n%s", stdout)
			}
		})
	}
}

func TestSetupStyledInteractiveInstallsDetectedAgentsWithoutPrompt(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	server := identityServer(t)
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "setup", "--styled")
	if err != nil {
		t.Fatalf("setup --styled: %v", err)
	}
	if strings.Contains(stdout, "Set up HEY for your coding agents?") {
		t.Errorf("setup asked for agent confirmation:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Signed in as Jane Doe (jane@example.com)") {
		t.Errorf("setup omitted the signed-in identity:\n%s", stdout)
	}
	for _, hidden := range []string{"• Personal", "• Work", "Using All Accounts", "Default mail account"} {
		if strings.Contains(stdout, hidden) {
			t.Errorf("setup listed account detail %q:\n%s", hidden, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(configHome, ".agents", "skills", "hey", "SKILL.md")); err != nil {
		t.Errorf("setup did not install the detected agent skill: %v", err)
	}
}

func TestSetupRepeatKeepsDetectedConnectedAgentsVisible(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	server := identityServer(t)
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", false, "setup", "--styled"); err != nil {
		t.Fatalf("initial setup: %v", err)
	}
	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "setup", "--styled")
	if err != nil {
		t.Fatalf("repeat setup: %v", err)
	}
	for _, want := range []string{"Step 1: Coding agents", "✓ Codex connected", "Step 2: Try it out!"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("repeat setup missing %q:\n%s", want, stdout)
		}
	}
}

func TestSetupRepeatMigratesManagedLegacyCodexSkill(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	server := identityServer(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "setup"); err != nil {
		t.Fatalf("initial setup: %v", err)
	}

	legacy := filepath.Join(home, ".codex", "skills", "hey")
	writeSkillFixture(t, legacy, "# managed legacy duplicate", true)
	if check := harness.CheckCodexSkill(); check.Status != "fail" {
		t.Fatalf("preflight did not notice managed duplicate: %+v", check)
	}

	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "setup"); err != nil {
		t.Fatalf("repeat setup: %v", err)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Fatalf("repeat setup left managed legacy duplicate: %v", err)
	}
}

// Machine output plus HEY_NONINTERACTIVE on a terminal must not start OAuth.
func TestSetupJSONNonInteractiveEnvSkipsSignIn(t *testing.T) {
	isolateAgents(t)
	stubStdinTerminal(t)
	t.Setenv("HEY_NONINTERACTIVE", "1")
	server := quietServer(t)

	_, response, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	data := wizardData(t, response)
	if data["status"] != "incomplete" {
		t.Errorf("status = %v, want incomplete", data["status"])
	}
}

// Credentials that merely exist are not a login: when HEY rejects them, the
// wizard must not skip OAuth and then report a complete, signed-in setup.
func TestSetupJSONStaleCredentialsReportIncomplete(t *testing.T) {
	isolateAgents(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "stale-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	data := wizardData(t, response)
	if data["status"] != "incomplete" {
		t.Errorf("status = %v, want incomplete for rejected credentials", data["status"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v", data["issues"])
	}
	if issue, _ := issues[0].(map[string]any); issue["check"] != "Stored sign-in rejected" {
		t.Errorf("issue = %v", issues[0])
	}
	// Machine clients must be routed to repair auth, not to hey tui.
	if len(response.Breadcrumbs) == 0 || response.Breadcrumbs[0].Command != "hey auth login" {
		t.Errorf("breadcrumbs = %+v", response.Breadcrumbs)
	}
}

// A user-authored skill at the old Codex-specific path does not prevent the
// wizard from connecting Codex through the shared skill, and is preserved.
func TestSetupWizardPreservesUnmanagedLegacyCodexSkill(t *testing.T) {
	isolateAgents(t)
	server := identityServer(t)
	configHome := t.TempDir()
	custom := "# my own hey skill\n"
	codexSkill := filepath.Join(configHome, ".codex", "skills", "hey")
	if err := os.MkdirAll(codexSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexSkill, "SKILL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	data := wizardData(t, response)
	if data["status"] != "complete" {
		t.Errorf("status = %v, want complete with the shared skill", data["status"])
	}
	rawIssues := data["issues"].([]any)
	if len(rawIssues) != 0 {
		t.Errorf("issues = %v, want none", rawIssues)
	}
	if got, _ := os.ReadFile(filepath.Join(codexSkill, "SKILL.md")); string(got) != custom {
		t.Errorf("user skill changed: %q", got)
	}
}

// The checklist and the issue list must agree: rejected stored credentials
// render as not signed in.
func TestShowWizardSuccessRejectedCredentialsChecklist(t *testing.T) {
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	var out bytes.Buffer
	issues := []agentIssue{{Check: "Stored sign-in rejected", Hint: "Run: hey auth login"}}
	showWizardSuccess(&out, wizardResult{Status: "incomplete", Issues: issues}, agentSetupOutcome{}, true, 1)
	if !strings.Contains(out.String(), "✗ Signed in") {
		t.Errorf("rejected credentials must render as not signed in:\n%s", out.String())
	}
}

// Prompts render on stderr, so a redirected stderr means no prompting: an
// invisible prompt must never sit waiting for input.
func TestInteractiveStdioRequiresStderrTerminal(t *testing.T) {
	stubTerminal := func(v *func() bool, value bool) {
		orig := *v
		*v = func() bool { return value }
		t.Cleanup(func() { *v = orig })
	}
	stubTerminal(&stdinIsTerminal, true)
	stubTerminal(&stdoutIsTerminal, true)
	stubTerminal(&stderrIsTerminal, false)
	t.Setenv("HEY_NONINTERACTIVE", "")

	if interactiveStdio() {
		t.Error("a redirected stderr must disable prompting")
	}
	stubTerminal(&stderrIsTerminal, true)
	if !interactiveStdio() {
		t.Error("three terminals and no escape hatch should allow prompting")
	}
}

// --ids-only and --count cannot render a wizard result, so they are rejected
// before any side effect — never OAuth-then-"requires list data".
func TestSetupRejectsListOnlyFormatsBeforeSideEffects(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	for _, flag := range []string{"--ids-only", "--count"} {
		for _, args := range [][]string{{"setup"}, {"setup", "agents"}, {"setup", "codex"}} {
			configHome := t.TempDir()
			_, _, err := runAuthCommand(t, configHome, server.URL, "", false, append(args, flag)...)
			if err == nil || !strings.Contains(err.Error(), flag+" is not supported") {
				t.Fatalf("%v %s: error = %v", args, flag, err)
			}
			if _, statErr := os.Stat(filepath.Join(configHome, ".agents")); !os.IsNotExist(statErr) {
				t.Errorf("%v %s ran side effects before rejecting the format", args, flag)
			}
		}
	}
}

// A rejected HEY_TOKEN outranks anything hey auth login saves, so the
// remediation must point at the environment, not at a login that cannot win.
func TestSetupJSONRejectedEnvTokenPointsAtEnvironment(t *testing.T) {
	isolateAgents(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	_, response, err := runAuthCommand(t, t.TempDir(), server.URL, "revoked-env-token", true, "setup")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	data := wizardData(t, response)
	if data["status"] != "incomplete" {
		t.Errorf("status = %v", data["status"])
	}
	issues := data["issues"].([]any)
	issue := issues[0].(map[string]any)
	if issue["check"] != "HEY_TOKEN rejected" || issue["hint"] != "Update or unset HEY_TOKEN" {
		t.Errorf("issue = %v", issue)
	}
	if len(response.Breadcrumbs) == 0 || response.Breadcrumbs[0].Command != "unset HEY_TOKEN" {
		t.Errorf("breadcrumbs must point at the environment, got %+v", response.Breadcrumbs)
	}
}
