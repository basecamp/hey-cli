# Watching for changes over Action Cable

`hey watch` is told when a box changed instead of polling for it.
`internal/cable` dials HEY's cable server with [actioncable-client](https://github.com/basecamp/actioncable-client/tree/main/go)'s Go module,
authorizing the upgrade request with the same credentials the SDK sends on an API request
(`HEY_CABLE_URL` overrides the endpoint). `internal/cmd/watch.go` subscribes to
haystack's `Postings::ChangesChannel`, which broadcasts only `{change, account_id, box_id,
box_kind, posting_ids, at}` — a doorbell, not the change itself.

The change is then read through `Postings().AllChanges`, the same incremental sync feed the
mail clients use, starting from the cursor in the box's `posting_changes_url`. That is what
makes a reconnect safe: the cursor, not the notification, is the source of truth, so a
missed broadcast costs nothing, and a 409 means catch up in full instead. A read that
fails leaves the cursor where it was and is retried on a doubling backoff, so a change
isn't lost with the notification that announced it, and a subscription that closes without
the watch being interrupted is an error rather than a quiet exit. `ready` waits for that
retry too (`catchUp`/`retryUnread`/`readyOnceCaughtUp`): a catch-up that left a box behind
owes its ready until the box is read, and a drop in between cancels the debt — the
reconnect's catch-up announces its own. The look at the transition queue and the ready
announcement are one critical section with `noteConnection`'s queueing, which is what keeps
a drop from landing between them.

New mail is a watch event, not a flag: every added and updated line carries `"new":
true|false` and `--events new` selects the true ones (`internal/cmd/watch_new.go`). New is
unseen, not muted, and `active_at` later than the watch's record of the thread — or than
the watch's start, on HEY's clock (`serverNow`), for a thread it has no record of — and
every posting the watch reads is recorded, in every box and whatever `--events` or `--box`
says (`--box` picks the boxes whose changes are reported; every box is followed), each
posting recorded as soon as it is classified. The
start is the Date header translated back to when the request was made (mail that lands while
the server answers is later than the start), it is taken before the box list, and each
box's cursor starts at it (`watchStartSince`), keeping only the version from the box's
`posting_changes_url`. The since HEY puts there is not its clock but the box's last posting
activity (`Box#last_posting_activity_at`: unbundled postings only, the box's own `updated_at`
when it has none), and the feed answers deletions and bundled postings later than that; and
`/boxes.json` comes through the SDK's ETag cache with an ETag of the box rows alone, which
posting activity does not touch, so a 304 serves the since as it was when the list was
cached. A read from HEY's since reported history as news on every start, and a since later
than the start would leave mail that landed in between behind it, read by nothing. A watch
that cannot read HEY's clock does not start: the workstation's clock is no stand-in for a
cutoff every feed and new mail are measured against, since a fast one would skip changes and
a slow one would report history. That
is HEY's semantics and state across events, so the CLI decides it once; what to do about
it is the reader's. The Date header is whole seconds, so the start can be up to a second
(plus the clock request's whole time, retries included) early and a change from that window
is reported. Nothing HEY serves on demand says the time finer — Action Cable pings are
whole seconds too; a posting doorbell's `at` and the feeds' cursors carry microseconds, but
only once something has changed, never as a "now" before the watch starts — and rounding
the other way would skip changes. A 409 skip-ahead moves the cursor to HEY's clock when it
answered (`serverNowAnswered` — not taken back by the request's time, since a resync has
no gap to catch and a slow request could leave a busy feed still behind), keeping the feed
version from a list read past the SDK's cache (`newUncachedSDKClient`: the list's ETag is
its rows, which neither posting activity nor a new feed version changes, and HEY answers
409 for a version it no longer speaks), and sets that box's floor there
(`newMail.skippedTo`): activity at or before it is never new there, known thread or not,
because the watch never read the gap. The first skip is read from straight away; a 409
after it is the same recovery (`feedRecovery`), and the next skip waits on the retry
backoff rather than every doorbell. The recovery's one resync goes out with the clean read
that ends it, at the last skip, so a reader that re-reads on it has missed nothing a later
skip passed. A list or clock read that fails is retried on that backoff, and an
interrupt during one ends quietly (`skipFailed`). A calendar's 409 skips the same way. `resync` is an event of its own — reported by default,
left out by `--events new` — so a script for new mail never runs on one. With
`--label`, a genuine label gain on a thread the watch already saw without the folder
also counts as new (`classify` in `watch.go`), so `--events new` reports it; a late
tag on a thread never seen before still needs `--events updated`. The Omarchy bar plugin toasts from those lines itself (app-name, glyph,
click-to-focus and the replace-not-stack id all live in the plugin), and nothing
desktop-shaped lives in `watch*.go`.

The TUI's mail list follows the same channel and wants less from it. `internal/cmd/tui_watch.go`
subscribes and relays typed `tui.MailWatchEvent` values; `internal/tui/live.go` defines
that `tui.MailWatcher` contract, so the TUI never sees cable or auth, and a test hands it
a plain channel. Box events are doorbells: `mailView.refreshBox` reads that box's top page
again, which is where a change shows up. Connection events carry disconnect/reconnect
state. A reconnect asks for `tui.AnyBoxChanged`, standing for broadcasts sent during the
gap, and the model re-reads the box on screen.

`internal/tui/live.go` owns both refresh and reconnect timing. `liveRefreshDelay` collects
one delivery's changes into a single re-read — a thread rings the doorbell once per
posting. `liveRetryDelay` is how long a re-read waits when a form or a picker is open over
the list, or a write hasn't landed: the change is held rather than dropped, because a
re-read replaces what the reader is looking at. `mailWatchRetryDelay` backs a watch that
could not start or stopped for good from two seconds up to thirty; authentication failures
stand without retrying. `contentList.refreshHead` is what makes a re-read safe — unlike
`setPostings` it keeps the cursor on its posting and keeps a selection — and the re-read
has its own request lane (`liveRequestID`, `postingsRefreshedMsg`) so it can never be
confused with a read the user asked for, or show the spinner.

The watch outlives the view context, which a mail account switch throws away; that is what
`model.watchCtx` is for. The model holds connection state above the active section, so an
offline or reconnecting notice remains visible in Mail, Contacts, Calendar and Journal.
A temporary drop is followed by Action Cable's own reconnect; an initial network failure
or a stream that closes for good gets a fresh bounded dial from the model. The initial dial
itself is capped so it can report state instead of waiting inside Action Cable forever.
A successful connection removes the status row and catches the current mail box up.

The Screener is told over a different stream, because haystack has no channel for it:
`Clearance::Broadcasting` re-renders the Screener's own button over a Turbo stream, and
`clearances/index.jbuilder` serves that stream's signed name next to the pending count.
So the TUI subscribes to `Turbo::StreamsChannel` with `signed_stream_name` (that is
`tui.ScreenerWatcher`, and the name is why the watch can only be opened after a read).
What HEY broadcasts there is markup for the web app — nothing is parsed out of it, the
arrival is the whole message, and the count is read again behind it. The SDK's
`Clearances().Summary` is that read: the same cheap request as `PendingCount`, keeping
the stream name it used to throw away.

Every read of the count carries that name, which is what lets a stream that closed be
opened again: `startScreenerWatch` opens one whenever the name it is handed is new, so
ctrl+r, closing The Screener, or the doorbell itself will all reopen a watch the server
hung up on. Opening one gives up the one before it — the subscription belongs to the
watch's context, and cancelling is what unsubscribes it, so a stream nobody is following
does not go on ringing. A mail account switch gives it up for the same reason: the signed
name is the account's, and the new account's sources read serves its own. The subscription
uses that short-lived context, while `tuiCableClient` is owned by the TUI-wide watch
context; replacing a Screener name therefore unsubscribes it without closing Mail's shared
connection.

The doorbell always re-reads the count, wherever the user is, because the mail list is
where The Screener announces itself. When The Screener is what's on screen the queue is
re-read too, through `screenerPane.refreshHead` — the same keep-your-place trick as the
mail list — and held while a decision is in flight or the clear-everything question is up.
On the history tab nothing is read; the pending pane is just marked unloaded, which is
what `switchTab` already looks at. All the watches share one websocket
(`tuiCableClient` in `internal/cmd/tui_watch.go`): many subscriptions, one authorization.
A client that stopped itself preserves its terminal failure and never dials again.
`tuiSubscribe` detects that state after any failed subscription, drops the stopped client,
and dials a fresh one; a replacement that also stops is dropped and its failure is
classified as authentication or network so the TUI can respond consistently. Mail
doorbells are coalesced when their bounded queue
is full, while a connection transition drains those stale doorbells and takes priority;
the reconnect catch-up reads the current state once.

The calendars are told the way The Screener is, one Turbo stream per calendar rather than
one channel: `Calendar` broadcasts a refresh on its own stream whenever a recording on it
is written, and `calendars.json` serves each calendar's `signed_stream_name` next to its
`recording_changes_url` — the box shape, on the calendar. Nothing is parsed out of the
frame; the arrival is the whole message. What nobody broadcasts is the calendar *list*
changing, and the web app has the same blind spot (a calendar shared mid-session appears
on its next navigation), so a slow poll of the calendar-level changes feed
(`calendar_changes_url`, `Calendars().AllCalendarChanges` — one page, usually empty) is
how both followers learn of a calendar arriving or leaving. Its `added` bucket carries the
same wrapper as the list, stream name included, so a calendar learned of either way is
immediately subscribable.

The TUI's side is `tui.CalendarWatcher` (`internal/tui/live.go`), implemented by
`watchCalendarChanges` in `internal/cmd/tui_watch.go`: every calendar's stream folded into
one doorbell channel, the poll resubscribing arrivals and dropping leavers, and any
subscription closing on its own tearing the whole watch down — reopening resubscribes
everything, which is cheaper to reason about than limping on with some calendars quiet.
The model follows it only while the Calendar or Journal section is on screen
(`watchingCalendars` in `tui.go`): every other section re-reads on entry, so a doorbell
rung for a section nobody is looking at would be answered by nothing. A ring is debounced
through the same `liveRefreshDelay`, and the re-read is `calendarView.refreshLive` — the
ordinary span read, which never shows the spinner once a day has been drawn and keeps the
selection by `Recording.key()` — or `journalView.refreshLive`, which splices the fresh top
page in front of what the list had grown past it, the `refreshHead` trick with dates for
ids. Both hold the re-read while a form, a picker or a pending delete is up
(`liveRetryDelay`), and a failed start or closed stream retries quietly on
`mailWatchRetryDelay`'s curve: the calendar re-reads on every step the reader takes, so a
watch that is down costs staleness, not a notice.

`hey watch` follows the same streams on its own connection and reports the changes
themselves (`internal/cmd/watch_calendar.go`). Rings are coalesced per calendar for
`calendarCoalesceDelay`, then the calendar's recording feed is read from its cursor
(`Calendars().AllRecordingChanges`, cursors starting at the watch's start like the boxes' —
`calendarCursor`) and each recording is a `recording_added`, `recording_updated`
or `recording_deleted` line naming its calendar where a mail line names its box. The poll
reports `calendar_added`, `calendar_updated` and `calendar_deleted`, and a recording feed's
409 is `calendar_resync` after skipping ahead to HEY's clock, as a box does. The
email-specific flags switch all of it off — `--box`, `--label`, or an `--events` list naming only
mail changes (`watchingCalendars` in watch_calendar.go) — and `ready` waits for the
calendars' catch-up exactly as it waits for the boxes', on the same retry backoff and the
same `readyOnceCaughtUp` critical section.
