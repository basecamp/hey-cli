package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/tui"
)

func stubRunTUI(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := runTUI
	runTUI = func(*hey.Client, *hey.Client, string, tui.Watchers, tui.Options) error {
		calls++
		return nil
	}
	t.Cleanup(func() { runTUI = orig })
	return &calls
}

func stubAskToSignIn(t *testing.T, answer bool) *int {
	t.Helper()
	calls := 0
	orig := askToSignIn
	askToSignIn = func() (bool, error) {
		calls++
		return answer, nil
	}
	t.Cleanup(func() { askToSignIn = orig })
	return &calls
}

func quietServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestBareHeyInteractiveUnauthenticatedRunsWizard(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	tuiCalls := stubRunTUI(t)
	server := quietServer(t)

	stdout, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", false)
	if err != nil {
		t.Fatalf("bare hey: %v", err)
	}
	if *tuiCalls != 0 {
		t.Error("logged out: the TUI must not open")
	}
	// The wizard ran (envelope form, since stdout is a buffer), not help.
	if !strings.Contains(stdout, `"status"`) || strings.Contains(stdout, "USAGE") {
		t.Errorf("expected the wizard, got:\n%s", stdout)
	}
}

// Bare `hey` never opens the TUI — that contract moved to `hey tui` — so an
// authenticated interactive run prints help.
func TestBareHeyInteractiveAuthenticatedShowsHelp(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	tuiCalls := stubRunTUI(t)
	server := quietServer(t)

	stdout, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false)
	if err != nil {
		t.Fatalf("bare hey: %v", err)
	}
	if *tuiCalls != 0 {
		t.Error("bare hey must never open the TUI")
	}
	if !strings.Contains(stdout, "USAGE") || !strings.Contains(stdout, "hey tui") {
		t.Errorf("expected help pointing at hey tui, got:\n%s", stdout)
	}
}

// The hidden `hey hey` and `hey tui` share the TUI runner.
func TestHeyTuiOpensTUIWhenAuthenticated(t *testing.T) {
	isolateAgents(t)
	tuiCalls := stubRunTUI(t)
	server := quietServer(t)

	for _, command := range []string{"tui", "hey"} {
		if _, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false, command); err != nil {
			t.Fatalf("hey %s: %v", command, err)
		}
	}
	if *tuiCalls != 2 {
		t.Errorf("TUI opened %d times, want 2", *tuiCalls)
	}
}

func TestHeyTuiTopicStartsAtTheRequestedThread(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	original := runTUI
	var options tui.Options
	runTUI = func(_ *hey.Client, _ *hey.Client, _ string, _ tui.Watchers, got tui.Options) error {
		options = got
		return nil
	}
	t.Cleanup(func() { runTUI = original })

	if _, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false,
		"tui", "--topic", "5511", "--topic-title", "Lunch on Thursday?", "--instance", "omarchy"); err != nil {
		t.Fatalf("hey tui --topic: %v", err)
	}
	if options.Open.TopicID != 5511 || options.Open.Title != "Lunch on Thursday?" || options.Instance != "omarchy" {
		t.Fatalf("initial topic = %#v", options.Open)
	}
}

func TestHeyTuiRemoteSendsTheTopicWithoutLaunching(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	calls := stubRunTUI(t)
	original := openInRunningTUI
	var request tui.OpenRequest
	var instance string
	openInRunningTUI = func(gotInstance string, got tui.OpenRequest) error {
		instance = gotInstance
		request = got
		return nil
	}
	t.Cleanup(func() { openInRunningTUI = original })

	if _, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false,
		"tui", "--topic", "5511", "--topic-title", "Lunch on Thursday?", "--instance", "omarchy", "--remote"); err != nil {
		t.Fatalf("hey tui --remote: %v", err)
	}
	if request.TopicID != 5511 || request.Title != "Lunch on Thursday?" || instance != "omarchy" || *calls != 0 {
		t.Fatalf("remote request = %#v, TUI launches = %d", request, *calls)
	}
}

func TestHeyTuiScreenerStartsAtTheScreener(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	original := runTUI
	var options tui.Options
	runTUI = func(_ *hey.Client, _ *hey.Client, _ string, _ tui.Watchers, got tui.Options) error {
		options = got
		return nil
	}
	t.Cleanup(func() { runTUI = original })

	if _, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false,
		"tui", "--screener", "--instance", "omarchy"); err != nil {
		t.Fatalf("hey tui --screener: %v", err)
	}
	if !options.Open.Screener || options.Open.TopicID != 0 || options.Instance != "omarchy" {
		t.Fatalf("initial destination = %#v", options.Open)
	}
}

func TestHeyTuiRemoteSendsTheScreenerWithoutLaunching(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	calls := stubRunTUI(t)
	original := openInRunningTUI
	var request tui.OpenRequest
	var instance string
	openInRunningTUI = func(gotInstance string, got tui.OpenRequest) error {
		instance = gotInstance
		request = got
		return nil
	}
	t.Cleanup(func() { openInRunningTUI = original })

	if _, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false,
		"tui", "--screener", "--instance", "omarchy", "--remote"); err != nil {
		t.Fatalf("hey tui --screener --remote: %v", err)
	}
	if !request.Screener || request.TopicID != 0 || instance != "omarchy" || *calls != 0 {
		t.Fatalf("remote request = %#v, TUI launches = %d", request, *calls)
	}
}

func TestHeyTuiRemoteRequiresOneDestination(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	calls := stubRunTUI(t)
	original := openInRunningTUI
	remoteCalls := 0
	openInRunningTUI = func(string, tui.OpenRequest) error {
		remoteCalls++
		return nil
	}
	t.Cleanup(func() { openInRunningTUI = original })

	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"tui", "--remote"}, want: "--remote requires --topic or --screener"},
		{args: []string{"tui", "--topic", "5511", "--screener"}, want: "--topic and --screener cannot be used together"},
		{args: []string{"tui", "--topic-title", "Lunch on Thursday?"}, want: "--topic-title requires --topic"},
		{args: []string{"tui", "--screener", "--topic-title", "Lunch on Thursday?"}, want: "--topic-title requires --topic"},
	} {
		_, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false, test.args...)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("hey %s error = %v, want %q", strings.Join(test.args, " "), err, test.want)
		}
	}
	if *calls != 0 || remoteCalls != 0 {
		t.Fatalf("invalid destinations launched TUI %d times and made %d remote calls", *calls, remoteCalls)
	}
}

func TestHeyTuiRejectsNonPositiveTopicIDs(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	calls := stubRunTUI(t)
	original := openInRunningTUI
	remoteCalls := 0
	openInRunningTUI = func(string, tui.OpenRequest) error {
		remoteCalls++
		return nil
	}
	t.Cleanup(func() { openInRunningTUI = original })

	for _, args := range [][]string{
		{"tui", "--topic", "0"},
		{"tui", "--topic", "-1"},
		{"tui", "--remote", "--topic", "0"},
		{"tui", "--remote", "--topic", "-1"},
	} {
		_, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false, args...)
		if err == nil || !strings.Contains(err.Error(), "topic ID must be positive") {
			t.Errorf("hey %s error = %v, want positive topic ID usage error", strings.Join(args, " "), err)
		}
	}
	if *calls != 0 || remoteCalls != 0 {
		t.Fatalf("invalid topics launched TUI %d times and made %d remote calls", *calls, remoteCalls)
	}
}

func TestBareHeyMachineFlagsShowHelpEvenOnTerminal(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	tuiCalls := stubRunTUI(t)
	server := quietServer(t)

	stdout, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false, "--json")
	if err != nil {
		t.Fatalf("bare hey --json: %v", err)
	}
	if *tuiCalls != 0 {
		t.Error("--json must never open the TUI")
	}
	if !strings.Contains(stdout, "USAGE") {
		t.Errorf("expected help, got:\n%s", stdout)
	}
}

func TestBareHeyNonInteractiveShowsHelp(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, false)
	tuiCalls := stubRunTUI(t)
	server := quietServer(t)

	stdout, _, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", false)
	if err != nil {
		t.Fatalf("bare hey: %v", err)
	}
	if *tuiCalls != 0 || !strings.Contains(stdout, "USAGE") {
		t.Errorf("expected help without a terminal, got tui=%d:\n%s", *tuiCalls, stdout)
	}
}

func TestRequireAuthPromptsOnlyWhenInteractiveAndStyled(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"--base-url", server.URL, "auth", "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("prime globals: %v", err)
	}

	t.Run("declined at a terminal", func(t *testing.T) {
		stubInteractive(t, true)
		asked := stubAskToSignIn(t, false)
		prev := writer
		writer = output.New(output.Options{Format: output.FormatStyled, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
		t.Cleanup(func() { writer = prev })

		err := requireAuth()
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != "auth" || cliErr.Message != "Not logged in" {
			t.Fatalf("error = %v, want auth/Not logged in", err)
		}
		if cliErr.Hint != "Run: hey auth login" {
			t.Errorf("hint = %q", cliErr.Hint)
		}
		if output.ExitCodeFor(err) != output.ExitAuth {
			t.Errorf("exit code = %d, want %d", output.ExitCodeFor(err), output.ExitAuth)
		}
		if *asked != 1 {
			t.Errorf("prompt shown %d times, want 1", *asked)
		}
	})

	t.Run("never prompts without a terminal", func(t *testing.T) {
		stubInteractive(t, false)
		asked := stubAskToSignIn(t, true)
		prev := writer
		writer = output.New(output.Options{Format: output.FormatStyled, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
		t.Cleanup(func() { writer = prev })

		if err := requireAuth(); err == nil {
			t.Fatal("expected an auth error")
		}
		if *asked != 0 {
			t.Errorf("prompt shown %d times without a terminal", *asked)
		}
	})

	t.Run("never prompts for machine output", func(t *testing.T) {
		stubInteractive(t, true)
		asked := stubAskToSignIn(t, true)
		prev := writer
		writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
		t.Cleanup(func() { writer = prev })

		if err := requireAuth(); err == nil {
			t.Fatal("expected an auth error")
		}
		if *asked != 0 {
			t.Errorf("prompt shown %d times for machine output", *asked)
		}
	})
}

func TestDataCommandWithoutAuthReturnsAuthErrorWhenPiped(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, false)
	asked := stubAskToSignIn(t, true)
	server := quietServer(t)

	_, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "box", "list")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "auth" {
		t.Fatalf("error = %v, want auth", err)
	}
	if *asked != 0 {
		t.Error("piped data command must not prompt")
	}
}
