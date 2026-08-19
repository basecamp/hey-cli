package cmd

import (
	"strings"
	"testing"
)

func TestCuratedCommandHelpUsesUserFacingLanguage(t *testing.T) {
	root := newRootCmd()
	tests := map[string]string{
		"auth":  "Sign in to HEY, sign out, or check your login status.",
		"setup": "Sign in and prepare HEY for first use.",
	}

	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			command, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			if command.Long != expected {
				t.Fatalf("unexpected %s description: %q", name, command.Long)
			}
		})
	}
}

func TestRenderRootHelpUsesUserFacingLanguage(t *testing.T) {
	originalColorDisabled := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = originalColorDisabled })

	var output strings.Builder
	renderRootHelp(&output, newRootCmd())

	const expected = `Read, send, and organize HEY email and calendars from your terminal.

USAGE
  hey <command> [flags]
  hey                     Open the interactive app

EMAIL
  boxes    List your HEY boxes
  box      List messages in a box
  threads  Read a thread
  compose  Write and send a new email
  reply    Reply to a thread
  drafts   List draft emails
  seen     Mark messages as seen
  unseen   Mark messages as unseen

CALENDAR & TASKS
  calendars   List calendars
  recordings  List events, to-dos, and other calendar entries
  todo        Create and manage to-dos
  habit       Track completed habits
  timetrack   Track time
  journal     Read and write journal entries

AUTH & CONFIG
  auth    Sign in, sign out, and check login status
  config  View and change settings
  setup   Set up HEY for first use
  doctor  Find login and configuration problems

FLAGS
      --json       Output JSON with metadata
      --markdown   Output as Markdown
      --quiet      Output result data only
  -v, --verbose    Show request details
      --help       Show help
      --version    Show version

EXAMPLES
  $ hey boxes
  $ hey box imbox
  $ hey threads 123
  $ hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"

LEARN MORE
  hey commands      List all available commands
  hey <command> -h  Help for any command
`

	if output.String() != expected {
		t.Fatalf("unexpected root help:\n%s", output.String())
	}
}
