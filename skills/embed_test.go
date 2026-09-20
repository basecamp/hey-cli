package skills

import (
	"strings"
	"testing"
)

func TestHeySkillReusesAuthenticationForUnattendedAgents(t *testing.T) {
	data, err := FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"hey auth status --json",
		"when an explicit authentication check is needed",
		"HEY_NONINTERACTIVE=1",
		"Never run `hey auth login` unattended",
		"report the task as blocked",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("embedded HEY skill does not contain %q", want)
		}
	}

	for _, forbidden := range []string{
		"run `hey auth login` first",
		"before the first data command",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("embedded HEY skill still requires an authentication preflight with %q", forbidden)
		}
	}
}

func TestHeySkillRetriesMacOSKeychainAccessWithoutBroadEscalation(t *testing.T) {
	data, err := FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"On macOS under Codex",
		"retry `hey auth status --json` once with elevated sandbox permission",
		"one read-only command",
		"normal approval flow",
		"rerun only the exact `hey` command the user requested",
		"separate one-command approval",
		"Never run the macOS `security` command",
		"never disable the sandbox globally",
		"never set `HEY_NO_KEYRING=1`",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("embedded HEY skill does not contain %q", want)
		}
	}
}
