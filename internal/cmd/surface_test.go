package cmd

import (
	"flag"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var updateSurface = flag.Bool("update-surface", false, "Update the .surface baseline file")

func TestSurfaceSnapshot(t *testing.T) {
	root := newRootCmd()

	current := collectSurface(root, "")
	sort.Strings(current)
	snapshot := strings.Join(current, "\n") + "\n"

	baselineFile := "../../.surface"
	baseline, err := os.ReadFile(baselineFile)
	if os.IsNotExist(err) {
		if *updateSurface {
			if err := os.WriteFile(baselineFile, []byte(snapshot), 0644); err != nil {
				t.Fatalf("writing baseline: %v", err)
			}
			t.Log("Surface baseline created at .surface")
			return
		}
		t.Fatal("No .surface baseline found. Run with -update-surface to create it.")
	}
	if err != nil {
		t.Fatalf("reading baseline: %v", err)
	}

	baselineLines := strings.Split(strings.TrimSpace(string(baseline)), "\n")
	sort.Strings(baselineLines)

	currentSet := map[string]bool{}
	for _, line := range current {
		currentSet[line] = true
	}
	baselineSet := map[string]bool{}
	for _, line := range baselineLines {
		baselineSet[line] = true
	}

	var removed []string
	for _, line := range baselineLines {
		if !currentSet[line] {
			removed = append(removed, line)
		}
	}

	var added []string
	for _, line := range current {
		if !baselineSet[line] {
			added = append(added, line)
		}
	}

	if len(removed) > 0 {
		t.Errorf("Surface compatibility break — removed commands/flags:\n%s",
			strings.Join(removed, "\n"))
	}

	if len(added) > 0 {
		if *updateSurface {
			if err := os.WriteFile(baselineFile, []byte(snapshot), 0644); err != nil {
				t.Fatalf("writing updated baseline: %v", err)
			}
			t.Logf("Surface baseline updated with additions:\n%s", strings.Join(added, "\n"))
		} else {
			t.Errorf("Surface has new commands/flags:\n%s\n\nRun with -update-surface to accept.",
				strings.Join(added, "\n"))
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
