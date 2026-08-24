package cmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// The calendar's changes. Recordings — events, todos, habits, journal entries — are what
// the calendars hold; the calendar_* three are the calendars themselves coming and going,
// and calendar_resync is the watch's word that one of them changed more than its feed
// could list.
var calendarWatchChanges = []string{
	"recording_added", "recording_updated", "recording_deleted",
	"calendar_added", "calendar_updated", "calendar_deleted", "calendar_resync",
}

const (
	// calendarCoalesceDelay collects one write's doorbells into a single read: HEY rings a
	// calendar's stream once per recording it touches, and the feed answers them all at once.
	calendarCoalesceDelay = 500 * time.Millisecond

	// watchCalendarPollInterval is how often the calendar-level feed is read, which is the
	// only way the watch learns of a calendar added or removed while it runs: there is no
	// stream to follow until a calendar's name is known. The read is usually one empty page.
	watchCalendarPollInterval = 5 * time.Minute
)

const (
	watchCalendarAdded   = "calendar_added"
	watchCalendarUpdated = "calendar_updated"
	watchCalendarDeleted = "calendar_deleted"
	watchCalendarResync  = "calendar_resync"
)

// watchedCalendar is one calendar the watch follows: how far its recording feed has been
// read, and the stream that rings when it changes.
type watchedCalendar struct {
	id     int64
	name   string
	cursor hey.CalendarChangesCursor
	stream string
	stop   context.CancelFunc
}

// calendarsWatch is the calendar half of a watch: the calendars and their cursors, the
// doorbell their streams ring, the coalescing timer behind it, and the poll that keeps
// the set current.
type calendarsWatch struct {
	calendars  map[int64]*watchedCalendar
	listCursor hey.CalendarChangesCursor
	unread     map[int64]bool

	pendingMu sync.Mutex
	pending   map[int64]bool
	wake      chan struct{}
	due       <-chan time.Time
	poll      *time.Ticker
}

// watchingCalendars is whether this invocation follows the calendars at all. Email-specific
// flags switch them off: --box scopes the watch to mail, and an --events list that names
// only mail changes has not asked for the calendar.
func (c *watchCommand) watchingCalendars(changes map[string]bool) bool {
	if len(c.boxes) > 0 {
		return false
	}
	return slices.ContainsFunc(calendarWatchChanges, func(change string) bool { return changes[change] })
}

// watchedCalendars reads the calendars and where each one's feed should be read from. The
// cursors are capped at the watch's start the way the boxes' are — the server bakes each
// calendar's last activity into its URL, so a write that lands between reading the clock
// and reading the list would otherwise sit behind the cursor, read by nothing.
func (c *watchCommand) watchedCalendars(ctx context.Context, started time.Time) (*calendarsWatch, error) {
	list, err := sdk.Calendars().ListWithChanges(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if list == nil {
		return nil, apierr.ErrAPI(0, "could not list calendars")
	}

	listCursor, err := c.calendarCursor(list.CalendarChangesURL, started)
	if err != nil {
		return nil, err
	}

	watch := &calendarsWatch{
		calendars:  map[int64]*watchedCalendar{},
		listCursor: listCursor,
		unread:     map[int64]bool{},
		pending:    map[int64]bool{},
		wake:       make(chan struct{}, 1),
		poll:       time.NewTicker(watchCalendarPollInterval),
	}
	for _, listed := range list.Calendars {
		calendar, err := c.followedCalendar(listed, started)
		if err != nil {
			return nil, err
		}
		watch.calendars[calendar.id] = calendar
	}

	return watch, nil
}

func (c *watchCommand) followedCalendar(listed hey.ListedCalendar, started time.Time) (*watchedCalendar, error) {
	cursor, err := c.calendarCursor(listed.RecordingChangesURL, started)
	if err != nil {
		return nil, err
	}

	return &watchedCalendar{
		id:     listed.Calendar.Id,
		name:   calendarDisplayName(listed.Calendar),
		cursor: cursor,
		stream: listed.SignedStreamName,
	}, nil
}

// calendarCursor is where a feed should be read from: the URL HEY served, moved by
// --since, and otherwise capped at the watch's start.
func (c *watchCommand) calendarCursor(changesURL string, started time.Time) (hey.CalendarChangesCursor, error) {
	cursor, err := hey.CalendarChangesCursorFrom(changesURL)
	if err != nil {
		return hey.CalendarChangesCursor{}, apierr.FromSDK(err)
	}
	if c.since == "" {
		return calendarCursorNoLaterThan(cursor, started), nil
	}

	at, err := parseWatchSince(c.since)
	if err != nil {
		return hey.CalendarChangesCursor{}, err
	}
	cursor.Since = at.UTC().Format(watchCursorTimeLayout)

	return cursor, nil
}

// calendarCursorNoLaterThan is noLaterThan for a calendar feed's cursor, and exists for
// the same race. A cursor that cannot be read is left as it is.
func calendarCursorNoLaterThan(cursor hey.CalendarChangesCursor, started time.Time) hey.CalendarChangesCursor {
	at, err := time.Parse(watchCursorTimeLayout, cursor.Since)
	if err == nil && at.After(started) {
		cursor.Since = started.UTC().Format(watchCursorTimeLayout)
	}

	return cursor
}

// calendarDisplayName is what a calendar is called on a watch line. The personal calendar
// has no name of its own — HEY leaves the field empty and the web app labels it from the
// identity — so the watch says what it is rather than nothing.
func calendarDisplayName(calendar generated.Calendar) string {
	if calendar.Name != "" {
		return calendar.Name
	}
	if calendar.Personal {
		return "Personal"
	}
	return fmt.Sprintf("Calendar %d", calendar.Id)
}

// ring notes which calendar rang and wakes the watch without blocking the cable's
// dispatcher. The pending set coalesces a burst into one read per calendar.
func (cw *calendarsWatch) ring(calendarID int64) {
	cw.pendingMu.Lock()
	cw.pending[calendarID] = true
	cw.pendingMu.Unlock()
	ring(cw.wake, struct{}{})
}

// take empties the pending set, in a stable order.
func (cw *calendarsWatch) take() []int64 {
	cw.pendingMu.Lock()
	defer cw.pendingMu.Unlock()

	rung := slices.Sorted(maps.Keys(cw.pending))
	clear(cw.pending)

	return rung
}

// --- The watch's calendar half, on the same loop as the boxes ---

// calendarWake, calendarDue and calendarPoll are the loop's view of a calendar half that
// may not exist: a receive on a nil channel blocks forever, which is exactly what a watch
// without calendars wants from these arms.
func (w *postingsWatch) calendarWake() <-chan struct{} {
	if w.calendar == nil {
		return nil
	}
	return w.calendar.wake
}

func (w *postingsWatch) calendarDue() <-chan time.Time {
	if w.calendar == nil {
		return nil
	}
	return w.calendar.due
}

func (w *postingsWatch) calendarPoll() <-chan time.Time {
	if w.calendar == nil {
		return nil
	}
	return w.calendar.poll.C
}

func (w *postingsWatch) calendarsBehind() bool {
	return w.calendar != nil && len(w.calendar.unread) > 0
}

// subscribeCalendars follows every calendar's stream over the watch's own connection. A
// calendar whose stream cannot be subscribed is still read at every catch-up; the warning
// says its changes will not ring between them.
func (w *postingsWatch) subscribeCalendars(ctx context.Context) {
	for _, id := range slices.Sorted(maps.Keys(w.calendar.calendars)) {
		w.subscribeCalendar(ctx, w.calendar.calendars[id])
	}
}

func (w *postingsWatch) subscribeCalendar(ctx context.Context, calendar *watchedCalendar) {
	if calendar.stream == "" || w.cable == nil {
		return
	}

	subCtx, stop := context.WithCancel(ctx) //nolint:gosec // G118: cancel stored on the calendar, called when it stops being watched
	subscription, err := w.cable.Subscribe(subCtx, actioncable.Identifier{
		Channel: turboStreamsChannel,
		Params:  actioncable.Params{"signed_stream_name": calendar.stream},
	})
	if err != nil {
		stop()
		fmt.Fprintf(w.errOut, "warning: could not follow %s's stream — its changes surface on reconnects only: %v\n", calendar.name, err)
		return
	}
	calendar.stop = stop

	id := calendar.id
	go func() {
		defer unsubscribe(subCtx, subscription)
		for {
			select {
			case <-subCtx.Done():
				return
			case _, open := <-subscription.Messages():
				if !open {
					return
				}
				w.calendar.ring(id)
			}
		}
	}()
}

// coalesceCalendarRing arms the read behind a doorbell, once: the timer collects the rest
// of the burst, and readRungCalendars empties the pending set whole.
func (w *postingsWatch) coalesceCalendarRing() {
	if w.calendar.due == nil {
		w.calendar.due = time.After(calendarCoalesceDelay)
	}
}

func (w *postingsWatch) readRungCalendars(ctx context.Context) error {
	w.calendar.due = nil
	for _, id := range w.calendar.take() {
		if calendar, watching := w.calendar.calendars[id]; watching {
			if err := w.readCalendar(ctx, calendar); err != nil {
				return err
			}
		}
	}
	w.readyOnceCaughtUp(ctx)

	return nil
}

func (w *postingsWatch) readEveryCalendar(ctx context.Context) error {
	if w.calendar == nil {
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(w.calendar.calendars)) {
		if err := w.readCalendar(ctx, w.calendar.calendars[id]); err != nil {
			return err
		}
	}

	return nil
}

func (w *postingsWatch) readUnreadCalendars(ctx context.Context) error {
	if w.calendar == nil {
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(w.calendar.unread)) {
		if calendar, watching := w.calendar.calendars[id]; watching {
			if err := w.readCalendar(ctx, calendar); err != nil {
				return err
			}
		}
	}

	return nil
}

func (w *postingsWatch) readCalendar(ctx context.Context, calendar *watchedCalendar) error {
	changes, err := sdk.Calendars().AllRecordingChanges(ctx, calendar.id, calendar.cursor)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // an interrupt or a --timeout is how a watch is meant to end
		case permanentReadError(err):
			return apierr.FromSDK(err)
		default:
			fmt.Fprintf(w.errOut, "warning: could not read changes in %s: %v\n", calendar.name, err)
			w.calendar.unread[calendar.id] = true
			w.armRetry()
			return nil
		}
	}
	delete(w.calendar.unread, calendar.id)
	w.settleBackoff()

	if changes.FullSyncRequired {
		fmt.Fprintf(w.errOut, "notice: too much changed in %s to follow one change at a time — skipping ahead, re-read the calendar\n", calendar.name)
		skipped, err := w.skipCalendarAhead(ctx, calendar)
		if err != nil {
			return err
		}
		if skipped {
			w.reportCalendar(ctx, watchEvent{Change: watchCalendarResync, At: watchTime(time.Now())}, calendar.id, calendar.name)
		}
		return nil
	}

	if changes.NextCursor != nil {
		calendar.cursor = *changes.NextCursor
	}

	w.reportRecordings(ctx, calendar, "recording_added", changes.Added, func(recording generated.Recording) time.Time { return recording.CreatedAt })
	w.reportRecordings(ctx, calendar, "recording_updated", changes.Updated, func(recording generated.Recording) time.Time { return recording.UpdatedAt })
	for _, deleted := range changes.Deleted {
		w.reportCalendar(ctx, watchEvent{
			Change:        "recording_deleted",
			At:            watchTime(deleted.DeletedAt),
			RecordingID:   deleted.ID,
			RecordingType: deleted.Type,
		}, calendar.id, calendar.name)
	}

	return nil
}

// reportRecordings walks one bucket in type order, so a read reports the same changes in
// the same order every time.
func (w *postingsWatch) reportRecordings(ctx context.Context, calendar *watchedCalendar, change string, bucket map[string][]generated.Recording, at func(generated.Recording) time.Time) {
	for _, recordingType := range slices.Sorted(maps.Keys(bucket)) {
		for _, recording := range bucket[recordingType] {
			w.reportCalendar(ctx, watchEvent{
				Change:        change,
				At:            watchTime(at(recording)),
				RecordingID:   recording.Id,
				RecordingType: recordingType,
				Recording:     &recording,
			}, calendar.id, calendar.name)
		}
	}
}

// skipCalendarAhead moves a calendar's cursor to the server's current one, which is the
// only way back once its feed has fallen too far behind, and says whether it did. A
// calendar the server no longer lists cannot be followed any more and stops being watched
// — the calendar-level feed reports its deletion in its own time.
func (w *postingsWatch) skipCalendarAhead(ctx context.Context, calendar *watchedCalendar) (bool, error) {
	list, err := sdk.Calendars().ListWithChanges(ctx)
	if err != nil {
		return false, apierr.FromSDK(err)
	}
	if list == nil {
		return false, apierr.ErrAPI(0, "could not list calendars")
	}

	for _, listed := range list.Calendars {
		if listed.Calendar.Id == calendar.id {
			cursor, err := hey.CalendarChangesCursorFrom(listed.RecordingChangesURL)
			if err != nil {
				return false, apierr.FromSDK(err)
			}
			calendar.cursor = cursor
			return true, nil
		}
	}

	w.stopWatchingCalendar(calendar.id)
	return false, nil
}

// pollCalendarList reads the calendar-level feed: calendars that arrived are followed
// from here on and read once, calendars that left stop being watched, and every one of
// them is a change to report. A read that fails is skipped — the cursor has not moved, so
// the next poll reads the same changes.
func (w *postingsWatch) pollCalendarList(ctx context.Context) error {
	changes, err := sdk.Calendars().AllCalendarChanges(ctx, w.calendar.listCursor)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // an interrupt or a --timeout is how a watch is meant to end
		case permanentReadError(err):
			return apierr.FromSDK(err)
		default:
			fmt.Fprintf(w.errOut, "warning: could not read the calendar changes: %v\n", err)
			return nil
		}
	}

	for _, added := range changes.Added {
		calendar, err := w.startWatchingCalendar(ctx, added)
		if err != nil {
			return err
		}
		w.reportCalendar(ctx, watchEvent{Change: watchCalendarAdded, At: watchTime(added.Calendar.UpdatedAt)}, calendar.id, calendar.name)
		if err := w.readCalendar(ctx, calendar); err != nil {
			return err
		}
	}
	for _, updated := range changes.Updated {
		name := calendarDisplayName(updated)
		if calendar, watching := w.calendar.calendars[updated.Id]; watching {
			calendar.name = name
		}
		w.reportCalendar(ctx, watchEvent{Change: watchCalendarUpdated, At: watchTime(updated.UpdatedAt)}, updated.Id, name)
	}
	for _, deleted := range changes.Deleted {
		name := fmt.Sprintf("Calendar %d", deleted.ID)
		if calendar, watching := w.calendar.calendars[deleted.ID]; watching {
			name = calendar.name
		}
		w.stopWatchingCalendar(deleted.ID)
		w.reportCalendar(ctx, watchEvent{Change: watchCalendarDeleted, At: watchTime(deleted.DeletedAt)}, deleted.ID, name)
	}

	if changes.NextCursor != nil {
		w.calendar.listCursor = *changes.NextCursor
	}

	return nil
}

func (w *postingsWatch) startWatchingCalendar(ctx context.Context, listed hey.ListedCalendar) (*watchedCalendar, error) {
	cursor, err := hey.CalendarChangesCursorFrom(listed.RecordingChangesURL)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}

	calendar := &watchedCalendar{
		id:     listed.Calendar.Id,
		name:   calendarDisplayName(listed.Calendar),
		cursor: cursor,
		stream: listed.SignedStreamName,
	}
	w.calendar.calendars[calendar.id] = calendar
	w.subscribeCalendar(ctx, calendar)

	return calendar, nil
}

func (w *postingsWatch) stopWatchingCalendar(calendarID int64) {
	calendar, watching := w.calendar.calendars[calendarID]
	if !watching {
		return
	}
	if calendar.stop != nil {
		calendar.stop()
	}
	delete(w.calendar.calendars, calendarID)
	delete(w.calendar.unread, calendarID)

	w.calendar.pendingMu.Lock()
	delete(w.calendar.pending, calendarID)
	w.calendar.pendingMu.Unlock()
}

// reportCalendar stamps the calendar onto the event and hands it on the way a box change
// is handed on.
func (w *postingsWatch) reportCalendar(ctx context.Context, event watchEvent, calendarID int64, name string) {
	event.Calendar = &watchEventCalendar{ID: calendarID, Name: name}
	w.report(ctx, event, nil, nil)
}
