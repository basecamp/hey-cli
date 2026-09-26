package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/basecamp/hey-cli/skills"
)

var (
	skillCodeSpan    = regexp.MustCompile("`([^`\n]+)`")
	skillInvocation  = regexp.MustCompile(`(?:^|[\s(])hey\s+`)
	skillCommandWord = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

// The skill is the first thing an agent reads, so a command it names that the binary does
// not have is the agent's first error — `hey contacts unbundle` and `hey threads` both sat
// in it long after the commands were renamed. Every `hey …` it shows, in a code span, a code
// block or a trigger, must name commands and flags this binary has.
func TestSkillNamesRealCommands(t *testing.T) {
	data, err := skills.FS.ReadFile("hey/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	invocations := skillInvocations(string(data))
	if len(invocations) == 0 {
		t.Fatal("found no hey invocations in the skill")
	}
	for _, invocation := range invocations {
		if problem := checkSkillInvocation(root, invocation); problem != "" {
			t.Errorf("hey %s: %s", invocation, problem)
		}
	}
}

func TestSkillInvocationCheckCatchesStaleNames(t *testing.T) {
	root := newRootCmd()
	for _, invocation := range []string{
		"threads 12345",
		"contacts unbundle 12345",
		"contact note set 12345 --notes \"Prefers email\"",
		"contact note sett 12345",
		"label add|tag 12345 --to 789",
	} {
		if problem := checkSkillInvocation(root, invocation); problem == "" {
			t.Errorf("hey %s was accepted", invocation)
		}
	}
	for _, invocation := range []string{
		"contact note set 12345 --note-html \"<p>Prefers email</p>\"",
		"--account 12345 box list --json",
		"box view imbox --page next-cursor --json",
		"contact bundle|unbundle <contact_id>",
		"draft edit <draft_id> --subject/--to/--cc/--bcc/-m (flags replace)",
		"login / hey logout",
		"timetrack export -o tracked-time.csv --json",
	} {
		if problem := checkSkillInvocation(root, invocation); problem != "" {
			t.Errorf("hey %s was refused: %s", invocation, problem)
		}
	}
}

// skillInvocations answers what follows each `hey ` in the places the skill shows commands:
// its triggers, its code blocks and its code spans. Prose is left alone.
func skillInvocations(skill string) []string {
	var sources []string
	inFrontmatter, inFence := false, false
	for i, line := range strings.Split(skill, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case i == 0 && trimmed == "---":
			inFrontmatter = true
		case inFrontmatter:
			if trimmed == "---" {
				inFrontmatter = false
			} else if trigger, ok := strings.CutPrefix(trimmed, "- "); ok {
				sources = append(sources, trigger)
			}
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
		case inFence:
			sources = append(sources, line)
		default:
			for _, span := range skillCodeSpan.FindAllStringSubmatch(line, -1) {
				sources = append(sources, span[1])
			}
		}
	}

	var invocations []string
	for _, source := range sources {
		matches := skillInvocation.FindAllStringIndex(source, -1)
		for i, match := range matches {
			end := len(source)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			invocations = append(invocations, strings.TrimSpace(source[match[1]:end]))
		}
	}
	return invocations
}

// checkSkillInvocation walks one invocation down the command tree and answers what is
// wrong with it, or nothing. Words descend while they name subcommands; the first word
// that does not is an argument, unless the command cannot take one. Flags are looked up
// on the command reached so far.
func checkSkillInvocation(root *cobra.Command, invocation string) string {
	command := root
	descending := true
	tokens := skillTokens(invocation)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case skillStopToken(token):
			return ""
		case strings.HasPrefix(token, "-") && len(token) > 1:
			compound := strings.Contains(token, "/")
			for _, name := range strings.Split(token, "/") {
				flag := lookupSkillFlag(command, name)
				if flag == nil {
					return fmt.Sprintf("%s has no flag %s", command.CommandPath(), name)
				}
				if !compound && flag.NoOptDefVal == "" && !strings.Contains(name, "=") {
					i++
				}
			}
		case descending && strings.Contains(token, "|") && skillAlternatives(token):
			for _, name := range strings.Split(token, "|") {
				if skillSubcommand(command, name) == nil {
					return fmt.Sprintf("%s has no subcommand %s", command.CommandPath(), name)
				}
			}
			return ""
		case descending:
			if sub := skillSubcommand(command, token); sub != nil {
				command = sub
				continue
			}
			if command == root || !command.Runnable() {
				return fmt.Sprintf("%s has no subcommand %s", command.CommandPath(), token)
			}
			descending = false
		}
	}
	return ""
}

// skillTokens splits an invocation on whitespace the way a shell would, keeping a quoted
// message or jq expression in one token.
func skillTokens(invocation string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	inToken := false
	for _, r := range invocation {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			current.WriteRune(r)
			inToken = true
		case unicode.IsSpace(r):
			if inToken {
				tokens = append(tokens, current.String())
				current.Reset()
				inToken = false
			}
		default:
			current.WriteRune(r)
			inToken = true
		}
	}
	if inToken {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// skillStopToken is where an invocation ends and something else — a pipe, a redirect, a
// comment, the next command — begins.
func skillStopToken(token string) bool {
	switch token {
	case "|", "||", ">", ">>", "&&", ";", "/":
		return true
	}
	return strings.HasPrefix(token, "#") || strings.HasPrefix(token, "2>")
}

func skillAlternatives(token string) bool {
	for _, name := range strings.Split(token, "|") {
		if !skillCommandWord.MatchString(name) {
			return false
		}
	}
	return true
}

func skillSubcommand(command *cobra.Command, name string) *cobra.Command {
	for _, sub := range command.Commands() {
		if sub.Name() == name || sub.HasAlias(name) {
			return sub
		}
	}
	return nil
}

func lookupSkillFlag(command *cobra.Command, token string) *pflag.Flag {
	name, _, _ := strings.Cut(token, "=")
	sets := []*pflag.FlagSet{command.Flags(), command.PersistentFlags(), command.InheritedFlags()}
	for _, set := range sets {
		var flag *pflag.Flag
		if long, ok := strings.CutPrefix(name, "--"); ok {
			flag = set.Lookup(long)
		} else if short := strings.TrimPrefix(name, "-"); len(short) == 1 {
			flag = set.ShorthandLookup(short)
		}
		if flag != nil {
			return flag
		}
	}
	return nil
}
