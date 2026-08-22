package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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

CORE COMMANDS
  tui        Launch the interactive terminal UI
  box        List HEY boxes and their email threads
  threads    Read a thread
  reply      Reply to a thread
  compose    Write and send a new email
  search     Search email threads and messages
  contacts   Manage contacts
  calendars  List calendars
  todo       Create and manage to-dos
  journal    Read and write journal entries

MAIL
  screener     Decide who gets to email you
  attachments  List and save files from a thread
  drafts       List draft emails
  watch        Follow email threads as they change

WRITE & SHARE
  bulk-reply  Reply to multiple email threads
  forward     Forward the latest message in a thread
  share       Get a sharing link for an email thread
  unshare     Turn off an email thread's sharing link

SAVED CONTENT
  clip     List and manage passages saved from email
  snippet  List and manage reusable email snippets

ORGANIZE
  label          List and manage email labels
  collection     List and manage email collections
  workflow       List and manage email workflows
  seen           Mark email threads as seen
  unseen         Mark email threads as unseen
  move           Move email threads to another box
  trash          Move email threads to Trash
  spam           Mark email threads as spam
  ignore         Ignore email threads
  stop-ignoring  Stop ignoring email threads

CALENDAR & TASKS
  recordings  List events, to-dos, and other calendar entries
  habit       Create and manage habits
  timetrack   Track time

ACCOUNT & SYSTEM
  auth      Sign in, sign out, and check login status
  accounts  List and select linked mail accounts
  config    View and change settings
  setup     Set up HEY for first use
  doctor    Find login and configuration problems
  upgrade   Upgrade hey to the latest release
  version   Show the installed hey version

HELP TOPICS
  output           Output formats and filtering
  exit-codes       Exit status reference
  environment      Environment variable reference
  linked-accounts  Linked account selection

FLAGS
      --account    Select a linked mail account
      --json       Output a JSON response envelope
      --jq         Filter JSON with a jq expression
      --markdown   Output Markdown
      --quiet      Output result data without the response envelope
      --ids-only   Output only IDs, one per line
      --count      Output only the result count
      --styled     Force human-readable terminal output
      --html       Write original HTML
      --stats      Include request statistics
      --base-url   Override the server URL
  -v, --verbose    Show request details
  -h, --help       Show help
      --version    Show version

EXAMPLES
  $ hey tui
  $ hey box view imbox
  $ hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"
  $ hey todo list
  $ hey threads 123 --json

LEARN MORE
  hey commands          List all available commands
  hey help <topic>      Read a help topic
  hey <command> --help  Help for any command
`

	if output.String() != expected {
		t.Fatalf("unexpected root help:\n%s", output.String())
	}
}

func TestRootHelpStaysScannable(t *testing.T) {
	originalColorDisabled := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = originalColorDisabled })

	var output strings.Builder
	renderRootHelp(&output, newRootCmd())
	for _, line := range strings.Split(output.String(), "\n") {
		if len(line) > 100 {
			t.Errorf("root help line is %d columns, want no more than 100:\n%s", len(line), line)
		}
	}

	seen := make(map[string]string)
	for _, category := range curatedCategories {
		if len(category.names) > 13 {
			t.Errorf("%s has %d commands, want no more than 13", category.heading, len(category.names))
		}
		for _, name := range category.names {
			if previous := seen[name]; previous != "" {
				t.Errorf("%s appears in both %s and %s", name, previous, category.heading)
			}
			seen[name] = category.heading
		}
	}
}

func TestHelpTopicsAreDiscoverableReferences(t *testing.T) {
	root := newRootCmd()
	catalog := walkCommands(root, "")

	for _, name := range curatedHelpTopics {
		t.Run(name, func(t *testing.T) {
			topic, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			if !topic.IsAdditionalHelpTopicCommand() {
				t.Fatalf("%s is not registered as a help topic", name)
			}

			var output strings.Builder
			topic.SetOut(&output)
			renderHelpTopic(topic)
			if !strings.Contains(output.String(), topic.Long) {
				t.Errorf("%s help does not contain its reference text", name)
			}
			if strings.Contains(output.String(), "INHERITED FLAGS") || strings.Contains(output.String(), "USAGE") {
				t.Errorf("%s help contains command scaffolding:\n%s", name, output.String())
			}

			for _, entry := range catalog {
				if entry["name"] == name {
					t.Errorf("help topic %s appears in the executable command catalog", name)
				}
			}
		})
	}
}

func TestHelpReferencesSkipRuntimeSetup(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HEY_TOKEN", "would-authenticate-a-runtime-command")
	t.Setenv("HEY_ACCOUNT_ID", "999")

	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".hey"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".hey", "config.json"), []byte(`{"base_url":"http://127.0.0.1:1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repository)

	root := newRootCmd()
	var output strings.Builder
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "output"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help output: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "DEFAULT OUTPUT") {
		t.Errorf("help topic was not rendered:\n%s", output.String())
	}
}

func TestHelpCompletionIncludesCommandsAndTopics(t *testing.T) {
	root := newRootCmd()
	help, _, err := root.Find([]string{"help"})
	if err != nil {
		t.Fatal(err)
	}
	completions, directive := help.ValidArgsFunction(help, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("completion directive = %v, want no file completion", directive)
	}

	joined := strings.Join(completions, "\n")
	for _, want := range []string{"box\t", "output\t", "exit-codes\t", "environment\t", "linked-accounts\t"} {
		if !strings.Contains(joined, want) {
			t.Errorf("help completion does not include %q:\n%s", want, joined)
		}
	}
}

func TestRunnableParentHelpShowsBothUsageForms(t *testing.T) {
	root := newRootCmd()
	search, _, err := root.Find([]string{"search"})
	if err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	search.SetOut(&output)
	renderCommandHelp(search)
	for _, want := range []string{"hey search [query] [flags]", "hey search <command> [flags]"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("search help does not contain %q:\n%s", want, output.String())
		}
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
