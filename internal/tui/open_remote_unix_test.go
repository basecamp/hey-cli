//go:build unix

package tui

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestOpenRemoteRoundTrip(t *testing.T) {
	setPrivateRuntimeDir(t)
	received := make(chan OpenRequest, 2)
	listener, err := startOpenListener("omarchy", func(msg tea.Msg) {
		request, ok := msg.(OpenRequest)
		if ok {
			received <- request
		}
	})
	if err != nil {
		t.Fatalf("start open listener: %v", err)
	}
	if listener == nil {
		t.Fatal("open listener did not start")
	}
	t.Cleanup(func() { closeOpenListener(listener) })

	for _, want := range []OpenRequest{
		{TopicID: 5511, AccountID: 42, Title: "Lunch on Thursday?"},
		{Screener: true, AccountID: 42},
	} {
		if err := OpenInRunningTUI("omarchy", want); err != nil {
			t.Fatalf("open destination: %v", err)
		}
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("request = %#v, want %#v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("open request was not delivered")
		}
	}
}

func TestTopicRemoteInstancesUseDistinctValidatedSockets(t *testing.T) {
	setPrivateRuntimeDir(t)
	defaultPath := mustTUISocketPath(t, "")
	omarchyPath := mustTUISocketPath(t, "omarchy")
	if defaultPath == omarchyPath {
		t.Fatal("named TUI shared the default socket")
	}
	for _, instance := range []string{"omarchy/../../other", strings.Repeat("a", 33), "."} {
		if _, err := tuiSocketPath(instance); err == nil {
			t.Errorf("instance %q was accepted", instance)
		}
	}
}

func TestTopicRemoteFallbackUsesPrivatePerUserDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", t.TempDir())
	path := mustTUISocketPath(t, "omarchy")
	parent := filepath.Dir(path)
	if parent == os.TempDir() {
		t.Fatalf("socket was placed directly in shared temporary directory: %s", path)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat private runtime directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("private runtime directory mode = %04o, want 0700", got)
	}
}

func TestTopicRemoteRejectsInsecureRuntimeDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o755); err != nil {
		t.Fatalf("make runtime directory insecure: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	if _, err := tuiSocketPath("omarchy"); err == nil {
		t.Fatal("insecure XDG_RUNTIME_DIR was accepted")
	}
}

func TestTopicRemoteListenerOwnershipIsExclusive(t *testing.T) {
	setPrivateRuntimeDir(t)
	first, err := startOpenListener("omarchy", func(tea.Msg) {})
	if err != nil || first == nil {
		t.Fatalf("start first listener: listener=%v err=%v", first != nil, err)
	}
	defer closeOpenListener(first)

	second, err := startOpenListener("omarchy", func(tea.Msg) {})
	if err == nil || second != nil {
		closeOpenListener(second)
		t.Fatalf("second listener did not report the owned instance: listener=%v err=%v", second != nil, err)
	}
	if err := Run(nil, nil, "all", Watchers{}, Options{Instance: "omarchy"}); err == nil {
		t.Fatal("Run silently started a TUI without owning its requested instance")
	}

	closeOpenListener(first)
	third, err := startOpenListener("omarchy", func(tea.Msg) {})
	if err != nil || third == nil {
		t.Fatalf("start listener after owner closed: listener=%v err=%v", third != nil, err)
	}
	closeOpenListener(third)
}

func TestTopicRemoteListenerDoesNotRemoveAReplacementPath(t *testing.T) {
	setPrivateRuntimeDir(t)
	listener, err := startOpenListener("omarchy", func(tea.Msg) {})
	if err != nil || listener == nil {
		t.Fatalf("start listener: listener=%v err=%v", listener != nil, err)
	}
	path := mustTUISocketPath(t, "omarchy")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove listener path: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement path: %v", err)
	}
	closeOpenListener(listener)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("listener cleanup removed replacement path: %v", err)
	}
}

func TestOpenRemoteRequiresOneDestination(t *testing.T) {
	for _, request := range []OpenRequest{
		{},
		{TopicID: 5511, Screener: true},
		{TopicID: -1, Screener: true},
		{TopicID: 5511, AccountID: -1},
	} {
		if err := OpenInRunningTUI("omarchy", request); err == nil {
			t.Errorf("request %#v was accepted", request)
		}
	}
}

func TestTopicRemoteReportsNoRunningTUI(t *testing.T) {
	setPrivateRuntimeDir(t)
	if err := OpenInRunningTUI("omarchy", OpenRequest{TopicID: 5511}); !errors.Is(err, ErrNoRunningTUI) {
		t.Fatalf("error = %v, want ErrNoRunningTUI", err)
	}
}

func TestRunReportsTopicListenerConfigurationErrors(t *testing.T) {
	err := Run(nil, nil, "all", Watchers{}, Options{Instance: "invalid/name"})
	if err == nil {
		t.Fatal("Run accepted an invalid listener instance")
	}
}

// setPrivateRuntimeDir points the TUI at a private runtime directory the test owns.
// The directory is made under the system temp root with a short name rather than
// with t.TempDir(): a unix socket path is capped at 104 bytes on macOS, and
// t.TempDir() embeds the test name inside an already long per-user temp path,
// which put the socket over the cap and failed bind with "invalid argument".
func setPrivateRuntimeDir(t *testing.T) {
	t.Helper()
	runtimeDir, err := os.MkdirTemp("", "hey")
	if err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatalf("protect runtime directory: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
}

func mustTUISocketPath(t *testing.T, instance string) string {
	t.Helper()
	path, err := tuiSocketPath(instance)
	if err != nil {
		t.Fatalf("TUI socket path: %v", err)
	}
	return path
}

var _ net.Listener = (*ownedTUIListener)(nil)
