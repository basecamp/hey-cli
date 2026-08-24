package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// Watchers are the streams the TUI follows to stay live. All are optional: without them
// a list is the snapshot it was read as.
type Watchers struct {
	Mail     MailWatcher
	Screener ScreenerWatcher
	Calendar CalendarWatcher
}

// MailWatcher opens the stream of mail and connection events that keeps the TUI live.
// Box events ask it to re-read changed mail, while connection events let it show when
// live updates are reconnecting. The stream closes when ctx is done, or when whatever
// is behind it has given up for good.
type MailWatcher func(ctx context.Context) (<-chan MailWatchEvent, error)

// MailConnection is a transition reported by the connection behind a mail watch.
type MailConnection uint8

const (
	MailConnectionUnchanged MailConnection = iota
	MailConnectionDisconnected
	MailConnectionReconnected
)

// MailWatchEvent reports either a changed box or a connection transition. A disconnected
// event says whether the connection is already retrying; a reconnect asks the TUI to
// catch up the box on screen because broadcasts sent during the gap were missed.
type MailWatchEvent struct {
	BoxID         int64
	Connection    MailConnection
	WillReconnect bool
}

// ScreenerWatcher opens the stream that says The Screener changed. ctx owns this signed
// subscription; connectionCtx owns the shared TUI connection that remains live when a
// signed stream is replaced. HEY serves the signed name alongside the pending count, so
// a watcher opens after that name has been read.
type ScreenerWatcher func(ctx, connectionCtx context.Context, signedStreamName string) (<-chan struct{}, error)

// CalendarWatcher opens the stream that says a calendar changed. It subscribes every
// calendar the account can see and folds them into one doorbell: which calendar rang does
// not matter, because the TUI re-reads whatever span is on screen either way. A watcher
// discovers calendars added or removed while it runs on its own and rings for those too.
// The stream closes when ctx is done, or when whatever is behind it has given up for good.
type CalendarWatcher func(ctx, connectionCtx context.Context) (<-chan struct{}, error)

// AnyBoxChanged stands for "something changed, we don't know what" — a watcher sends it
// after a reconnect, where the changes broadcast while it was away were missed.
const AnyBoxChanged int64 = 0

type mailWatchStatus uint8

const (
	mailWatchLive mailWatchStatus = iota
	mailWatchReconnecting
	mailWatchUnavailable
)

// liveRefreshDelay collects one delivery's changes into a single re-read: a thread lands
// as several postings, and each one rings the doorbell separately.
//
// liveRetryDelay is how long a re-read waits when it can't be applied yet, because a form
// or a picker is open over the list. Nothing is read until it closes, but the change is
// held onto rather than dropped.
const (
	liveRefreshDelay      = 500 * time.Millisecond
	liveRetryDelay        = 2 * time.Second
	mailWatchFirstRetry   = 2 * time.Second
	mailWatchMaximumRetry = 30 * time.Second
)

// mailWatchStartedMsg carries the stream a watcher opened, or the reason there isn't one.
type mailWatchStartedMsg struct {
	attempt uint64
	events  <-chan MailWatchEvent
	err     error
}

// mailWatchEventMsg reports one mail-watch event, or a stream that has closed.
type mailWatchEventMsg struct {
	event  MailWatchEvent
	closed bool
}

// mailWatchRetryMsg asks the model to open a new watch after a failed start or a stream
// that stopped for good. The attempt identifies the state that scheduled it, so a timer
// left behind by a successful connection cannot replace that connection.
type mailWatchRetryMsg struct{ attempt uint64 }

// mailRefreshDueMsg is the re-read a change asked for, once its delay has passed.
type mailRefreshDueMsg struct{ boxID int64 }

func startMailWatchCmd(ctx context.Context, watch MailWatcher, attempt uint64) tea.Cmd {
	if watch == nil {
		return nil
	}
	return func() tea.Msg {
		events, err := watch(ctx)
		return mailWatchStartedMsg{attempt: attempt, events: events, err: err}
	}
}

// waitForMailWatchEventCmd blocks until the next box or connection event, then reports it
// once. The handler re-arms it, the way watchThemeCmd is re-armed.
func waitForMailWatchEventCmd(events <-chan MailWatchEvent) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, open := <-events
		if !open {
			return mailWatchEventMsg{closed: true}
		}
		return mailWatchEventMsg{event: event}
	}
}

func retryMailWatchLaterCmd(attempt uint64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return mailWatchRetryMsg{attempt: attempt} })
}

func mailWatchRetryDelay(failures int) time.Duration {
	delay := mailWatchFirstRetry
	for range max(failures-1, 0) {
		if delay >= mailWatchMaximumRetry/2 {
			return mailWatchMaximumRetry
		}
		delay *= 2
	}
	return min(delay, mailWatchMaximumRetry)
}

func retryableMailWatchError(err error) bool {
	var known *apierr.Error
	if errors.As(err, &known) {
		return known.Code == apierr.CodeNetwork
	}
	return apierr.AsError(apierr.FromSDK(err)).Code == apierr.CodeNetwork
}

func (m *model) mailWatchFailed(err error) tea.Cmd {
	m.mailWatchEvents = nil
	if retryableMailWatchError(err) {
		m.mailWatchStatus = mailWatchReconnecting
		m.mailWatchReason = "Offline — reconnecting to HEY"
		return m.retryMailWatch()
	}

	m.mailWatchStatus = mailWatchUnavailable
	m.mailWatchReason = errorNotice("Live updates unavailable", err)
	return nil
}

func (m *model) retryMailWatch() tea.Cmd {
	if m.watchMail == nil {
		return nil
	}
	m.mailWatchStatus = mailWatchReconnecting
	m.mailWatchFailures++
	return retryMailWatchLaterCmd(m.mailWatchAttempt, mailWatchRetryDelay(m.mailWatchFailures))
}

func (m *model) mailWatchStopped() tea.Cmd {
	m.mailWatchReason = "Live updates disconnected — reconnecting to HEY"
	return m.retryMailWatch()
}

func (m *model) mailWatchDisconnected(willReconnect bool) {
	m.mailWatchStatus = mailWatchReconnecting
	m.mailWatchReason = "Live updates disconnected — reconnecting to HEY"
	if !willReconnect {
		m.mailWatchStatus = mailWatchUnavailable
		m.mailWatchReason = "Live updates disconnected"
	}
}

func (m *model) mailWatchConnected() {
	m.mailWatchStatus = mailWatchLive
	m.mailWatchReason = ""
	m.mailWatchFailures = 0
}

func (m model) mailWatchNotice() string {
	switch m.mailWatchStatus {
	case mailWatchReconnecting:
		if m.mailWatchReason != "" {
			return m.mailWatchReason
		}
		return "Live updates disconnected — reconnecting to HEY"
	case mailWatchUnavailable:
		if m.mailWatchReason != "" {
			return m.mailWatchReason
		}
		return "Live updates unavailable"
	default:
		return ""
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

func startScreenerWatchCmd(ctx, connectionCtx context.Context, watch ScreenerWatcher, signedStreamName string) tea.Cmd {
	if watch == nil || signedStreamName == "" {
		return nil
	}
	return func() tea.Msg {
		changes, err := watch(ctx, connectionCtx, signedStreamName)
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

// --- The calendar ---

// calendarWatchStartedMsg carries the stream a watcher opened, or the reason there isn't
// one. The attempt identifies the watch that asked, so a start that lost its race with a
// section switch — the watch was dropped and another opened — cannot install its stream
// over the current one.
type calendarWatchStartedMsg struct {
	attempt uint64
	changes <-chan struct{}
	err     error
}

// calendarChangedMsg reports that a calendar changed, or that the stream has closed. The
// frame HEY broadcasts carries nothing the TUI can use: it is a doorbell, and the span on
// screen is read again behind it.
type calendarChangedMsg struct {
	attempt uint64
	closed  bool
}

// calendarRefreshDueMsg is the re-read a calendar change asked for, once its delay has passed.
type calendarRefreshDueMsg struct{}

// calendarWatchRetryMsg asks the model to open a new calendar watch after a failed start
// or a stream that closed. The attempt identifies the state that scheduled it, the way
// mailWatchRetryMsg's does.
type calendarWatchRetryMsg struct{ attempt uint64 }

func startCalendarWatchCmd(ctx, connectionCtx context.Context, watch CalendarWatcher, attempt uint64) tea.Cmd {
	if watch == nil {
		return nil
	}
	return func() tea.Msg {
		changes, err := watch(ctx, connectionCtx)
		return calendarWatchStartedMsg{attempt: attempt, changes: changes, err: err}
	}
}

func waitForCalendarChangeCmd(attempt uint64, changes <-chan struct{}) tea.Cmd {
	if changes == nil {
		return nil
	}
	return func() tea.Msg {
		_, open := <-changes
		return calendarChangedMsg{attempt: attempt, closed: !open}
	}
}

func refreshCalendarLaterCmd(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return calendarRefreshDueMsg{} })
}

func retryCalendarWatchLaterCmd(attempt uint64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return calendarWatchRetryMsg{attempt: attempt} })
}
