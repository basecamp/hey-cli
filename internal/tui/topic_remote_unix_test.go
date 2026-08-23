//go:build !windows

package tui

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestTopicRemoteRoundTrip(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
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
	t.Cleanup(func() { closeTopicListener("omarchy", listener) })

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

func TestTopicRemoteInstancesUseSeparateSafeSockets(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	defaultPath := topicSocketPath("")
	omarchyPath := topicSocketPath("omarchy")
	if defaultPath == omarchyPath {
		t.Fatal("named TUI shared the default socket")
	}
	if got := topicSocketPath("omarchy/../../other"); got != topicSocketPath("omarchyother") {
		t.Fatalf("unsafe instance path = %q", got)
	}
}

func TestTopicRemoteReportsNoRunningTUI(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if err := OpenTopicInRunningTUI("omarchy", TopicRequest{TopicID: 5511}); !errors.Is(err, ErrNoRunningTUI) {
		t.Fatalf("error = %v, want ErrNoRunningTUI", err)
	}
}
