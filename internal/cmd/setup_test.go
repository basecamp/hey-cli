package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

// isolateAgents makes agent detection deterministic: no claude/codex binary
// on PATH and no ~/.local/bin, so only the ~/.claude and ~/.codex directories
// a test creates count. Agent CLIs are never spawned.
func isolateAgents(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
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

func TestSuccessHeadline(t *testing.T) {
	tests := []struct {
		status string
		issues int
		want   string
	}{
		{"complete", 0, "Setup complete!"},
		{"incomplete", 1, "Setup finished — 1 step needs attention"},
		{"incomplete", 3, "Setup finished — 3 steps need attention"},
	}
	for _, tt := range tests {
		if got := successHeadline(tt.status, tt.issues); got != tt.want {
			t.Errorf("successHeadline(%q, %d) = %q, want %q", tt.status, tt.issues, got, tt.want)
		}
	}
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
	})
	text := out.String()
	for _, want := range []string{"Setup complete!", "✓ Signed in", "✓ Claude Code Plugin", "Try these commands:", "hey boxes", `hey search "quarterly planning"`} {
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
	})
	text = out.String()
	for _, want := range []string{"Setup finished — 1 step needs attention", "✗ Claude Code Plugin", "Some steps need attention:", "Claude Code Plugin: Run: hey setup claude", "Then verify with: hey doctor"} {
		if !strings.Contains(text, want) {
			t.Errorf("incomplete summary missing %q:\n%s", want, text)
		}
	}

	out.Reset()
	showWizardSuccess(&out, wizardResult{Status: "complete"}, agentSetupOutcome{Skipped: true})
	if !strings.Contains(out.String(), "Coding agent setup skipped — run: hey setup") {
		t.Errorf("skipped summary:\n%s", out.String())
	}
}
