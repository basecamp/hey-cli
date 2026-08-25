package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// curatedCategories defines the subset of categories and commands shown in root help.
// Commands not listed here are discoverable via `hey commands`.
var curatedCategories = []struct {
	heading string
	names   []string
}{
	{
		heading: "CORE COMMANDS",
		names:   []string{"tui", "box", "thread", "reply", "compose", "search", "contact", "calendar", "journal"},
	},
	{
		heading: "MAIL",
		names:   []string{"screener", "attachment", "draft", "watch"},
	},
	{
		heading: "WRITE & SHARE",
		names:   []string{"bulk-reply", "forward", "share", "unshare"},
	},
	{
		heading: "SAVED CONTENT",
		names:   []string{"clip", "snippet"},
	},
	{
		heading: "ORGANIZE",
		names:   []string{"label", "collection", "workflow", "seen", "unseen", "bubble-up-now", "pop", "move", "trash", "spam", "ignore", "stop-ignoring"},
	},
	{
		heading: "CALENDAR & TASKS",
		names:   []string{"event", "todo", "habit", "timetrack"},
	},
	{
		heading: "ACCOUNT & SYSTEM",
		names:   []string{"auth", "account", "config", "setup", "shell-completion", "doctor", "upgrade", "version"},
	},
}

type helpEntry struct {
	name string
	desc string
}

func configureHelpCommand(root *cobra.Command) {
	root.InitDefaultHelpCmd()
	help, _, err := root.Find([]string{"help"})
	if err != nil {
		panic(err)
	}
	help.Hidden = true
	help.ValidArgsFunction = completeHelpReference
}

func completeHelpReference(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	target := cmd.Root()
	if len(args) > 0 {
		found, _, err := target.Find(args)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		target = found
	}

	var completions []cobra.Completion
	for _, sub := range target.Commands() {
		if sub.Hidden || (!sub.IsAvailableCommand() && !sub.IsAdditionalHelpTopicCommand()) {
			continue
		}
		if strings.HasPrefix(sub.Name(), toComplete) {
			completions = append(completions, cobra.CompletionWithDesc(sub.Name(), sub.Short))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

func isHelpReference(cmd *cobra.Command) bool {
	return cmd.Name() == "help" || cmd.IsAdditionalHelpTopicCommand()
}

// customHelpFunc returns a help function that renders styled help for all
// commands: agent JSON when --agent is set, curated categories for root,
// and a consistent styled layout for every subcommand.
func customHelpFunc(defaultHelp func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		if agentFlag {
			printAgentHelp(cmd)
			return
		}
		if cmd == cmd.Root() {
			renderRootHelp(cmd.OutOrStdout(), cmd)
			return
		}
		if cmd.IsAdditionalHelpTopicCommand() {
			renderHelpTopic(cmd)
			return
		}
		renderCommandHelp(cmd)
	}
}

func renderRootHelp(w io.Writer, cmd *cobra.Command) {
	var b strings.Builder

	b.WriteString("Read, send, and organize HEY email, contacts, and calendars from your terminal.\n")

	// USAGE
	b.WriteString("\n")
	b.WriteString(bold.format("USAGE") + "\n")
	b.WriteString("  hey <command> [flags]\n")
	b.WriteString("  hey tui                 Open the interactive app\n")

	// Build lookup from command name → registered cobra.Command
	registered := make(map[string]*cobra.Command, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		registered[sub.Name()] = sub
	}

	// Render curated categories
	for _, cat := range curatedCategories {
		var entries []helpEntry
		maxName := 0
		for _, name := range cat.names {
			sub := registered[name]
			if sub == nil {
				continue
			}
			entries = append(entries, helpEntry{name: name, desc: sub.Short})
			if len(name) > maxName {
				maxName = len(name)
			}
		}
		if len(entries) == 0 {
			continue
		}

		b.WriteString("\n")
		b.WriteString(bold.format(cat.heading) + "\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "  %-*s  %s\n", maxName, e.name, e.desc)
		}
	}

	// HELP TOPICS
	b.WriteString("\n")
	b.WriteString(bold.format("HELP TOPICS") + "\n")
	for _, name := range curatedHelpTopics {
		topic := registered[name]
		if topic == nil {
			continue
		}
		fmt.Fprintf(&b, "  %-15s  %s\n", topic.Name(), topic.Short)
	}

	// FLAGS — the global flags, as registered on the root command
	b.WriteString("\n")
	b.WriteString(bold.format("FLAGS") + "\n")
	for _, f := range globalFlags(cmd) {
		description := f.Usage
		if concise, ok := rootFlagDescriptions[f.Name]; ok {
			description = concise
		}
		writeFlagLine(&b, f.Shorthand, "--"+f.Name, description)
	}
	// Cobra owns --help and --version, so neither is a persistent flag to read.
	writeFlagLine(&b, "h", "--help", "Show help")
	writeFlagLine(&b, "", "--version", "Show version")

	// EXAMPLES
	b.WriteString("\n")
	b.WriteString(bold.format("EXAMPLES") + "\n")
	examples := []string{
		"$ hey tui",
		"$ hey box view imbox",
		`$ hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"`,
		"$ hey todo list",
		"$ hey thread read 123 --json",
	}
	for _, ex := range examples {
		b.WriteString(italic.format("  "+ex) + "\n")
	}

	// LEARN MORE
	b.WriteString("\n")
	b.WriteString(bold.format("LEARN MORE") + "\n")
	b.WriteString("  hey commands          List all available commands\n")
	b.WriteString("  hey help <topic>      Read a help topic\n")
	b.WriteString("  hey <command> --help  Help for any command\n")

	fmt.Fprint(w, b.String())
}

func renderHelpTopic(cmd *cobra.Command) {
	fmt.Fprintln(cmd.OutOrStdout(), cmd.Long)
}

// renderCommandHelp renders styled help for any non-root command, reading
// structure from cobra's command tree rather than hardcoding per-command.
func renderCommandHelp(cmd *cobra.Command) {
	w := cmd.OutOrStdout()
	var b strings.Builder

	// Description
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	if desc != "" {
		b.WriteString(desc)
		b.WriteString("\n")
	}

	// USAGE
	b.WriteString("\n")
	b.WriteString(bold.format("USAGE") + "\n")
	_, hasCompatibilityUsage := cmd.Annotations[compatibilityUsageAnnotation]
	if cmd.Runnable() && !hasCompatibilityUsage {
		b.WriteString("  " + cmd.UseLine() + "\n")
	}
	if cmd.HasAvailableSubCommands() {
		b.WriteString("  " + cmd.CommandPath() + " <command> [flags]\n")
	}

	// ALIASES
	if len(cmd.Aliases) > 0 {
		b.WriteString("\n")
		b.WriteString(bold.format("ALIASES") + "\n")
		b.WriteString("  " + cmd.Name())
		for _, a := range cmd.Aliases {
			b.WriteString(", " + a)
		}
		b.WriteString("\n")
	}

	// COMMANDS
	if cmd.HasAvailableSubCommands() {
		var entries []helpEntry
		maxName := 0
		for _, sub := range cmd.Commands() {
			if !sub.IsAvailableCommand() {
				continue
			}
			entries = append(entries, helpEntry{name: sub.Name(), desc: sub.Short})
			if len(sub.Name()) > maxName {
				maxName = len(sub.Name())
			}
		}
		b.WriteString("\n")
		b.WriteString(bold.format("COMMANDS") + "\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "  %-*s  %s\n", maxName, e.name, e.desc)
		}
	}

	// FLAGS — local flags plus parent-scoped persistent flags
	merged := pflag.NewFlagSet("flags", pflag.ContinueOnError)
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) { merged.AddFlag(f) })
	parentScopedFlags(cmd).VisitAll(func(f *pflag.Flag) {
		if merged.Lookup(f.Name) == nil {
			merged.AddFlag(f)
		}
	})
	flagsUsage := strings.TrimRight(merged.FlagUsages(), "\n")
	if flagsUsage != "" {
		b.WriteString("\n")
		b.WriteString(bold.format("FLAGS") + "\n")
		b.WriteString(flagsUsage)
		b.WriteString("\n")
	}

	// INHERITED FLAGS — root-level globals only
	inherited := filterInheritedFlags(cmd)
	if inherited != "" {
		b.WriteString("\n")
		b.WriteString(bold.format("INHERITED FLAGS") + "\n")
		b.WriteString(inherited)
		b.WriteString("\n")
	}

	// EXAMPLES
	if cmd.Example != "" {
		b.WriteString("\n")
		b.WriteString(bold.format("EXAMPLES") + "\n")
		for _, line := range strings.Split(cmd.Example, "\n") {
			b.WriteString(italic.format(line) + "\n")
		}
	}

	// LEARN MORE
	b.WriteString("\n")
	b.WriteString(bold.format("LEARN MORE") + "\n")
	if cmd.HasAvailableSubCommands() {
		b.WriteString("  " + cmd.CommandPath() + " <command> --help\n")
	} else if cmd.HasParent() {
		b.WriteString("  " + cmd.Parent().CommandPath() + " --help\n")
	}

	fmt.Fprint(w, b.String())
}

// globalFlagReadingOrder is how the global flags read in root help: the
// account first, then the output shaping, then verbosity. It orders the
// flags rather than choosing them, so a flag registered without a place
// here still gets listed, at the end.
var globalFlagReadingOrder = []string{"account", "json", "jq", "markdown", "quiet", "ids-only", "count", "styled", "html", "stats", "base-url", "verbose"}

var rootFlagDescriptions = map[string]string{
	"account":  "Select a linked mail account",
	"json":     "Output a JSON response envelope",
	"jq":       "Filter JSON with a jq expression",
	"markdown": "Output Markdown",
	"quiet":    "Output result data without the response envelope",
	"ids-only": "Output only IDs, one per line",
	"count":    "Output only the result count",
	"styled":   "Force human-readable terminal output",
	"html":     "Write original HTML",
	"stats":    "Include request statistics",
	"base-url": "Override the server URL",
	"verbose":  "Show request details",
}

// globalFlags returns the root command's persistent flags — every flag help
// calls global, described by the registration in root.go — in reading order,
// leaving out the hidden ones.
func globalFlags(cmd *cobra.Command) []*pflag.Flag {
	persistent := cmd.Root().PersistentFlags()

	var ordered []*pflag.Flag
	listed := map[string]bool{}
	for _, name := range globalFlagReadingOrder {
		if f := persistent.Lookup(name); f != nil && !f.Hidden {
			ordered = append(ordered, f)
			listed[name] = true
		}
	}
	persistent.VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && !listed[f.Name] {
			ordered = append(ordered, f)
		}
	})

	return ordered
}

func writeFlagLine(b *strings.Builder, shorthand, name, desc string) {
	if shorthand != "" {
		fmt.Fprintf(b, "  -%s, %-12s %s\n", shorthand, name, desc)
	} else {
		fmt.Fprintf(b, "      %-12s %s\n", name, desc)
	}
}

// parentScopedFlags returns inherited flags that originate from a non-root
// parent command. These are promoted into the FLAGS section so they're
// immediately visible on leaf commands.
func parentScopedFlags(cmd *cobra.Command) *pflag.FlagSet {
	root := cmd.Root()
	ps := pflag.NewFlagSet("parent-scoped", pflag.ContinueOnError)
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		rootFlag := root.PersistentFlags().Lookup(f.Name)
		if rootFlag != nil && rootFlag == f {
			return // root-level — stays in INHERITED FLAGS
		}
		ps.AddFlag(f)
	})
	return ps
}

// filterInheritedFlags returns formatted flag usages for INHERITED FLAGS,
// containing the global flags this command actually inherits. Parent-scoped
// flags are left out — they are already promoted to FLAGS.
func filterInheritedFlags(cmd *cobra.Command) string {
	inherited := cmd.InheritedFlags()
	filtered := pflag.NewFlagSet("inherited", pflag.ContinueOnError)
	for _, f := range globalFlags(cmd) {
		if inherited.Lookup(f.Name) == f {
			filtered.AddFlag(f)
		}
	}
	return strings.TrimRight(filtered.FlagUsages(), "\n")
}
