package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/tui"
)

func TestRelayMailChangesNamesTheChangedBoxAndConnectionState(t *testing.T) {
	messages := make(chan actioncable.Message, 1)
	connection := newMailConnectionNotifier()
	events := make(chan tui.MailWatchEvent, mailChangeBacklog)

	go relayMailChanges(t.Context(), messages, connection, events)

	messages <- actioncable.Message(`{"change":"upsert","box_id":24088}`)
	if got := <-events; got.BoxID != 24088 || got.Connection != tui.MailConnectionUnchanged {
		t.Errorf("event = %+v, want the box the notification named", got)
	}

	connection.note(tui.MailConnectionDisconnected, true)
	if got := <-events; got.Connection != tui.MailConnectionDisconnected || !got.WillReconnect {
		t.Errorf("event = %+v, want the temporary disconnect", got)
	}
	connection.note(tui.MailConnectionReconnected, false)
	if got := <-events; got.Connection != tui.MailConnectionReconnected {
		t.Errorf("event = %+v, want the reconnect", got)
	}

	// A notification that can't be read leaves the stream alone.
	messages <- actioncable.Message(`not json`)
	messages <- actioncable.Message(`{"box_id":31145}`)
	if got := <-events; got.BoxID != 31145 {
		t.Errorf("event = %+v, want the stream to carry on past what it can't read", got)
	}
}

func TestMailConnectionNotifierKeepsTheNewestRapidTransition(t *testing.T) {
	connection := newMailConnectionNotifier()
	connection.note(tui.MailConnectionDisconnected, true)
	connection.note(tui.MailConnectionReconnected, false)

	event, version, changed := connection.after(0)
	if !changed || version != 2 || event.Connection != tui.MailConnectionReconnected {
		t.Errorf("event = %+v, version = %d, changed = %v; want the latest reconnect", event, version, changed)
	}
	if _, _, changed := connection.after(version); changed {
		t.Error("the same transition should only be read once")
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
	connection := newMailConnectionNotifier()
	reconnects := make(chan struct{}, reconnectBacklog)
	mail := make(chan tui.MailWatchEvent, mailChangeBacklog)
	screener := make(chan struct{}, 1)

	go relayMailChanges(relaying, mailMessages, connection, mail)
	go relayScreenerChanges(relaying, screenerMessages, reconnects, screener)

	stop()
	if _, open := <-mail; open {
		t.Error("the mail stream should be closed once its relay is done")
	}
	if _, open := <-screener; open {
		t.Error("the Screener stream should be closed once its relay is done")
	}

	for range reconnectBacklog + 2 {
		connection.note(tui.MailConnectionReconnected, false)
		ring(reconnects, struct{}{})
	}
}

func TestMailConnectionStateTakesPriorityOverAFullBoxBacklog(t *testing.T) {
	events := make(chan tui.MailWatchEvent, 2)
	ringMailWatchEvent(events, tui.MailWatchEvent{BoxID: 1})
	ringMailWatchEvent(events, tui.MailWatchEvent{BoxID: 2})
	ringMailWatchEvent(events, tui.MailWatchEvent{BoxID: 3})
	if len(events) != 1 {
		t.Fatalf("events = %d, want a full box backlog coalesced", len(events))
	}
	if got := <-events; got.BoxID != tui.AnyBoxChanged || got.Connection != tui.MailConnectionUnchanged {
		t.Errorf("event = %+v, want a catch-all so the box on screen cannot stay stale", got)
	}

	ringMailWatchEvent(events, tui.MailWatchEvent{BoxID: 1})
	ringMailWatchEvent(events, tui.MailWatchEvent{BoxID: 2})
	ringMailWatchEvent(events, tui.MailWatchEvent{Connection: tui.MailConnectionDisconnected, WillReconnect: true})
	if len(events) != 1 {
		t.Fatalf("events = %d, want stale box doorbells coalesced behind connection state", len(events))
	}
	if got := <-events; got.Connection != tui.MailConnectionDisconnected || !got.WillReconnect {
		t.Errorf("event = %+v, want the disconnect", got)
	}
}

type scriptedCableTransport struct{ conn *scriptedCableConn }

func (t scriptedCableTransport) Dial(context.Context, string, actioncable.DialOptions) (actioncable.Conn, error) {
	return t.conn, nil
}

type scriptedCableConn struct {
	reads       chan []byte
	done        chan struct{}
	once        sync.Once
	subprotocol string
}

func newScriptedCableConn() *scriptedCableConn {
	return &scriptedCableConn{
		reads:       make(chan []byte, 2),
		done:        make(chan struct{}),
		subprotocol: actioncable.SubprotocolV1JSON,
	}
}

func (c *scriptedCableConn) Subprotocol() string { return c.subprotocol }

func (c *scriptedCableConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case payload := <-c.reads:
		return payload, nil
	case <-c.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *scriptedCableConn) Write(context.Context, []byte) error { return nil }

func (c *scriptedCableConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func TestSubscribeTuiCableRecognizesAnUnenumeratedTerminalFailure(t *testing.T) {
	conn := newScriptedCableConn()
	conn.subprotocol = "actioncable-v9-telepathy"
	client := actioncable.New("ws://cable.example.test/cable", actioncable.WithTransport(scriptedCableTransport{conn: conn}))
	if err := client.Connect(t.Context()); !errors.Is(err, actioncable.ErrUnsupportedSubprotocol) {
		t.Fatalf("connect error = %v, want the unsupported protocol to stop the client", err)
	}
	tuiCable.client = client
	t.Cleanup(func() { tuiCable.client = nil })

	_, stopped, err := subscribeTuiCable(t.Context(), client, actioncable.Identifier{Channel: changesChannel})
	if !stopped {
		t.Fatal("a client stopped by an unenumerated terminal failure should be replaced")
	}
	var known *apierr.Error
	if !errors.As(err, &known) || known.Code != apierr.CodeNetwork {
		t.Errorf("error = %T %v, want a retryable network error", err, err)
	}
	if tuiCable.client != nil {
		t.Error("a stopped client should not remain cached")
	}
}

func TestServerStoppedActionCableClientIsReplaceable(t *testing.T) {
	conn := newScriptedCableConn()
	conn.reads <- []byte(`{"type":"welcome"}`)
	client := actioncable.New("ws://cable.example.test/cable", actioncable.WithTransport(scriptedCableTransport{conn: conn}))
	if err := client.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()
	tuiCable.client = client
	t.Cleanup(func() { tuiCable.client = nil })

	conn.reads <- []byte(`{"type":"disconnect","reason":"unauthorized","reconnect":false}`)
	subscribing, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	_, stopped, err := subscribeTuiCable(subscribing, client, actioncable.Identifier{Channel: changesChannel})
	if !stopped {
		t.Fatalf("subscribe error = %T %v, want the terminal server disconnect to replace the client", err, err)
	}
	var known *apierr.Error
	if !errors.As(err, &known) || known.Code != apierr.CodeAuth {
		t.Errorf("error = %T %v, want an authentication error", err, err)
	}
	if tuiCable.client != nil {
		t.Error("a client stopped by the server should not remain cached")
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

	_, err := tuiSubscribe(dialing, dialing, actioncable.Identifier{Channel: changesChannel})
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
