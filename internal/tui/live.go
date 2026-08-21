package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Watchers are the streams the TUI follows to stay live. Both are optional: without them
// a list is the snapshot it was read as.
type Watchers struct {
	Mail     MailWatcher
	Screener ScreenerWatcher
}

// MailWatcher opens the stream of boxes that changed, so the TUI is told when to re-read
// a box instead of polling for it. The stream closes when ctx is done, or when whatever
// is behind it has given up for good — which is what tells the TUI it isn't live any more.
type MailWatcher func(ctx context.Context) (<-chan int64, error)

// ScreenerWatcher opens the stream that says The Screener changed. HEY signs the stream
// name and serves it alongside the pending count, so a watcher can only be opened once
// that name has been read — there is nothing to subscribe to before then.
type ScreenerWatcher func(ctx context.Context, signedStreamName string) (<-chan struct{}, error)

// AnyBoxChanged stands for "something changed, we don't know what" — a watcher sends it
// after a reconnect, where the changes broadcast while it was away were missed.
const AnyBoxChanged int64 = 0

// liveRefreshDelay collects one delivery's changes into a single re-read: a thread lands
// as several postings, and each one rings the doorbell separately.
//
// liveRetryDelay is how long a re-read waits when it can't be applied yet, because a form
// or a picker is open over the list. Nothing is read until it closes, but the change is
// held onto rather than dropped.
const (
	liveRefreshDelay = 500 * time.Millisecond
	liveRetryDelay   = 2 * time.Second
)

// mailWatchStartedMsg carries the stream a watcher opened, or the reason there isn't one.
type mailWatchStartedMsg struct {
	changes <-chan int64
	err     error
}

// mailChangedMsg reports one changed box, or a stream that has closed.
type mailChangedMsg struct {
	boxID  int64
	closed bool
}

// mailRefreshDueMsg is the re-read a change asked for, once its delay has passed.
type mailRefreshDueMsg struct{ boxID int64 }

func startMailWatchCmd(ctx context.Context, watch MailWatcher) tea.Cmd {
	if watch == nil {
		return nil
	}
	return func() tea.Msg {
		changes, err := watch(ctx)
		return mailWatchStartedMsg{changes: changes, err: err}
	}
}

// waitForMailChangeCmd blocks until the next box changes, then reports it once. The
// handler re-arms it, the way watchThemeCmd is re-armed.
func waitForMailChangeCmd(changes <-chan int64) tea.Cmd {
	if changes == nil {
		return nil
	}
	return func() tea.Msg {
		boxID, open := <-changes
		if !open {
			return mailChangedMsg{closed: true}
		}
		return mailChangedMsg{boxID: boxID}
	}
}

func refreshMailLaterCmd(boxID int64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return mailRefreshDueMsg{boxID: boxID} })
}

// --- The Screener ---

// screenerWatchStartedMsg carries the stream a watcher opened, or the reason there isn't
// one. It names the signed stream it is about, because a watch given up on — HEY has
// named another one, or the account changed — must not be mistaken for the current one.
type screenerWatchStartedMsg struct {
	stream  string
	changes <-chan struct{}
	err     error
}

// screenerChangedMsg reports that someone arrived in or left The Screener, or that the
// stream has closed. HEY broadcasts the Screener's own rendering, so the message says
// nothing the TUI can use: it is a doorbell and the count is read again behind it. The
// stream it came from is named for the same reason screenerWatchStartedMsg names it: a
// stream given up closes, and that close is not news about the one that replaced it.
type screenerChangedMsg struct {
	stream string
	closed bool
}

// screenerRefreshDueMsg is the re-read a Screener change asked for, once its delay has passed.
type screenerRefreshDueMsg struct{}

func startScreenerWatchCmd(ctx context.Context, watch ScreenerWatcher, signedStreamName string) tea.Cmd {
	if watch == nil || signedStreamName == "" {
		return nil
	}
	return func() tea.Msg {
		changes, err := watch(ctx, signedStreamName)
		return screenerWatchStartedMsg{stream: signedStreamName, changes: changes, err: err}
	}
}

func waitForScreenerChangeCmd(stream string, changes <-chan struct{}) tea.Cmd {
	if changes == nil {
		return nil
	}
	return func() tea.Msg {
		_, open := <-changes
		return screenerChangedMsg{stream: stream, closed: !open}
	}
}

func refreshScreenerLaterCmd(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return screenerRefreshDueMsg{} })
}
