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
		heading: "INTERACTIVE",
		names:   []string{"tui"},
	},
	{
		heading: "EMAIL",
		names:   []string{"boxes", "box", "labels", "label", "collections", "collection", "workflows", "workflow", "snippets", "snippet", "search", "contacts", "screener", "threads", "share", "unshare", "attachments", "compose", "reply", "bulk-reply", "forward", "drafts", "seen", "unseen", "move", "trash", "spam", "ignore", "stop-ignoring", "watch"},
	},
	{
		heading: "CALENDAR & TASKS",
		names:   []string{"calendars", "recordings", "todo", "habit", "timetrack", "journal"},
	},
	{
		heading: "AUTH & CONFIG",
		names:   []string{"auth", "accounts", "config", "setup", "doctor", "upgrade", "version"},
	},
}

type helpEntry struct {
	name string
	desc string
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

	// FLAGS — the global flags, as registered on the root command
	b.WriteString("\n")
	b.WriteString(bold.format("FLAGS") + "\n")
	for _, f := range globalFlags(cmd) {
		writeFlagLine(&b, f.Shorthand, "--"+f.Name, f.Usage)
	}
	// Cobra owns --help and --version, so neither is a persistent flag to read.
	writeFlagLine(&b, "", "--help", "Show help")
	writeFlagLine(&b, "", "--version", "Show version")

	// EXAMPLES
	b.WriteString("\n")
	b.WriteString(bold.format("EXAMPLES") + "\n")
	examples := []string{
		"$ hey boxes",
		"$ hey box imbox",
		"$ hey threads 123",
		`$ hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"`,
	}
	for _, ex := range examples {
		b.WriteString(italic.format("  "+ex) + "\n")
	}

	// LEARN MORE
	b.WriteString("\n")
	b.WriteString(bold.format("LEARN MORE") + "\n")
	b.WriteString("  hey commands      List all available commands\n")
	b.WriteString("  hey <command> -h  Help for any command\n")

	fmt.Fprint(w, b.String())
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
	if cmd.HasAvailableSubCommands() && !cmd.Runnable() {
		b.WriteString("  " + cmd.CommandPath() + " <command> [flags]\n")
	} else {
		b.WriteString("  " + cmd.UseLine() + "\n")
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
