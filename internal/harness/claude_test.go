package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basecamp/hey-cli/internal/version"
)

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeInstalledPlugins(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectClaudeByHomeDirectory(t *testing.T) {
	home := tempHome(t)
	t.Setenv("PATH", t.TempDir())

	if DetectClaude() {
		t.Error("no ~/.claude and no binary should not detect Claude")
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !DetectClaude() {
		t.Error("~/.claude directory should detect Claude")
	}
}

func TestCheckClaudePluginFormats(t *testing.T) {
	tests := map[string]struct {
		content string
		status  string
	}{
		"missing file":  {content: "", status: "fail"},
		"v2 installed":  {content: `{"version": 2, "plugins": {"hey@37signals": [{"version": "1.0.0", "scope": "user"}]}}`, status: "pass"},
		"v1 flat map":   {content: `{"hey@37signals": {"version": "1.0.0"}}`, status: "pass"},
		"array format":  {content: `[{"name": "hey@37signals", "version": "1.0.0"}]`, status: "pass"},
		"other plugin":  {content: `{"version": 2, "plugins": {"basecamp@37signals": [{"version": "1.0.0"}]}}`, status: "fail"},
		"bare hey name": {content: `{"hey": {"version": "1.0.0"}}`, status: "fail"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			home := tempHome(t)
			if tt.content != "" {
				writeInstalledPlugins(t, home, tt.content)
			}
			check := CheckClaudePlugin()
			if check.Status != tt.status {
				t.Errorf("status = %q, want %q (message %q)", check.Status, tt.status, check.Message)
			}
			if check.Status == "fail" && check.Hint != "Run: hey setup claude" {
				t.Errorf("hint = %q", check.Hint)
			}
		})
	}
}

func TestCheckClaudeSkillLink(t *testing.T) {
	home := tempHome(t)

	check := CheckClaudeSkillLink()
	if check.Status != "fail" || check.Hint != "Run: hey setup claude" {
		t.Errorf("missing skill: %+v", check)
	}

	skillDir := filepath.Join(home, ".claude", "skills", "hey")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# hey"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Present but unmarked is somebody else's skill occupying the path —
	// never reported as a connected integration.
	if check := CheckClaudeSkillLink(); check.Status != "fail" || check.Hint != "Move it aside, then run: hey setup claude" {
		t.Errorf("unmanaged skill: %+v", check)
	}

	if err := os.WriteFile(filepath.Join(skillDir, SkillOwnershipMarker), []byte("hey-cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	if check := CheckClaudeSkillLink(); check.Status != "pass" {
		t.Errorf("managed skill: %+v", check)
	}
}

func TestInstalledPluginVersionFormats(t *testing.T) {
	tests := map[string]string{
		`{"version": 2, "plugins": {"hey@37signals": [{"version": "2.1.0"}]}}`:      "2.1.0",
		`{"hey@37signals": {"version": "1.5.0"}}`:                                   "1.5.0",
		`[{"name": "hey@37signals", "version": "0.9.0"}]`:                           "0.9.0",
		`{"version": 2, "plugins": {"basecamp@37signals": [{"version": "3.0.0"}]}}`: "",
		`not json`: "",
	}
	for content, want := range tests {
		if got := installedPluginVersion([]byte(content)); got != want {
			t.Errorf("installedPluginVersion(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestCheckClaudePluginVersion(t *testing.T) {
	home := tempHome(t)

	origVersion := version.Version
	version.Version = "1.2.3"
	t.Cleanup(func() { version.Version = origVersion })

	writeInstalledPlugins(t, home, `{"version": 2, "plugins": {"hey@37signals": [{"version": "1.2.3"}]}}`)
	if check := CheckClaudePluginVersion(); check.Status != "pass" {
		t.Errorf("matching version: %+v", check)
	}

	writeInstalledPlugins(t, home, `{"version": 2, "plugins": {"hey@37signals": [{"version": "1.0.0"}]}}`)
	check := CheckClaudePluginVersion()
	if check.Status != "warn" || check.Hint != AutoUpdateHint {
		t.Errorf("mismatched version: %+v", check)
	}

	version.Version = "dev"
	if check := CheckClaudePluginVersion(); check.Status != "pass" {
		t.Errorf("dev build must not nag: %+v", check)
	}
}
