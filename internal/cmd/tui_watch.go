package cmd

import (
	"context"
	"sync"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-cli/internal/cable"
	"github.com/basecamp/hey-cli/internal/tui"
)

const turboStreamsChannel = "Turbo::StreamsChannel"

// mailChangeBacklog is how many notifications wait to be read before the reconnect
// catch-up is dropped instead of blocking the cable client's own goroutine. A full
// backlog means a re-read is already coming.
const mailChangeBacklog = 16

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
func watchMailChanges(ctx context.Context) (<-chan int64, error) {
	client, err := tuiCableClient(ctx)
	if err != nil {
		return nil, err
	}

	// A reconnect stands for every box: the changes broadcast while the connection was
	// down were missed, and the box on screen has to be read again to find them.
	changes := make(chan int64, mailChangeBacklog)
	subscription, err := client.Subscribe(ctx, actioncable.Identifier{Channel: changesChannel},
		actioncable.OnConnected(func(reconnected bool) {
			if reconnected {
				select {
				case changes <- tui.AnyBoxChanged:
				default:
				}
			}
		}))
	if err != nil {
		return nil, err
	}

	go relayMailChanges(ctx, subscription, changes)

	return changes, nil
}

func relayMailChanges(ctx context.Context, subscription *actioncable.Subscription, changes chan<- int64) {
	defer close(changes)

	for {
		select {
		case <-ctx.Done():
			return
		case message, open := <-subscription.Messages():
			if !open {
				return
			}
			var notification struct {
				BoxID int64 `json:"box_id"`
			}
			if err := message.Unmarshal(&notification); err != nil {
				continue
			}
			select {
			case changes <- notification.BoxID:
			case <-ctx.Done():
				return
			}
		}
	}
}

// watchScreenerChanges opens the stream that says The Screener changed. HEY re-renders the
// Screener's own button over a Turbo stream whenever a clearance is created or decided,
// and serves the signed name of that stream with the pending count. What it broadcasts is
// markup for the web app, so nothing is read out of it: the arrival is the whole message,
// and the TUI reads the count again behind it.
func watchScreenerChanges(ctx context.Context, signedStreamName string) (<-chan struct{}, error) {
	client, err := tuiCableClient(ctx)
	if err != nil {
		return nil, err
	}

	changes := make(chan struct{}, 1)
	subscription, err := client.Subscribe(ctx, actioncable.Identifier{
		Channel: turboStreamsChannel,
		Params:  actioncable.Params{"signed_stream_name": signedStreamName},
	}, actioncable.OnConnected(func(reconnected bool) {
		if reconnected {
			ringScreener(changes)
		}
	}))
	if err != nil {
		return nil, err
	}

	go relayScreenerChanges(ctx, subscription, changes)

	return changes, nil
}

func relayScreenerChanges(ctx context.Context, subscription *actioncable.Subscription, changes chan struct{}) {
	defer close(changes)

	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-subscription.Messages():
			if !open {
				return
			}
			ringScreener(changes)
		}
	}
}

// ringScreener drops the ring when one is already waiting: they all say the same thing,
// and it must not block the cable client's own goroutine.
func ringScreener(changes chan struct{}) {
	select {
	case changes <- struct{}{}:
	default:
	}
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

	client, err := cable.Dial(ctx, cfg.BaseURL, authMgr)
	if err != nil {
		return nil, watchDialError(err)
	}
	tuiCable.client = client

	go func() {
		<-ctx.Done()
		tuiCable.Lock()
		defer tuiCable.Unlock()
		_ = client.Close()
		tuiCable.client = nil
	}()

	return client, nil
}
