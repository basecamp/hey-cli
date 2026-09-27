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

func TestHeySkillExplainsContactDeliveryRouting(t *testing.T) {
	data, err := FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"hey contact deliver <contact_id> --to imbox\\|feed\\|papertrail\\|screened-out",
		"contact ID",
		"clearance ID",
		"box item ID",
		"box ID",
		"Screened Out is the deny/blocking choice and applies only to external email contacts",
		"Selecting Feed removes an existing bundle",
		"write-only",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("embedded HEY skill does not contain %q", want)
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
		"`data.authenticated` is `true`",
		"`data.authenticated` is `false` or the status command fails",
		"Never run the macOS `security` command",
		"print or copy credentials",
		"move credentials into a file",
		"never disable the sandbox globally",
		"never set `HEY_NO_KEYRING=1`",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("embedded HEY skill does not contain %q", want)
		}
	}
}

// Setting a note replaces it, so the skill's recipe for adding to one must not write
// back Markdown that has lost part of the note, nor write after a failed read.
func TestHeySkillAddsToAContactNoteWithoutLosingIt(t *testing.T) {
	data, err := FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"note_markdown_lossless",
		"--jq 'if .data.note_markdown_lossless then .data.note_markdown else error(",
		"note=$(hey contact note show 12345 --jq '.data.note_html') &&",
		`hey contact note set 12345 --note-html "$note`,
		"a failed read must not go on to write",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("embedded HEY skill does not contain %q", want)
		}
	}
}

// Writing a journal entry replaces it, so the skill's recipe for adding to one must not
// write back Markdown that has lost part of the entry, nor write after a failed read.
func TestHeySkillAddsToAJournalEntryWithoutLosingIt(t *testing.T) {
	data, err := FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"content_markdown_lossless",
		"--jq 'if .data.content_markdown_lossless then .data.content_markdown else error(",
		"entry=$(hey journal read 2026-03-15 --jq '.data.content // error(",
		`hey journal write 2026-03-15 --content-html "$entry`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("embedded HEY skill does not contain %q", want)
		}
	}
}
