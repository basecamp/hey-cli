package cmd

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestSurfaceSnapshot(t *testing.T) {
	root := newRootCmd()

	current := collectSurface(root, "")
	sort.Strings(current)
	snapshot := strings.Join(current, "\n") + "\n"

	baselineFile := "../../.surface"
	baseline, err := os.ReadFile(baselineFile)
	if os.IsNotExist(err) {
		// First run — write the baseline
		if err := os.WriteFile(baselineFile, []byte(snapshot), 0644); err != nil {
			t.Fatalf("writing baseline: %v", err)
		}
		t.Log("Surface baseline created at .surface")
		return
	}
	if err != nil {
		t.Fatalf("reading baseline: %v", err)
	}

	baselineLines := strings.Split(strings.TrimSpace(string(baseline)), "\n")
	sort.Strings(baselineLines)

	// Check for removals (commands in baseline but not in current)
	currentSet := map[string]bool{}
	for _, line := range current {
		currentSet[line] = true
	}

	var removed []string
	for _, line := range baselineLines {
		if !currentSet[line] {
			removed = append(removed, line)
		}
	}

	if len(removed) > 0 {
		t.Errorf("Surface compatibility break — removed commands/flags:\n%s\n\nUpdate .surface if this removal is intentional.",
			strings.Join(removed, "\n"))
	}

	// Update baseline with any additions
	if snapshot != string(baseline) {
		if err := os.WriteFile(baselineFile, []byte(snapshot), 0644); err != nil {
			t.Logf("warning: could not update baseline: %v", err)
		}
	}
}

func collectSurface(cmd *cobra.Command, prefix string) []string {
	var lines []string

	path := prefix + cmd.Name()
	lines = append(lines, path)

	cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		lines = append(lines, path+" --"+f.Name)
	})

	for _, sub := range cmd.Commands() {
		if sub.Hidden || !sub.IsAvailableCommand() {
			continue
		}
		lines = append(lines, collectSurface(sub, path+" ")...)
	}

	return lines
}
