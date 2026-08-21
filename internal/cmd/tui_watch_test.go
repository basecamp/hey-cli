package cmd

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/tui"
)

func TestRelayMailChangesNamesTheChangedBox(t *testing.T) {
	messages := make(chan actioncable.Message, 1)
	reconnects := make(chan struct{}, reconnectBacklog)
	changes := make(chan int64, mailChangeBacklog)

	go relayMailChanges(t.Context(), messages, reconnects, changes)

	messages <- actioncable.Message(`{"change":"upsert","box_id":24088}`)
	if got := <-changes; got != 24088 {
		t.Errorf("change = %d, want the box the notification named", got)
	}

	ring(reconnects, struct{}{})
	if got := <-changes; got != tui.AnyBoxChanged {
		t.Errorf("change = %d, want a reconnect to stand for every box", got)
	}

	// A notification that can't be read leaves the stream alone.
	messages <- actioncable.Message(`not json`)
	messages <- actioncable.Message(`{"box_id":31145}`)
	if got := <-changes; got != 31145 {
		t.Errorf("change = %d, want the stream to carry on past what it can't read", got)
	}
}

func TestRelayScreenerChangesRingsOnEveryBroadcast(t *testing.T) {
	messages := make(chan actioncable.Message, 1)
	reconnects := make(chan struct{}, reconnectBacklog)
	changes := make(chan struct{}, 1)

	go relayScreenerChanges(t.Context(), messages, reconnects, changes)

	messages <- actioncable.Message(`<turbo-stream action="replace" target="screener_button">`)
	<-changes

	ring(reconnects, struct{}{})
	<-changes
}

func TestARelayIsTheOnlyWriterToTheStreamItCloses(t *testing.T) {
	// The cable client runs the callbacks it queued before it was told to stop, so a
	// reconnect can be announced after the relay closed the channel the TUI reads.
	// Sending on a closed channel panics, off a goroutine Bubble Tea can't recover,
	// which leaves the terminal in raw mode — so only the relay may write to it.
	relaying, stop := context.WithCancel(t.Context())

	mailMessages := make(chan actioncable.Message)
	screenerMessages := make(chan actioncable.Message)
	reconnects := make(chan struct{}, reconnectBacklog)
	mail := make(chan int64, mailChangeBacklog)
	screener := make(chan struct{}, 1)

	go relayMailChanges(relaying, mailMessages, reconnects, mail)
	go relayScreenerChanges(relaying, screenerMessages, reconnects, screener)

	stop()
	if _, open := <-mail; open {
		t.Error("the mail stream should be closed once its relay is done")
	}
	if _, open := <-screener; open {
		t.Error("the Screener stream should be closed once its relay is done")
	}

	for range reconnectBacklog + 2 {
		ring(reconnects, struct{}{})
	}
}

func TestTuiSubscribeReplacesAClientThatStoppedItself(t *testing.T) {
	// A client the server hung up on for good answers every Subscribe with ErrClosed and
	// never dials again on its own, so handing it to a reopened watch means the TUI is
	// never live again. The dial that replaces it cannot succeed here — that one is
	// attempted at all is the whole point.
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HEY_CABLE_URL", "ws://127.0.0.1:1/cable")

	previousCfg, previousAuthMgr := cfg, authMgr
	cfg = &config.Config{BaseURL: "https://app.hey.example.com"}
	authMgr = auth.NewManager(cfg.BaseURL, http.DefaultClient, t.TempDir())

	stopped := actioncable.New("ws://127.0.0.1:1/cable")
	_ = stopped.Close()
	tuiCable.client = stopped

	t.Cleanup(func() {
		cfg, authMgr = previousCfg, previousAuthMgr
		tuiCable.client = nil
	})

	// A dial keeps retrying until it is welcomed or its context is done, and nothing is
	// listening on this address, so the wait is what bounds the attempt.
	dialing, giveUp := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer giveUp()

	_, err := tuiSubscribe(dialing, actioncable.Identifier{Channel: changesChannel})
	if err == nil {
		t.Fatal("a dial against a dead address should fail")
	}
	if errors.Is(err, actioncable.ErrClosed) {
		t.Errorf("err = %v, want the stopped client replaced rather than subscribed to again", err)
	}
	if tuiCable.client == stopped {
		t.Error("the stopped client should be dropped, so the next watch dials")
	}
}

func TestRingDropsWhatWouldBlock(t *testing.T) {
	notifications := make(chan struct{}, 1)

	ring(notifications, struct{}{})
	ring(notifications, struct{}{})

	if len(notifications) != 1 {
		t.Errorf("waiting notifications = %d, want the one that says everything the other did", len(notifications))
	}
	select {
	case <-notifications:
	case <-time.After(time.Second):
		t.Fatal("the first notification should be waiting")
	}
}
