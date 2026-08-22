package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestCuratedCommandHelpUsesUserFacingLanguage(t *testing.T) {
	root := newRootCmd()
	tests := map[string]string{
		"auth":  "Sign in to HEY, sign out, or check your login status.",
		"setup": "Sign in and connect your coding agents.",
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
	for _, name := range []string{"boxes", "box", "labels", "label", "workflows", "workflow", "clips", "clip", "snippets", "snippet", "search", "seen", "unseen", "move", "trash", "spam", "ignore", "stop-ignoring", "watch"} {
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
  hey tui                 Open the interactive app

INTERACTIVE
  tui  Launch the interactive terminal UI

EMAIL
  boxes          List your HEY boxes
  box            List email threads in a box
  labels         List your email labels
  label          View and manage an email label
  collections    List your email collections
  collection     View and manage an email collection
  workflows      List your email workflows
  workflow       View and manage an email workflow
  clips          List the newest page of passages clipped from email
  clip           Save and manage passages from email
  snippets       List reusable email snippets
  snippet        Create and manage reusable email snippets
  search         Search email threads and messages
  contacts       Manage contacts
  screener       Decide who gets to email you
  threads        Read a thread
  share          Get a sharing link for an email thread
  unshare        Turn off an email thread's sharing link
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

CALENDAR & TASKS
  calendars   List calendars
  recordings  List events, to-dos, and other calendar entries
  todo        Create and manage to-dos
  habit       Create and manage habits
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
      --markdown   Output Markdown: a table for a listing, a document for a thread
      --quiet      Output result data only
      --ids-only   Output only IDs, one per line
      --count      Output only the count of results
      --styled     Human rendering, bodies as rendered Markdown — the default on a terminal; forces it when piped
      --html       Write the original HTML to a pipe or file (threads, journal read, contacts show, contacts note show)
      --stats      Include request stats in response meta
      --base-url   Override server URL
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

func TestHelpListsEveryGlobalFlag(t *testing.T) {
	originalColorDisabled := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = originalColorDisabled })

	root := newRootCmd()

	var global []string
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			global = append(global, f.Name)
		}
	})
	if len(global) == 0 {
		t.Fatal("the root command registers no global flags")
	}

	var rootHelp strings.Builder
	renderRootHelp(&rootHelp, root)
	for _, name := range global {
		if !strings.Contains(rootHelp.String(), "--"+name+" ") {
			t.Errorf("root help does not list the global --%s:\n%s", name, rootHelp.String())
		}
	}
	if strings.Contains(rootHelp.String(), "--agent") {
		t.Errorf("root help lists the hidden --agent:\n%s", rootHelp.String())
	}

	for _, command := range descendants(root) {
		t.Run(command.CommandPath(), func(t *testing.T) {
			var help bytes.Buffer
			command.SetOut(&help)
			renderCommandHelp(command)

			for _, name := range global {
				if !strings.Contains(help.String(), "--"+name+" ") {
					t.Errorf("%s help does not list the global --%s:\n%s", command.CommandPath(), name, help.String())
				}
			}
			if strings.Contains(help.String(), "--agent") {
				t.Errorf("%s help lists the hidden --agent:\n%s", command.CommandPath(), help.String())
			}
		})
	}
}

func descendants(command *cobra.Command) []*cobra.Command {
	found := make([]*cobra.Command, 0, len(command.Commands()))
	for _, child := range command.Commands() {
		found = append(found, child)
		found = append(found, descendants(child)...)
	}
	return found
}
