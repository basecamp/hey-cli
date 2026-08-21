package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/config"
)

func TestUntrustedLocalConfigFailsBeforeNetworkRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(server.Close)
	setupLocalTrustTest(t, server.URL, "all")

	err := runLocalTrustCLI(t, "--json", "boxes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || !strings.Contains(cliErr.Message, "not trusted") {
		t.Fatalf("error = %v, want untrusted local config", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestJQIsMachineReadableForLocalConfigTrust(t *testing.T) {
	root := newRootCmd()
	command, _, err := root.Find([]string{"boxes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := command.ParseFlags([]string{"--jq", ".data"}); err != nil {
		t.Fatal(err)
	}
	if !machineReadableOutput(command) {
		t.Fatal("--jq output was treated as interactive")
	}
}

func TestTrustLocalAllowsRequestsAndChangesRequireTrustAgain(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity.json":
			_, _ = w.Write([]byte(`{"id":1,"accounts":[{"id":2,"status":"active"}]}`))
		case "/boxes.json":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	path := setupLocalTrustTest(t, server.URL, "2")

	if err := runLocalTrustCLI(t, "--json", "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	if err := runLocalTrustCLI(t, "--json", "boxes"); err != nil {
		t.Fatal(err)
	}
	if requests == 0 {
		t.Fatal("trusted local config made no request")
	}
	if err := runLocalTrustCLI(t, "--json", "config", "untrust-local"); err != nil {
		t.Fatal(err)
	}
	requests = 0
	if err := runLocalTrustCLI(t, "--json", "boxes"); err == nil {
		t.Fatal("untrusted local config was used after trust removal")
	}
	if requests != 0 {
		t.Fatalf("requests after trust removal = %d, want 0", requests)
	}
	if err := runLocalTrustCLI(t, "--json", "config", "trust-local"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(`{"base_url":"`+server.URL+`","account_id":"all"}`), 0600); err != nil {
		t.Fatal(err)
	}
	requests = 0
	err := runLocalTrustCLI(t, "--json", "boxes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || !strings.Contains(cliErr.Message, "not trusted") {
		t.Fatalf("changed config error = %v, want untrusted", err)
	}
	if requests != 0 {
		t.Fatalf("requests after local change = %d, want 0", requests)
	}
}

func TestPromptForLocalConfigTrustChoices(t *testing.T) {
	previousCfg := cfg
	cfg = &config.Config{BaseURL: "https://app.hey.com", AccountID: "42"}
	t.Cleanup(func() { cfg = previousCfg })

	for _, test := range []struct {
		input string
		want  localConfigTrustChoice
	}{
		{"1\n", localConfigTrustOnce},
		{"2\n", localConfigTrustAlways},
		{"\n", localConfigTrustCancel},
	} {
		cmd := newRootCmd()
		cmd.SetIn(strings.NewReader(test.input))
		var output bytes.Buffer
		cmd.SetErr(&output)
		choice, err := promptForLocalConfigTrust(cmd, &config.LocalConfig{
			Path:      "/workspace/.hey/config.json",
			BaseURL:   "https://app.hey.com",
			AccountID: "99",
		})
		if err != nil {
			t.Fatal(err)
		}
		if choice != test.want {
			t.Errorf("choice for %q = %d, want %d", test.input, choice, test.want)
		}
		if !strings.Contains(output.String(), "/workspace/.hey/config.json") ||
			!strings.Contains(output.String(), "Local mail account:  99") ||
			!strings.Contains(output.String(), "Effective account:   42") {
			t.Errorf("prompt omitted local or effective values: %q", output.String())
		}
	}
}

func setupLocalTrustTest(t *testing.T, baseURL, accountID string) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	workspace := filepath.Join(tmp, "workspace")
	localDir := filepath.Join(workspace, ".hey")
	if err := os.MkdirAll(localDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	path := filepath.Join(localDir, "config.json")
	if err := os.WriteFile(path, []byte(`{"base_url":"`+baseURL+`","account_id":"`+accountID+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runLocalTrustCLI(t *testing.T, args ...string) error {
	t.Helper()
	resetRootFlagsForTrustTest(t)
	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(args)
	return root.Execute()
}

func resetRootFlagsForTrustTest(t *testing.T) {
	t.Helper()
	previousJSON := jsonFlag
	previousJQ := jqFlag
	previousAccount := accountFlag
	previousBaseURL := baseURL
	jsonFlag = false
	jqFlag = ""
	accountFlag = ""
	baseURL = ""
	t.Cleanup(func() {
		jsonFlag = previousJSON
		jqFlag = previousJQ
		accountFlag = previousAccount
		baseURL = previousBaseURL
	})
}

// upgrade and version never read the local server or account, so an
// untrusted local config must not block them (machine-readable mode would
// otherwise fail them outright with a trust error).
func TestConfigIndependentCommandsSkipLocalConfigTrust(t *testing.T) {
	root := newRootCmd()
	for _, test := range []struct {
		args []string
		want bool
	}{
		{args: []string{"boxes"}, want: true},
		{args: []string{"doctor"}, want: true},
		{args: []string{"config", "show"}, want: false},
		{args: []string{"upgrade"}, want: false},
		{args: []string{"version"}, want: false},
	} {
		command, _, err := root.Find(test.args)
		if err != nil {
			t.Fatalf("Find(%v): %v", test.args, err)
		}
		if got := commandUsesRuntimeConfig(command); got != test.want {
			t.Errorf("commandUsesRuntimeConfig(%v) = %v, want %v", test.args, got, test.want)
		}
	}
}

func TestVersionRunsWithUntrustedLocalConfig(t *testing.T) {
	setupLocalTrustTest(t, "https://mail.example.com", "all")

	if err := runLocalTrustCLI(t, "--json", "version"); err != nil {
		t.Fatalf("version with an untrusted local config: %v", err)
	}
}

// The agent-facing setup subcommands never touch the HEY server or account,
// and the installer's non-TTY handoff runs `setup agents` from an arbitrary
// directory — an untrusted .hey/config.json there must not block it. The
// wizard itself signs in against the effective server, so it stays gated.
func TestSetupSubcommandsSkipLocalConfigTrust(t *testing.T) {
	root := newRootCmd()
	for _, test := range []struct {
		args []string
		want bool
	}{
		{args: []string{"setup"}, want: true},
		{args: []string{"setup", "agents"}, want: false},
		{args: []string{"setup", "claude"}, want: false},
		{args: []string{"setup", "codex"}, want: false},
	} {
		command, _, err := root.Find(test.args)
		if err != nil {
			t.Fatalf("Find(%v): %v", test.args, err)
		}
		if got := commandUsesRuntimeConfig(command); got != test.want {
			t.Errorf("commandUsesRuntimeConfig(%v) = %v, want %v", test.args, got, test.want)
		}
	}
}

func TestSetupAgentsRunsWithUntrustedLocalConfig(t *testing.T) {
	isolateAgents(t)
	t.Setenv(agentSetupEnv, "none")
	setupLocalTrustTest(t, "https://mail.example.com", "all")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := runLocalTrustCLI(t, "--json", "setup", "agents"); err != nil {
		t.Fatalf("setup agents must not be blocked by an untrusted local config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "hey", "SKILL.md")); err != nil {
		t.Errorf("skill was not installed: %v", err)
	}
}

// The installer pipes curl from an arbitrary directory: a malformed
// .hey/config.json there must not stop a local-only command before its
// trust exemption is even consulted. Runtime-config commands still surface
// the parse error.
func TestSetupAgentsRunsWithMalformedLocalConfig(t *testing.T) {
	isolateAgents(t)
	t.Setenv(agentSetupEnv, "none")
	path := setupLocalTrustTest(t, "https://mail.example.com", "all")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := runLocalTrustCLI(t, "--json", "setup", "agents"); err != nil {
		t.Fatalf("setup agents must survive a malformed local config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "hey", "SKILL.md")); err != nil {
		t.Errorf("skill was not installed: %v", err)
	}

	err := runLocalTrustCLI(t, "--json", "boxes")
	if err == nil || !strings.Contains(err.Error(), "could not parse config") {
		t.Fatalf("runtime-config commands must still report the parse error, got %v", err)
	}
}
