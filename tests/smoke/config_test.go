package smoke_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShow(t *testing.T) {
	resp := heyJSON(t, "config", "show")

	type ConfigEntry struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	entries := dataAs[[]ConfigEntry](t, resp)

	found := false
	for _, e := range entries {
		if e.Key == "base_url" {
			found = true
			if e.Value != baseURL {
				t.Errorf("expected base_url=%s, got %s", baseURL, e.Value)
			}
			// Source should be "env" since we set HEY_BASE_URL.
			if e.Source != "env" {
				t.Errorf("expected source=env, got %s", e.Source)
			}
		}
	}
	if !found {
		t.Error("base_url not found in config show output")
	}
}

func TestConfigSet(t *testing.T) {
	// Use an isolated config dir for this test to avoid polluting the shared one.
	tmpDir, err := os.MkdirTemp("", "hey-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Helper that runs hey with a custom config dir and without HEY_BASE_URL
	// so the config file value is actually used.
	runHey := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(binaryPath, args...)
		env := os.Environ()
		// Remove HEY_BASE_URL so config file takes precedence.
		filtered := make([]string, 0, len(env))
		for _, e := range env {
			if !strings.HasPrefix(e, "HEY_BASE_URL=") {
				filtered = append(filtered, e)
			}
		}
		filtered = append(filtered,
			"XDG_CONFIG_HOME="+tmpDir,
			"HEY_NO_KEYRING=1",
			"NO_COLOR=1",
			"TERM=dumb",
		)
		cmd.Env = filtered
		var outBuf strings.Builder
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		return outBuf.String(), code
	}

	// Set base_url.
	out, code := runHey("config", "set", "base_url", "http://custom.localhost:9999", "--json")
	if code != 0 {
		t.Fatalf("config set failed (exit %d): %s", code, out)
	}
	assertContains(t, out, "custom.localhost:9999")

	// Verify the config file was written.
	configPath := filepath.Join(tmpDir, "hey-cli", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	var configFile map[string]string
	if err := json.Unmarshal(data, &configFile); err != nil {
		t.Fatalf("config file is not valid JSON: %v", err)
	}
	if configFile["base_url"] != "http://custom.localhost:9999" {
		t.Errorf("expected base_url=http://custom.localhost:9999 in config file, got %s", configFile["base_url"])
	}

	// Verify config show reflects the change.
	out, code = runHey("config", "show", "--json")
	if code != 0 {
		t.Fatalf("config show failed: %s", out)
	}
	assertContains(t, out, "custom.localhost:9999")
}

func TestConfigSetInvalidURL(t *testing.T) {
	heyFail(t, "config", "set", "base_url", "not-a-url")
}

func TestConfigSetUnknownKey(t *testing.T) {
	heyFail(t, "config", "set", "nonexistent_key", "value")
}

func TestConfigSetHTTPSRequired(t *testing.T) {
	// HTTP is only allowed for localhost. Non-localhost HTTP should fail.
	heyFail(t, "config", "set", "base_url", "http://example.com")
}
