package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeOmarchyHome points HOME at a directory with (or without) Omarchy's state
// dir and records what the refresh command would run.
func fakeOmarchyHome(t *testing.T, omarchy bool) *[]string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if omarchy {
		if err := os.MkdirAll(filepath.Join(home, ".local", "state", "omarchy"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var ran []string
	previous := omarchyShellRunner
	omarchyShellRunner = func(name string, args ...string) {
		ran = append(ran, strings.Join(append([]string{name}, args...), " "))
	}
	t.Cleanup(func() { omarchyShellRunner = previous })
	return &ran
}

func TestOmarchyRefreshNudgesTheBarPlugin(t *testing.T) {
	ran := fakeOmarchyHome(t, true)

	cmd := omarchyRefresh()
	if cmd == nil {
		t.Fatal("under Omarchy the refresh command must exist")
	}
	if msg := cmd(); msg != nil {
		t.Errorf("the refresh produces no message, got %T", msg)
	}
	if len(*ran) != 1 || (*ran)[0] != "omarchy-shell -q 37signals.hey refresh" {
		t.Errorf("ran %v", *ran)
	}
}

func TestOmarchyRefreshIsANoOpOutsideOmarchy(t *testing.T) {
	ran := fakeOmarchyHome(t, false)

	if cmd := omarchyRefresh(); cmd != nil {
		t.Error("without Omarchy's state dir there is no shell to nudge")
	}
	if len(*ran) != 0 {
		t.Errorf("nothing may run, ran %v", *ran)
	}
}

func TestMailViewPostingActionRefreshesTheBarPlugin(t *testing.T) {
	ran := fakeOmarchyHome(t, true)
	v := mailWithPostings()

	cmd, consumed := v.Update(postingActionDoneMsg{action: "Thread marked as seen", boxID: 1, postingID: 100, effect: postingActionSeen})
	if !consumed || cmd == nil {
		t.Fatal("a completed action should batch the bar refresh")
	}
	runBatch(cmd)
	if len(*ran) != 1 {
		t.Errorf("the bar plugin should be refreshed once after a posting action, ran %v", *ran)
	}

	// A failed action changed nothing, so there is nothing to refresh.
	cmd, _ = v.Update(postingActionDoneMsg{boxID: 1, postingID: 100, err: os.ErrClosed})
	runBatch(cmd)
	if len(*ran) != 1 {
		t.Errorf("a failed action must not refresh, ran %v", *ran)
	}
}

func TestScreenerDecisionRefreshesTheBarPlugin(t *testing.T) {
	ran := fakeOmarchyHome(t, true)
	v := newScreenerView(testVC())

	cmd, _ := v.Update(screenerDecisionDoneMsg{clearanceID: 1, name: "Maria Delgado", status: "approved"})
	runBatch(cmd)
	cmd, _ = v.Update(screenerClearedMsg{})
	runBatch(cmd)
	if len(*ran) != 2 {
		t.Errorf("approving a sender and clearing The Screener both change the Imbox, ran %v", *ran)
	}
}

// runBatch executes a command and any batch it expands to, synchronously.
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, inner := range msg {
			runBatch(inner)
		}
	}
}
