package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// codexRulesMarker opens every rules file hey-cli writes. A hey.rules without
// it is the user's, and nothing in hey-cli rewrites, removes or vouches for it.
const codexRulesMarker = "# Managed by hey-cli."

// CodexReadCommands are the hey commands Codex may run outside its sandbox
// without asking. Each reads HEY or local state and changes neither: no
// command here sends, edits, files, saves to disk or signs in. Codex matches a
// rule as a prefix of the command's words, so "box" also covers "box view".
var CodexReadCommands = []string{
	"search",
	"box",
	"bundle",
	"version",
	"doctor",
	"commands",
	"auth status",
	"account list",
	"account senders",
	"attachment list",
	"bubble list",
	"bulk-reply preview",
	"calendar list",
	"clip list",
	"collection list",
	"collection view",
	"contact list",
	"contact show",
	"contact threads",
	"contact note show",
	"draft list",
	"draft show",
	"event day",
	"event list",
	"event week",
	"habit list",
	"journal list",
	"journal read",
	"label list",
	"label view",
	"screener list",
	"screener history",
	"set-aside view",
	"set-aside group list",
	"set-aside group view",
	"snippet list",
	"thread read",
	"timetrack categories",
	"timetrack current",
	"timetrack list",
	"todo list",
	"workflow list",
	"workflow view",
}

// CodexRulesState is what occupies the path Codex loads hey's rules from.
type CodexRulesState int

const (
	CodexRulesAbsent  CodexRulesState = iota
	CodexRulesCurrent                 // hey-cli's, matching this release
	CodexRulesStale                   // hey-cli's, from another release
	CodexRulesForeign                 // anything hey-cli did not write, symlinks included
)

// CodexRulesPath returns $CODEX_HOME/rules/hey.rules, or "" without a Codex home.
func CodexRulesPath() string {
	codexHome := CodexHome()
	if codexHome == "" {
		return ""
	}
	return filepath.Join(codexHome, "rules", "hey.rules")
}

// CodexRules renders the rules file. Codex drops every user rule when any
// rules file fails to parse, so this emits one fixed line shape only.
func CodexRules() []byte {
	var b strings.Builder
	b.WriteString(codexRulesMarker + " hey setup codex rewrites this file; hey setup agents --remove deletes it.\n")
	b.WriteString("# Lets Codex run these read-only hey commands outside its sandbox, where they can\n")
	b.WriteString("# reach the system keyring and HEY. Every other hey command still asks first.\n")
	for _, command := range CodexReadCommands {
		tokens := append([]string{"hey"}, strings.Fields(command)...)
		fmt.Fprintf(&b, "prefix_rule(pattern = [\"%s\"], decision = \"allow\")\n", strings.Join(tokens, `", "`))
	}
	return []byte(b.String())
}

// InspectCodexRules reports what occupies path. A symlink is foreign whatever
// it points at: its target was never ours to inspect.
func InspectCodexRules(path string) (CodexRulesState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return CodexRulesAbsent, nil
	}
	if err != nil {
		return CodexRulesAbsent, err
	}
	if !info.Mode().IsRegular() {
		return CodexRulesForeign, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the fixed rules path under the Codex home
	if err != nil {
		return CodexRulesAbsent, err
	}
	switch {
	case !bytes.HasPrefix(data, []byte(codexRulesMarker)):
		return CodexRulesForeign, nil
	case bytes.Equal(data, CodexRules()):
		return CodexRulesCurrent, nil
	default:
		return CodexRulesStale, nil
	}
}

// CheckCodexRules checks whether hey's current Codex rules are installed.
func CheckCodexRules() *StatusCheck {
	check := &StatusCheck{Name: "Codex Rules"}
	path := CodexRulesPath()
	if path == "" {
		check.Status, check.Message = "warn", "Cannot determine Codex home directory"
		return check
	}
	state, err := InspectCodexRules(path)
	if err != nil {
		check.Status, check.Message, check.Hint = "warn", "Cannot check Codex rules", err.Error()
		return check
	}
	switch state {
	case CodexRulesCurrent:
		check.Status, check.Message = "pass", "Installed"
	case CodexRulesStale:
		check.Status, check.Message, check.Hint = "fail", "Out of date at "+path, "Run: hey setup codex"
	case CodexRulesForeign:
		check.Status, check.Message, check.Hint = "fail", "A rules file not written by hey-cli occupies "+path, "Move it aside, then run: hey setup codex"
	default:
		check.Status, check.Message, check.Hint = "fail", "Not installed; Codex runs hey reads in its sandbox, without the keyring", "Run: hey setup codex"
	}
	return check
}
