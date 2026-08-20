package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

func TestEmailCommandHelpKeepsPostingAsAnInternalTerm(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"boxes", "box", "labels", "label", "search", "seen", "unseen", "move", "trash", "spam", "ignore", "stop-ignoring", "watch"} {
		t.Run(name, func(t *testing.T) {
			command, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Join([]string{
				command.Use,
				command.Short,
				command.Long,
				command.Example,
				command.Annotations["agent_notes"],
				command.NonInheritedFlags().FlagUsages(),
			}, "\n")
			if strings.Contains(strings.ToLower(text), "posting") {
				t.Errorf("%s exposes internal posting terminology:\n%s", name, text)
			}
		})
	}
}

func TestEmailActionExamplesCarryTopicKind(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"seen", "unseen", "move", "trash", "spam", "ignore", "stop-ignoring"} {
		t.Run(name, func(t *testing.T) {
			command, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			for _, example := range strings.Split(strings.TrimSpace(command.Example), "\n") {
				if !strings.Contains(example, "--kind topic") {
					t.Errorf("example does not preserve email kind: %q", example)
				}
			}
		})
	}
}

func TestContactCommandHelpUsesHEYTerminology(t *testing.T) {
	root := newRootCmd()
	contacts, _, err := root.Find([]string{"contacts"})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		fmt.Fprintln(&text, command.Use, command.Short, command.Long, command.Example, command.Annotations["agent_notes"])
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(contacts)
	lower := strings.ToLower(text.String())
	if strings.Contains(lower, "reveal") || strings.Contains(lower, "delete a contact") {
		t.Errorf("contact help exposes internal or destructive terminology:\n%s", text.String())
	}
	for _, want := range []string{"hide a contact", "show a hidden contact again", "bundle a contact's mail", "list a contact's mail separately", "delete a private contact note"} {
		if !strings.Contains(lower, want) {
			t.Errorf("contact help missing %q:\n%s", want, text.String())
		}
	}
}

func TestRenderRootHelpUsesUserFacingLanguage(t *testing.T) {
	originalColorDisabled := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = originalColorDisabled })

	var output strings.Builder
	renderRootHelp(&output, newRootCmd())

	const expected = `Read, send, and organize HEY email, contacts, and calendars from your terminal.

USAGE
  hey <command> [flags]
  hey                     Open the interactive app

EMAIL
  boxes          List your HEY boxes
  box            List email and HEY World items in a box
  labels         List your email labels
  label          View and manage an email label
  search         Search email threads and messages
  contacts       Manage contacts
  threads        Read a thread
  attachments    List and save files from a thread
  compose        Write and send a new email
  reply          Reply to a thread
  bulk-reply     Reply to multiple email threads
  forward        Forward the latest message in a thread
  drafts         List draft emails
  seen           Mark email threads as seen
  unseen         Mark email threads as unseen
  move           Move email threads to another box
  trash          Move email threads to Trash
  spam           Mark email threads as spam
  ignore         Ignore email threads
  stop-ignoring  Stop ignoring email threads
  watch          Follow email threads as they change

HEY WORLD
  world  Manage HEY World posts

CALENDAR & TASKS
  calendars   List calendars
  recordings  List events, to-dos, and other calendar entries
  todo        Create and manage to-dos
  habit       Track completed habits
  timetrack   Track time
  journal     Read and write journal entries

AUTH & CONFIG
  auth      Sign in, sign out, and check login status
  accounts  List and select linked mail accounts
  config    View and change settings
  setup     Set up HEY for first use
  doctor    Find login and configuration problems
  upgrade   Upgrade hey to the latest release
  version   Show the installed hey version

FLAGS
      --account    Select a linked mail account ID or all
      --json       Output JSON with metadata
      --jq         Filter JSON with a built-in jq expression
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
