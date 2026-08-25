package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
)

// agentSetupEnv selects which coding agents `setup agents` targets.
// Values: claude | codex | all | none. Empty (unset) means auto-detect.
const agentSetupEnv = "HEY_SETUP_AGENT"

// newSetupAgentsCommand builds `hey setup agents`. It always runs
// non-interactively: it installs the baseline skill, connects agents per the
// HEY_SETUP_AGENT selector (or auto-detection), and emits a structured
// envelope. It never prompts, so it is safe for the piped installer and
// coding-agent shells.
func newSetupAgentsCommand() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Install or remove HEY coding-agent integrations",
		Long: "Install the baseline HEY agent skill and attempt to connect coding agents.\n\n" +
			"Selection is controlled by " + agentSetupEnv + ": claude, codex, all, or none. When\n" +
			"unset, a single detected agent is connected; when several are detected none is\n" +
			"guessed — the per-agent `hey setup <id>` commands are surfaced instead. Use\n" +
			"--remove to uninstall the HEY integrations and managed skill files.",
		// Selection is env-driven; positional args are always a mistake (typo,
		// or confusion with `setup <id>`). Reject them rather than silently ignore.
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"agent_notes": "Never prompts. Set " + agentSetupEnv + "=claude|codex|all|none to choose; unset auto-detects a single agent. --remove uninstalls HEY's managed agent integrations.",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if remove {
				return runRemoveAgentSetup(cmd)
			}
			return runNonInteractiveAgentSetup(cmd)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "Remove HEY coding-agent integrations")
	return cmd
}

// agentSetupRecord is the per-agent outcome captured while running handlers.
// errors/manualCommands are stored bare (not id-prefixed); the top-level union
// prefixes errors with the agent id.
type agentSetupRecord struct {
	id, name        string
	detectedBefore  bool
	detectedAfter   bool
	pluginInstalled bool
	binaryAbsent    bool
	errors          []string
	manualCommands  []string
}

// runNonInteractiveAgentSetup installs the baseline skill, resolves the agent
// selector, runs each targeted handler, and returns a structured envelope
// where every safety-critical outcome lives in a top-level flat field (the
// `agents` array is per-agent detail).
func runNonInteractiveAgentSetup(cmd *cobra.Command) error {
	if err := rejectListOnlyFormats("hey setup agents"); err != nil {
		return err
	}
	// Baseline skill: installed regardless of selector.
	_, skillErr := installSkillFiles()
	skillInstalled := skillErr == nil

	detectedBefore := detectedAgentIDs()

	selectorRaw := strings.TrimSpace(os.Getenv(agentSetupEnv))
	selector := strings.ToLower(selectorRaw)

	var warnings []string
	var ambiguous bool
	var ambiguousManual []string
	var targets []harness.AgentInfo

	switch selector {
	case "", "auto":
		selector = "auto"
		detected := harness.DetectedAgents()
		switch len(detected) {
		case 0:
			// baseline skill only
		case 1:
			targets = detected
		default:
			ambiguous = true
			ambiguousManual = agentChoiceCommands(detected)
			warnings = append(warnings, "Multiple coding agents detected; installed the baseline skill only. Choose one: "+strings.Join(ambiguousManual, ", "))
		}
	case "all":
		targets = harness.AllAgents()
	case "none":
		// baseline skill only
	case "claude", "codex":
		if a := harness.FindAgent(selector); a != nil {
			targets = []harness.AgentInfo{*a}
		}
	default:
		selector = "invalid"
		warnings = append(warnings, fmt.Sprintf("Unknown %s value %q; installed the baseline skill only (expected claude, codex, all, or none)", agentSetupEnv, selectorRaw))
	}

	// Run handlers in id order so aggregation is deterministic.
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	records := make([]agentSetupRecord, 0, len(targets))
	for _, agent := range targets {
		records = append(records, runAgentSetupHandler(cmd, agent))
	}

	attempted := make([]string, 0, len(records))
	for _, r := range records {
		attempted = append(attempted, r.id)
	}

	// errors: union of baseline + every per-agent error (id-prefixed), stable
	// first-seen dedup, in sorted-agent order.
	errUnion := newOrderedStringSet()
	if skillErr != nil {
		errUnion.add(fmt.Sprintf("skill: %s", skillErr))
	}
	for _, r := range records {
		for _, e := range r.errors {
			errUnion.add(fmt.Sprintf("%s: %s", r.id, e))
		}
	}

	// manual_commands: ambiguous → every `setup <id>`; else union of each
	// handler's own ordered sequence plus a synthesized hint for absent
	// binaries.
	manualUnion := newOrderedStringSet()
	if ambiguous {
		for _, m := range ambiguousManual {
			manualUnion.add(m)
		}
	} else {
		for _, r := range records {
			for _, m := range r.manualCommands {
				manualUnion.add(m)
			}
			if r.binaryAbsent && !r.pluginInstalled {
				manualUnion.add("hey setup " + r.id)
			}
		}
	}

	// warnings: synthesized missing-binary remediation, sorted-agent order.
	// Only when the absence actually prevented the connection — Codex is
	// routinely detected by its home directory alone and its skill-only
	// setup succeeds without a binary.
	for _, r := range records {
		if r.binaryAbsent && !r.pluginInstalled {
			warnings = append(warnings, fmt.Sprintf("%s: %s binary not found; install %s, then run: hey setup %s", r.id, r.name, r.name, r.id))
		}
	}

	agentsDetail := make([]map[string]any, 0, len(records))
	for _, r := range records {
		agentsDetail = append(agentsDetail, map[string]any{
			"id":               r.id,
			"name":             r.name,
			"detected_before":  r.detectedBefore,
			"detected_after":   r.detectedAfter,
			"plugin_installed": r.pluginInstalled,
			"errors":           orEmptyStrings(r.errors),
			"manual_commands":  orEmptyStrings(r.manualCommands),
		})
	}

	result := map[string]any{
		"skill_installed":  skillInstalled,
		"selector":         selector,
		"ambiguous":        ambiguous,
		"detected_before":  detectedBefore,
		"attempted_agents": attempted,
		"errors":           errUnion.slice(),
		"warnings":         orEmptyStrings(warnings),
		"manual_commands":  manualUnion.slice(),
		"agents":           agentsDetail,
	}

	manual := manualUnion.slice()
	breadcrumbs := make([]output.Breadcrumb, 0, 1+len(manual))
	breadcrumbs = append(breadcrumbs, output.Breadcrumb{Action: "doctor", Command: "hey doctor", Description: "Check CLI health"})
	for i, m := range manual {
		breadcrumbs = append(breadcrumbs, output.Breadcrumb{
			Action:      fmt.Sprintf("manual_step_%d", i+1),
			Command:     m,
			Description: "Manual setup step",
		})
	}

	return writeOK(result,
		output.WithSummary(agentSetupSummary(selector, ambiguous, skillInstalled, records)),
		output.WithBreadcrumbs(breadcrumbs...),
	)
}

// runAgentSetupHandler runs one agent's non-interactive handler and captures
// its before/after detection, plugin health, and remediation.
func runAgentSetupHandler(cmd *cobra.Command, agent harness.AgentInfo) agentSetupRecord {
	rec := agentSetupRecord{
		id:             agent.ID,
		name:           agent.Name,
		detectedBefore: agent.Detect != nil && agent.Detect(),
		binaryAbsent:   !agentBinaryPresent(agent.ID),
	}

	if handler, ok := agentSetupHandlers[agent.ID]; ok && handler.RunNonInteractive != nil {
		if err := handler.RunNonInteractive(cmd); err != nil {
			rec.errors = append(rec.errors, err.Error())
			var setupErr *agentSetupError
			if errors.As(err, &setupErr) {
				rec.manualCommands = append(rec.manualCommands, setupErr.Manual...)
			}
		}
	}

	rec.detectedAfter = agent.Detect != nil && agent.Detect()
	// Connected means the handler succeeded AND health checks pass. Checks
	// alone are not enough: an unmanaged skill at the canonical path makes
	// the handler refuse while the presence check still passes — that is a
	// conflict, not a connection.
	rec.pluginInstalled = len(rec.errors) == 0 && agentChecksPass(agent)
	return rec
}

// agentBinaryPresent reports whether the agent's executable is on disk.
// Unknown agents are assumed present so no bogus remediation is synthesized.
func agentBinaryPresent(id string) bool {
	switch id {
	case "claude":
		return harness.FindClaudeBinary() != ""
	case "codex":
		return harness.FindCodexBinary() != ""
	default:
		return true
	}
}

// detectedAgentIDs returns the ids of currently detected agents, sorted.
func detectedAgentIDs() []string {
	agents := harness.DetectedAgents()
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return ids
}

// agentChoiceCommands returns `hey setup <id>` for each agent, sorted by id.
func agentChoiceCommands(agents []harness.AgentInfo) []string {
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	cmds := make([]string, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, "hey setup "+id)
	}
	return cmds
}

// agentSetupSummary names the resulting state in one line.
func agentSetupSummary(selector string, ambiguous, skillInstalled bool, records []agentSetupRecord) string {
	switch {
	case !skillInstalled:
		return "Baseline skill installation failed"
	case selector == "invalid":
		return "Unknown " + agentSetupEnv + " value; installed baseline skill only"
	case ambiguous:
		return "Multiple coding agents detected; installed baseline skill only"
	case len(records) == 0:
		return "Installed baseline skill; no coding agents connected"
	}
	names := make([]string, 0, len(records))
	connected := 0
	for _, r := range records {
		names = append(names, r.name)
		if r.pluginInstalled {
			connected++
		}
	}
	if connected == len(records) {
		return "Installed baseline skill; connected " + joinNames(names)
	}
	return "Installed baseline skill; attempted " + joinNames(names)
}

// orderedStringSet accumulates strings with stable first-seen dedup.
type orderedStringSet struct {
	seen  map[string]bool
	items []string
}

func newOrderedStringSet() *orderedStringSet {
	return &orderedStringSet{seen: map[string]bool{}, items: []string{}}
}

func (s *orderedStringSet) add(v string) {
	if !s.seen[v] {
		s.seen[v] = true
		s.items = append(s.items, v)
	}
}

func (s *orderedStringSet) slice() []string { return s.items }

// orEmptyStrings replaces a nil slice with a non-nil empty one so JSON
// renders `[]` rather than `null`.
func orEmptyStrings(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}
