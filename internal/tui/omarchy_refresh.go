package tui

import (
	"context"
	"io"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The Omarchy bar plugin (37signals.hey) polls the Imbox every few minutes, so
// on its own it would show a thread as unread for minutes after the TUI marked
// it seen or archived it. After every posting mutation the TUI asks the plugin
// to refresh over the shell's IPC; the plugin guards against overlapping
// refreshes, so a burst of mutations costs a few no-op calls, not a fetch storm.

const (
	omarchyBarPluginID = "37signals.hey"
	// omarchy-shell itself gives up after two seconds; this only bounds a hung
	// binary.
	omarchyRefreshTimeout = 3 * time.Second
)

// omarchyShellRunner runs a shell command and discards whatever it says:
// whether the refresh reached the plugin is nothing the TUI can act on. A
// package-level seam so tests can record the invocation.
var omarchyShellRunner = func(name string, args ...string) {
	if _, err := exec.LookPath(name); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), omarchyRefreshTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: fixed omarchy command
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	_ = cmd.Run()
}

// omarchyRefresh returns a command that nudges the bar plugin to refresh, or
// nil when this machine does not run Omarchy. -q keeps omarchy-shell quiet and
// successful even when the shell or the plugin is not running.
func omarchyRefresh() tea.Cmd {
	if omarchyThemeDir(userHomeDir()) == "" {
		return nil
	}
	return func() tea.Msg {
		omarchyShellRunner("omarchy-shell", "-q", omarchyBarPluginID, "refresh")
		return nil
	}
}
