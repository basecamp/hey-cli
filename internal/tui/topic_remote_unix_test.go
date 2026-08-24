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

func TestTopicRemoteRoundTrip(t *testing.T) {
	setPrivateRuntimeDir(t)
	received := make(chan TopicRequest, 1)
	listener, err := startTopicListener("omarchy", func(msg tea.Msg) {
		request, ok := msg.(TopicRequest)
		if ok {
			received <- request
		}
	})
	if err != nil {
		t.Fatalf("start topic listener: %v", err)
	}
	if listener == nil {
		t.Fatal("topic listener did not start")
	}
	t.Cleanup(func() { closeTopicListener(listener) })

	want := TopicRequest{TopicID: 5511, AccountID: 42, Title: "Lunch on Thursday?"}
	if err := OpenTopicInRunningTUI("omarchy", want); err != nil {
		t.Fatalf("open topic: %v", err)
	}
	select {
	case got := <-received:
		if got != want {
			t.Fatalf("request = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("topic request was not delivered")
	}
}

func TestTopicRemoteInstancesUseDistinctValidatedSockets(t *testing.T) {
	setPrivateRuntimeDir(t)
	defaultPath := mustTopicSocketPath(t, "")
	omarchyPath := mustTopicSocketPath(t, "omarchy")
	if defaultPath == omarchyPath {
		t.Fatal("named TUI shared the default socket")
	}
	for _, instance := range []string{"omarchy/../../other", strings.Repeat("a", 33), "."} {
		if _, err := topicSocketPath(instance); err == nil {
			t.Errorf("instance %q was accepted", instance)
		}
	}
}

func TestTopicRemoteFallbackUsesPrivatePerUserDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", t.TempDir())
	path := mustTopicSocketPath(t, "omarchy")
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
	if _, err := topicSocketPath("omarchy"); err == nil {
		t.Fatal("insecure XDG_RUNTIME_DIR was accepted")
	}
}

func TestTopicRemoteListenerOwnershipIsExclusive(t *testing.T) {
	setPrivateRuntimeDir(t)
	first, err := startTopicListener("omarchy", func(tea.Msg) {})
	if err != nil || first == nil {
		t.Fatalf("start first listener: listener=%v err=%v", first != nil, err)
	}
	defer closeTopicListener(first)

	second, err := startTopicListener("omarchy", func(tea.Msg) {})
	if err == nil || second != nil {
		closeTopicListener(second)
		t.Fatalf("second listener did not report the owned instance: listener=%v err=%v", second != nil, err)
	}
	if err := Run(nil, nil, "all", Watchers{}, Options{Instance: "omarchy"}); err == nil {
		t.Fatal("Run silently started a TUI without owning its requested instance")
	}

	closeTopicListener(first)
	third, err := startTopicListener("omarchy", func(tea.Msg) {})
	if err != nil || third == nil {
		t.Fatalf("start listener after owner closed: listener=%v err=%v", third != nil, err)
	}
	closeTopicListener(third)
}

func TestTopicRemoteListenerDoesNotRemoveAReplacementPath(t *testing.T) {
	setPrivateRuntimeDir(t)
	listener, err := startTopicListener("omarchy", func(tea.Msg) {})
	if err != nil || listener == nil {
		t.Fatalf("start listener: listener=%v err=%v", listener != nil, err)
	}
	path := mustTopicSocketPath(t, "omarchy")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove listener path: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement path: %v", err)
	}
	closeTopicListener(listener)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("listener cleanup removed replacement path: %v", err)
	}
}

func TestTopicRemoteReportsNoRunningTUI(t *testing.T) {
	setPrivateRuntimeDir(t)
	if err := OpenTopicInRunningTUI("omarchy", TopicRequest{TopicID: 5511}); !errors.Is(err, ErrNoRunningTUI) {
		t.Fatalf("error = %v, want ErrNoRunningTUI", err)
	}
}

func TestRunReportsTopicListenerConfigurationErrors(t *testing.T) {
	err := Run(nil, nil, "all", Watchers{}, Options{Instance: "invalid/name"})
	if err == nil {
		t.Fatal("Run accepted an invalid listener instance")
	}
}

func setPrivateRuntimeDir(t *testing.T) {
	t.Helper()
	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatalf("protect runtime directory: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
}

func mustTopicSocketPath(t *testing.T, instance string) string {
	t.Helper()
	path, err := topicSocketPath(instance)
	if err != nil {
		t.Fatalf("topic socket path: %v", err)
	}
	return path
}

var _ net.Listener = (*ownedTopicListener)(nil)
