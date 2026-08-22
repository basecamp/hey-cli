package cmd

import (
	"context"
	"errors"
	"sync"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-cli/internal/cable"
	"github.com/basecamp/hey-cli/internal/tui"
)

const turboStreamsChannel = "Turbo::StreamsChannel"

// mailChangeBacklog is how many notifications wait to be read before one is dropped
// instead of blocking the relay. A full backlog means a re-read is already coming.
const mailChangeBacklog = 16

// reconnectBacklog is one, because a Screener reconnect always says the same thing. It
// is a channel of its own so that the relay goroutine is the only writer to the channel
// it closes: the cable client drains callbacks queued before it was told to stop, and a
// send on a closed channel panics whatever the select around it says — off a goroutine
// Bubble Tea knows nothing about, which takes the terminal down in raw mode. Nothing
// closes this one, so a callback arriving after the relay is gone rings into the buffer
// and is collected with it.
const reconnectBacklog = 1

type mailConnectionNotifier struct {
	sync.Mutex
	event   tui.MailWatchEvent
	version uint64
	wake    chan struct{}
}

func newMailConnectionNotifier() *mailConnectionNotifier {
	return &mailConnectionNotifier{wake: make(chan struct{}, 1)}
}

// note keeps the newest connection state and wakes the relay without blocking Action
// Cable's callback dispatcher. If a disconnect and reconnect arrive before the relay can
// draw either, the reconnect is the state that matters and still asks for a catch-up.
func (n *mailConnectionNotifier) note(connection tui.MailConnection, willReconnect bool) {
	n.Lock()
	n.event = tui.MailWatchEvent{Connection: connection, WillReconnect: willReconnect}
	n.version++
	n.Unlock()
	ring(n.wake, struct{}{})
}

func (n *mailConnectionNotifier) after(version uint64) (tui.MailWatchEvent, uint64, bool) {
	n.Lock()
	defer n.Unlock()
	return n.event, n.version, n.version != version
}

const (
	// unsubscribeTimeout bounds the goodbye sent for a watch that is over. Nothing waits
	// on it, and a connection that has gone away is reason to stop trying rather than hang.
	unsubscribeTimeout = 5 * time.Second

	// tuiCableDialTimeout turns an unreachable cable server into app state instead of
	// leaving the startup command inside Action Cable's retry loop forever. The model
	// owns the retries after this first bounded attempt.
	tuiCableDialTimeout = 5 * time.Second
)

// tuiWatchers are the streams `hey tui` follows to stay live.
func tuiWatchers() tui.Watchers {
	return tui.Watchers{Mail: watchMailChanges, Screener: watchScreenerChanges}
}

// watchMailChanges opens the stream of changed boxes the TUI follows, over the same
// cable subscription `hey watch` listens on. The notification is a doorbell — it names
// the box and nothing else — and that is all the TUI wants: it re-reads the box it is
// showing rather than following individual postings, so there is no cursor to keep.
//
// The stream closes when ctx is done, or when the connection is gone for good, which is
// how the TUI hears that its list has stopped being live.
func watchMailChanges(ctx context.Context) (<-chan tui.MailWatchEvent, error) {
	connection := newMailConnectionNotifier()
	subscription, err := tuiSubscribe(ctx, ctx, actioncable.Identifier{Channel: changesChannel},
		actioncable.OnConnected(func(reconnected bool) {
			if reconnected {
				connection.note(tui.MailConnectionReconnected, false)
			}
		}),
		actioncable.OnDisconnected(func(willReconnect bool) {
			connection.note(tui.MailConnectionDisconnected, willReconnect)
		}))
	if err != nil {
		return nil, err
	}

	events := make(chan tui.MailWatchEvent, mailChangeBacklog)
	go func() {
		defer unsubscribe(ctx, subscription)
		relayMailChanges(ctx, subscription.Messages(), connection, events)
	}()

	return events, nil
}

func relayMailChanges(ctx context.Context, messages <-chan actioncable.Message, connection *mailConnectionNotifier, events chan tui.MailWatchEvent) {
	defer close(events)
	var connectionVersion uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-connection.wake:
			event, version, changed := connection.after(connectionVersion)
			if !changed {
				continue
			}
			connectionVersion = version
			ringMailWatchEvent(events, event)
		case message, open := <-messages:
			if !open {
				return
			}
			var notification struct {
				BoxID int64 `json:"box_id"`
			}
			if err := message.Unmarshal(&notification); err != nil {
				continue
			}
			ringMailWatchEvent(events, tui.MailWatchEvent{BoxID: notification.BoxID})
		}
	}
}

// watchScreenerChanges opens the stream that says The Screener changed. HEY re-renders the
// Screener's own button over a Turbo stream whenever a clearance is created or decided,
// and serves the signed name of that stream with the pending count. What it broadcasts is
// markup for the web app, so nothing is read out of it: the arrival is the whole message,
// and the TUI reads the count again behind it.
func watchScreenerChanges(ctx, connectionCtx context.Context, signedStreamName string) (<-chan struct{}, error) {
	reconnects := make(chan struct{}, reconnectBacklog)
	subscription, err := tuiSubscribe(ctx, connectionCtx, actioncable.Identifier{
		Channel: turboStreamsChannel,
		Params:  actioncable.Params{"signed_stream_name": signedStreamName},
	}, actioncable.OnConnected(func(reconnected bool) {
		if reconnected {
			ring(reconnects, struct{}{})
		}
	}))
	if err != nil {
		return nil, err
	}

	changes := make(chan struct{}, 1)
	go func() {
		defer unsubscribe(ctx, subscription)
		relayScreenerChanges(ctx, subscription.Messages(), reconnects, changes)
	}()

	return changes, nil
}

func relayScreenerChanges(ctx context.Context, messages <-chan actioncable.Message, reconnects <-chan struct{}, changes chan<- struct{}) {
	defer close(changes)

	for {
		select {
		case <-ctx.Done():
			return
		case <-reconnects:
			ring(changes, struct{}{})
		case _, open := <-messages:
			if !open {
				return
			}
			ring(changes, struct{}{})
		}
	}
}

// unsubscribe drops a subscription whose watch is over. Cancelling the watch's context
// ends the relay, but the subscription itself belongs to the shared client: left
// registered it holds its buffered channel and its callback dispatcher for as long as the
// TUI runs, and goes on being handed messages nobody reads. The watch's context is what
// ended, so the goodbye is sent under one that outlives it.
func unsubscribe(ctx context.Context, subscription *actioncable.Subscription) {
	goodbye, giveUp := context.WithTimeout(context.WithoutCancel(ctx), unsubscribeTimeout)
	defer giveUp()
	_ = subscription.Unsubscribe(goodbye)
}

// ringMailWatchEvent keeps connection state ahead of stale box doorbells. Box events can
// be coalesced because one re-read catches up every posting in that box; a connection
// transition empties that backlog so the global status changes promptly, and reconnecting
// catches the visible box up without relying on an older doorbell.
func ringMailWatchEvent(events chan tui.MailWatchEvent, event tui.MailWatchEvent) {
	if event.Connection == tui.MailConnectionUnchanged {
		select {
		case events <- event:
		default:
		}
		return
	}

	for {
		select {
		case <-events:
			continue
		default:
		}
		select {
		case events <- event:
			return
		default:
			// The reader raced us between draining and sending. Try the now-current
			// queue again rather than blocking Action Cable's relay.
		}
	}
}

// ring drops the notification when one is already waiting: they all say the same thing,
// and a reader that has fallen behind must not hold up the goroutine doing the ringing.
func ring[T any](notifications chan<- T, notification T) {
	select {
	case notifications <- notification:
	default:
	}
}

// tuiSubscribe subscribes over the connection the TUI's watches share, dialling a new one
// when the one on hand has stopped itself. A client the server hung up on for good answers
// every Subscribe with ErrClosed and never dials again on its own, so a Screener stream
// reopened after that would find a dead connection and stay dead. Each dial carries
// current credentials, which is what makes redialling worth doing.
func tuiSubscribe(ctx, connectionCtx context.Context, identifier actioncable.Identifier, options ...actioncable.SubscriptionOption) (*actioncable.Subscription, error) {
	client, err := tuiCableClient(connectionCtx)
	if err != nil {
		return nil, err
	}

	subscription, err := client.Subscribe(ctx, identifier, options...)
	if stoppedTuiCableError(err) {
		forgetTuiCable(client)
		if client, err = tuiCableClient(connectionCtx); err != nil {
			return nil, err
		}
		subscription, err = client.Subscribe(ctx, identifier, options...)
	}
	return subscription, err
}

func stoppedTuiCableError(err error) bool {
	if errors.Is(err, actioncable.ErrClosed) {
		return true
	}
	var disconnected *actioncable.DisconnectError
	return errors.As(err, &disconnected) && !disconnected.Reconnect
}

// tuiCable is the one connection the TUI's watches share — two subscriptions over one
// websocket, authorized once. It is opened by whichever watch needs it first and closed
// when that watch's context is done, which is the TUI's own lifetime.
var tuiCable struct {
	sync.Mutex
	client *actioncable.Client
}

func tuiCableClient(ctx context.Context) (*actioncable.Client, error) {
	tuiCable.Lock()
	defer tuiCable.Unlock()

	if tuiCable.client != nil {
		return tuiCable.client, nil
	}

	dialing, stopDialing := context.WithTimeout(ctx, tuiCableDialTimeout)
	defer stopDialing()
	client, err := cable.Dial(dialing, cfg.BaseURL, authMgr)
	if err != nil {
		return nil, watchDialError(err)
	}
	tuiCable.client = client

	go func() {
		<-ctx.Done()
		_ = client.Close()
		forgetTuiCable(client)
	}()

	return client, nil
}

// forgetTuiCable drops a client that is no good any more, so the next watch dials rather
// than being handed it again. It leaves a newer client alone: whoever replaced this one
// owns the slot now.
func forgetTuiCable(client *actioncable.Client) {
	tuiCable.Lock()
	defer tuiCable.Unlock()

	if tuiCable.client == client {
		tuiCable.client = nil
	}
}
