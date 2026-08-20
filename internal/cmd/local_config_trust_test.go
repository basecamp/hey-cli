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
	previousAccount := accountFlag
	previousBaseURL := baseURL
	jsonFlag = false
	accountFlag = ""
	baseURL = ""
	t.Cleanup(func() {
		jsonFlag = previousJSON
		accountFlag = previousAccount
		baseURL = previousBaseURL
	})
}
