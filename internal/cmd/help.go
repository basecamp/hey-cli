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
		heading: "EMAIL",
		names:   []string{"boxes", "box", "threads", "compose", "reply", "drafts", "seen", "unseen"},
	},
	{
		heading: "CALENDAR & TASKS",
		names:   []string{"calendars", "recordings", "todo", "habit", "timetrack", "journal"},
	},
	{
		heading: "AUTH & CONFIG",
		names:   []string{"auth", "config", "setup", "doctor"},
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

	b.WriteString("CLI for HEY\n")

	// USAGE
	b.WriteString("\n")
	b.WriteString(bold.format("USAGE") + "\n")
	b.WriteString("  hey <command> [flags]\n")
	b.WriteString("  hey                     Launch the interactive TUI\n")

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

	// FLAGS — curated subset of global flags
	b.WriteString("\n")
	b.WriteString(bold.format("FLAGS") + "\n")
	type flagEntry struct {
		short string
		long  string
		desc  string
	}
	flags := []flagEntry{
		{"", "--json", "Output as JSON envelope"},
		{"", "--markdown", "Output as Markdown"},
		{"", "--quiet", "Output raw data without envelope"},
		{"-v", "--verbose", "Increase verbosity"},
		{"", "--help", "Show help for command"},
		{"", "--version", "Show version"},
	}
	for _, f := range flags {
		if f.short != "" {
			fmt.Fprintf(&b, "  %s, %-12s %s\n", f.short, f.long, f.desc)
		} else {
			fmt.Fprintf(&b, "      %-12s %s\n", f.long, f.desc)
		}
	}

	// EXAMPLES
	b.WriteString("\n")
	b.WriteString(bold.format("EXAMPLES") + "\n")
	examples := []string{
		"$ hey boxes",
		"$ hey box imbox",
		"$ hey threads 123",
		`$ hey compose --to "someone@hey.com" -m "Hello!"`,
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

// salientRootFlags is the curated set of root-level global flags shown in
// INHERITED FLAGS for subcommands.
var salientRootFlags = map[string]bool{
	"json":     true,
	"markdown": true,
	"quiet":    true,
	"verbose":  true,
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
// containing only the curated subset of root-level globals.
func filterInheritedFlags(cmd *cobra.Command) string {
	root := cmd.Root()
	filtered := pflag.NewFlagSet("inherited", pflag.ContinueOnError)
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		rootFlag := root.PersistentFlags().Lookup(f.Name)
		if rootFlag == nil || rootFlag != f {
			return // parent-scoped — already promoted to FLAGS
		}
		if !salientRootFlags[f.Name] {
			return
		}
		filtered.AddFlag(f)
	})
	return strings.TrimRight(filtered.FlagUsages(), "\n")
}
