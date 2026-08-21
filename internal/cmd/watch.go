package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	actioncable "github.com/basecamp/actioncable-go"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/cable"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

const changesChannel = "Postings::ChangesChannel"

// A failed read of the changes feed is retried on its own, doubling the wait each time so
// a server that stays down isn't hammered, and asyncScriptLimit caps how many --run-async
// commands run at once so a catch-up of thousands of changes can't fork a process for each.
const (
	firstWatchRetry   = 2 * time.Second
	longestWatchRetry = 2 * time.Minute
	asyncScriptLimit  = 16
)

var watchableChanges = []string{"added", "updated", "deleted"}

type watchCommand struct {
	cmd         *cobra.Command
	boxes       []string
	events      []string
	since       string
	asyncScript string
	syncScript  string
	exitOnFirst bool
	notify      bool
	notifyBoxes []string
	timeout     time.Duration
}

func newWatchCommand() *watchCommand {
	watchCommand := &watchCommand{}
	watchCommand.cmd = &cobra.Command{
		Use:   "watch",
		Short: "Follow email threads as they change",
		Long: `Print email threads as they change, one JSON object per line. Runs until interrupted.

Changes can drive a command instead of being printed, and that is a choice between two
behaviours: --run-async spawns the command per change and moves on, so a slow one never
holds up the watch and two can overlap; --run-sync waits for each and runs them in order.
Pass one or the other.

Besides the three thread changes, three lines describe the watch itself: "ready" once the
cursor is set and the subscription is live (again after every reconnect's catch-up),
"disconnected" when the connection drops, and "resync" when a box changed more than the
feed can list one change at a time and the watch skipped ahead. A resync is a change —
scripts run for it and --exit-on-first counts it; ready and disconnected are written to
stdout only.

--notify shows a desktop notification for new mail in the Imbox among the changes: at most
one per batch, replacing the last rather than stacking, under the app-name HEY so
notification silencing applies. --notify-box toasts another watched box instead, or more
than one. It needs notify-send (libnotify).`,
		Annotations: map[string]string{
			"agent_notes": "Long-running. Writes one JSON object per changed thread to stdout (NDJSON), not the usual envelope. Use --exit-on-first to block until one change lands and then exit.",
		},
		Example: `  hey watch
  hey watch --box imbox --events added
  hey watch --box imbox --exit-on-first
  hey watch --notify
  hey watch --run-async 'notify-send "New mail in $HEY_BOX_KIND"'
  hey watch --run-sync ./triage.sh
  hey watch --since 2026-08-18T09:00:00Z`,
		RunE: watchCommand.run,
		Args: cobra.NoArgs,
	}

	flags := watchCommand.cmd.Flags()
	flags.StringArrayVar(&watchCommand.boxes, "box", nil, "Box to watch by name or ID (repeatable, defaults to all)")
	flags.StringSliceVar(&watchCommand.events, "events", watchableChanges, "Changes to report: added, updated, deleted")
	flags.StringVar(&watchCommand.since, "since", "", "Report changes since this time first (RFC 3339 or YYYY-MM-DD)")
	flags.StringVar(&watchCommand.asyncScript, "run-async", "", "Shell command to spawn per change, without waiting for it")
	flags.StringVar(&watchCommand.syncScript, "run-sync", "", "Shell command to run per change, one at a time, waiting for each")
	flags.BoolVar(&watchCommand.exitOnFirst, "exit-on-first", false, "Exit after the first change")
	flags.BoolVar(&watchCommand.notify, "notify", false, "Show a desktop notification for new mail among the watched changes")
	flags.StringArrayVar(&watchCommand.notifyBoxes, "notify-box", nil, "Box whose new mail --notify toasts, by name or ID (repeatable, defaults to the Imbox)")
	flags.DurationVar(&watchCommand.timeout, "timeout", 0, "Give up waiting after this long (for example 30m)")

	return watchCommand
}

func (c *watchCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	changes, err := c.watchedChanges()
	if err != nil {
		return err
	}
	if c.asyncScript != "" && c.syncScript != "" {
		return apierr.ErrUsage("pass either --run-async or --run-sync, not both")
	}

	ctx, stopListeningForSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopListeningForSignals()

	if c.timeout > 0 {
		timed, giveUp := context.WithTimeout(ctx, c.timeout)
		defer giveUp()
		ctx = timed
	}

	// The notifier measures new activity against its start, so it starts before
	// the boxes' cursors are read: nothing can then change between the two.
	var notifier *desktopNotifier
	if c.notify {
		notifier = newDesktopNotifier(cmd.ErrOrStderr(), serverNow(ctx))
	}

	boxes, err := c.watchedBoxes(ctx)
	if err != nil {
		return err
	}
	if c.notify {
		notified, notifyErr := c.notifiedBoxes(boxes)
		if notifyErr != nil {
			return notifyErr
		}
		if notifier != nil {
			notifier.boxes = notified
		}
	}

	watch := &postingsWatch{
		boxes:       boxes,
		changes:     changes,
		asyncScript: c.asyncScript,
		syncScript:  c.syncScript,
		exitOnFirst: c.exitOnFirst,
		notifier:    notifier,
		out:         cmd.OutOrStdout(),
		errOut:      cmd.ErrOrStderr(),
		styled:      writer.IsStyled(),
		connection:  make(chan struct{}, 1),
		unread:      map[int64]bool{},
		running:     make(chan struct{}, asyncScriptLimit),
	}

	client, err := cable.Dial(ctx, cfg.BaseURL, authMgr)
	if err != nil {
		return watchDialError(err)
	}
	defer func() { _ = client.Close() }()

	subscription, err := client.Subscribe(ctx, actioncable.Identifier{Channel: changesChannel},
		actioncable.OnConnected(func(reconnected bool) {
			if reconnected {
				watch.noteConnection(true)
			}
		}),
		actioncable.OnDisconnected(func(willReconnect bool) { watch.noteConnection(false) }),
		actioncable.OnRejected(func() { watch.rejected.Store(true) }))
	if err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not subscribe to posting changes: %v", err))
	}

	if err := watch.listen(ctx, subscription); err != nil {
		return err
	}

	// A synchronous script's verdict is the command's verdict when we only waited
	// for the one change, and there's no other way to answer with its exit code.
	if c.exitOnFirst && watch.lastScriptExit != 0 {
		os.Exit(watch.lastScriptExit)
	}

	return nil
}

// watchDialError tells the two ways a dial fails apart: the server turned the
// credentials down, or it couldn't be reached at all.
func watchDialError(err error) error {
	var disconnect *actioncable.DisconnectError
	if errors.As(err, &disconnect) && disconnect.Reason == actioncable.ReasonUnauthorized {
		return apierr.ErrAuth("HEY's cable server turned these credentials down — run `hey auth login` again, or log in with `hey auth login --cookie` if the server doesn't take access tokens on a websocket yet")
	}

	return apierr.ErrNetwork(fmt.Errorf("could not connect to HEY's cable server: %w", err))
}

func (c *watchCommand) watchedChanges() (map[string]bool, error) {
	changes := map[string]bool{}
	for _, event := range c.events {
		event = strings.ToLower(strings.TrimSpace(event))
		if !slices.Contains(watchableChanges, event) {
			return nil, apierr.ErrUsage(fmt.Sprintf("unknown event %q — pass any of %s", event, strings.Join(watchableChanges, ", ")))
		}
		changes[event] = true
	}

	if len(changes) == 0 {
		return nil, apierr.ErrUsage("--events needs at least one of " + strings.Join(watchableChanges, ", "))
	}

	return changes, nil
}

func (c *watchCommand) watchedBoxes(ctx context.Context) (map[int64]*watchedBox, error) {
	listed, err := sdk.Boxes().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if listed == nil {
		return nil, apierr.ErrAPI(0, "could not list boxes")
	}

	watched := map[int64]*watchedBox{}
	for _, box := range *listed {
		if !c.watching(box) {
			continue
		}

		cursor, err := watchCursor(box.PostingChangesUrl, c.since)
		if err != nil {
			return nil, err
		}
		if cursor.Since == "" {
			continue
		}

		watched[box.Id] = &watchedBox{id: box.Id, kind: box.Kind, name: box.Name, cursor: cursor}
	}

	if len(watched) == 0 {
		return nil, apierr.ErrNotFound("box", strings.Join(c.boxes, ", "))
	}

	return watched, nil
}

func (c *watchCommand) watching(box generated.Box) bool {
	if len(c.boxes) == 0 {
		return true
	}

	return slices.ContainsFunc(c.boxes, func(wanted string) bool { return boxIs(box, wanted) })
}

// notifiedBoxes picks the watched boxes whose new mail --notify toasts: the ones
// --notify-box names, or the Imbox — the one box in HEY that asks for attention.
// Every name has to be a watched box, so a toast nobody will ever see is refused
// up front rather than waited for.
func (c *watchCommand) notifiedBoxes(watched map[int64]*watchedBox) (map[int64]bool, error) {
	wanted := c.notifyBoxes
	if len(wanted) == 0 {
		wanted = []string{"imbox"}
	}

	notified := map[int64]bool{}
	for _, name := range wanted {
		found := false
		for id, box := range watched {
			if boxIs(generated.Box{Id: box.id, Kind: box.kind, Name: box.name}, name) {
				notified[id] = true
				found = true
			}
		}
		if !found {
			return nil, apierr.ErrUsage(fmt.Sprintf("--notify toasts %q, which isn't being watched — watch it with --box, or name a watched box with --notify-box", name))
		}
	}

	return notified, nil
}

func boxIs(box generated.Box, wanted string) bool {
	return strings.EqualFold(wanted, box.Kind) ||
		strings.EqualFold(wanted, box.Name) ||
		wanted == strconv.FormatInt(box.Id, 10)
}

// watchCursor is where a box's changes feed should be read from. The server bakes its own
// clock into the box's changes URL, so that's the cursor unless --since moves it.
func watchCursor(changesURL, since string) (hey.PostingChangesCursor, error) {
	if changesURL == "" {
		return hey.PostingChangesCursor{}, nil
	}

	cursor, err := hey.PostingChangesCursorFrom(changesURL)
	if err != nil {
		return hey.PostingChangesCursor{}, err
	}
	if since == "" {
		return cursor, nil
	}

	at, err := parseWatchSince(since)
	if err != nil {
		return hey.PostingChangesCursor{}, err
	}
	cursor.Since = at.UTC().Format("2006-01-02T15:04:05.000Z")

	return cursor, nil
}

func parseWatchSince(since string) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, since); err == nil {
		return at, nil
	}
	if at, err := time.Parse("2006-01-02", since); err == nil {
		return at, nil
	}
	return time.Time{}, apierr.ErrUsage(fmt.Sprintf("could not read --since %q — pass an RFC 3339 time or YYYY-MM-DD", since))
}

type watchedBox struct {
	id     int64
	kind   string
	name   string
	cursor hey.PostingChangesCursor
}

// watchEvent is one changed posting, as a line of NDJSON or as a script's stdin —
// or a word about the watch itself, which has no posting and, for ready and
// disconnected, no box either.
type watchEvent struct {
	Change    string             `json:"change"`
	At        string             `json:"at"`
	Box       *watchEventBox     `json:"box,omitempty"`
	PostingID int64              `json:"posting_id,omitempty"`
	ThreadID  int64              `json:"thread_id,omitempty"`
	Posting   *generated.Posting `json:"posting,omitempty"`
}

// The watch's own news. A resync is a change — something happened, more than the
// feed could list — so it goes wherever changes go; ready and disconnected are
// written to stdout only: a script runs per change, and neither is one.
const (
	watchReady        = "ready"
	watchDisconnected = "disconnected"
	watchResync       = "resync"
)

type watchEventBox struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// postingsWatch holds what a run of the command follows: the boxes and how far each
// one has been read, and what to do with a change once it arrives.
type postingsWatch struct {
	boxes          map[int64]*watchedBox
	changes        map[string]bool
	asyncScript    string
	syncScript     string
	exitOnFirst    bool
	notifier       *desktopNotifier
	out            io.Writer
	errOut         io.Writer
	styled         bool
	connection     chan struct{}
	transitionsMu  sync.Mutex
	transitions    []bool
	rejected       atomic.Bool
	unread         map[int64]bool
	backoff        time.Duration
	retry          <-chan time.Time
	running        chan struct{}
	reported       int
	lastScriptExit int
}

func (w *postingsWatch) listen(ctx context.Context, subscription *actioncable.Subscription) error {
	if err := w.readEveryBox(ctx); err != nil {
		return err
	}
	// From here on, anything after the cursors is reported: a reader that wants
	// a gap-free picture reads its own state now, not before.
	w.announce(watchReady)

	for !w.finished() {
		select {
		case <-ctx.Done():
			// An interrupt or a --timeout is how a watch is meant to end.
			return nil
		case <-w.connection:
			if err := w.followConnection(ctx); err != nil {
				return err
			}
		case <-w.retry:
			w.retry = nil
			if err := w.readUnreadBoxes(ctx); err != nil {
				return err
			}
		case message, open := <-subscription.Messages():
			if !open {
				return w.closedError(ctx)
			}
			if err := w.read(ctx, message); err != nil {
				return err
			}
		}
	}

	return nil
}

// closedError tells the two ways the subscription's messages dry up apart: the watch was
// interrupted or timed out, which is how it's meant to end, or the connection went away
// for good and there is nothing left listening — which a watch left running unattended
// has to hear about rather than exiting quietly.
func (w *postingsWatch) closedError(ctx context.Context) error {
	switch {
	case ctx.Err() != nil:
		return nil //nolint:nilerr // an interrupt or a --timeout is how a watch is meant to end
	case w.rejected.Load():
		return apierr.ErrAuth("HEY's cable server turned this subscription down — run `hey auth login` again, or log in with `hey auth login --cookie` if the server doesn't take access tokens on a websocket yet")
	default:
		return apierr.ErrNetwork(errors.New("HEY's cable server hung up for good — nothing is watching for changes any more"))
	}
}

// noteConnection is called from the cable client's own goroutine with every drop
// and reconnect. The transitions queue up in the order they happened, and the
// signal never blocks the connection: one wake-up drains them all.
func (w *postingsWatch) noteConnection(connected bool) {
	w.transitionsMu.Lock()
	w.transitions = append(w.transitions, connected)
	w.transitionsMu.Unlock()

	select {
	case w.connection <- struct{}{}:
	default:
	}
}

// followConnection acts on the queued transitions in order: a drop is announced,
// a reconnect catches every box up and then announces ready. Order is what keeps
// a reader's picture right — a reconnect that completed while a slow catch-up
// held the loop must not have its earlier drop announced after its ready.
func (w *postingsWatch) followConnection(ctx context.Context) error {
	for _, connected := range w.drainTransitions() {
		if !connected {
			w.announce(watchDisconnected)
			continue
		}
		if err := w.readEveryBox(ctx); err != nil {
			return err
		}
		w.announce(watchReady)
	}

	return nil
}

func (w *postingsWatch) drainTransitions() []bool {
	w.transitionsMu.Lock()
	defer w.transitionsMu.Unlock()

	transitions := w.transitions
	w.transitions = nil

	return transitions
}

func (w *postingsWatch) readEveryBox(ctx context.Context) error {
	for _, id := range slices.Sorted(maps.Keys(w.boxes)) {
		if err := w.readBox(ctx, w.boxes[id]); err != nil {
			return err
		}
	}

	return nil
}

// readUnreadBoxes retries the boxes whose last read failed. Their cursors haven't moved,
// so the changes they missed are still ahead of them.
func (w *postingsWatch) readUnreadBoxes(ctx context.Context) error {
	for _, id := range slices.Sorted(maps.Keys(w.unread)) {
		if err := w.readBox(ctx, w.boxes[id]); err != nil {
			return err
		}
	}

	return nil
}

func (w *postingsWatch) read(ctx context.Context, message actioncable.Message) error {
	var notification struct {
		BoxID int64 `json:"box_id"`
	}
	if err := message.Unmarshal(&notification); err != nil {
		fmt.Fprintf(w.errOut, "warning: could not read a change notification: %v\n", err)
		return nil
	}

	if box, watching := w.boxes[notification.BoxID]; watching {
		return w.readBox(ctx, box)
	}

	return nil
}

func (w *postingsWatch) readBox(ctx context.Context, box *watchedBox) error {
	changes, err := sdk.Postings().AllChanges(ctx, box.id, box.cursor)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // an interrupt or a --timeout is how a watch is meant to end
		case permanentReadError(err):
			return apierr.FromSDK(err)
		default:
			fmt.Fprintf(w.errOut, "warning: could not read changes in %s: %v\n", box.name, err)
			w.readAgainLater(box)
			return nil
		}
	}
	w.wasRead(box)

	if changes.FullSyncRequired {
		fmt.Fprintf(w.errOut, "notice: too much changed in %s to follow one change at a time — skipping ahead, read the box with `hey box %s`\n", box.name, box.kind)
		if err := w.skipAhead(ctx, box); err != nil {
			return err
		}
		w.report(ctx, watchEvent{Change: watchResync, At: watchTime(time.Now())}, box, nil)
		return nil
	}

	if changes.NextCursor != nil {
		box.cursor = *changes.NextCursor
	}

	// The notifier sees every posting, so it knows a thread's activity whatever
	// --events asks for, but toasts only what was reported — the same filter,
	// the same stop at the first change — and one read is one batch, so a burst
	// of mail is one toast.
	var observed, reported []generated.Posting
	for _, posting := range changes.Added {
		observed = append(observed, posting)
		if w.report(ctx, watchEvent{Change: "added", At: watchTime(posting.CreatedAt), PostingID: posting.Id}, box, &posting) {
			reported = append(reported, posting)
		}
	}
	for _, posting := range changes.Updated {
		observed = append(observed, posting)
		if w.report(ctx, watchEvent{Change: "updated", At: watchTime(posting.UpdatedAt), PostingID: posting.Id}, box, &posting) {
			reported = append(reported, posting)
		}
	}
	for _, posting := range changes.Deleted {
		w.report(ctx, watchEvent{Change: "deleted", At: watchTime(posting.DeletedAt), PostingID: posting.Id}, box, nil)
	}
	if w.notifier != nil {
		w.notifier.batch(box, observed, reported)
	}

	return nil
}

// permanentReadError tells a read that will never work from one that might. A malformed
// cursor or credentials the server won't take doesn't get better by waiting two minutes,
// and a watch that retried it silently would sit there for hours and still exit 0.
func permanentReadError(err error) bool {
	switch hey.AsError(err).Code {
	case hey.CodeUsage, hey.CodeAuth:
		return true
	default:
		return false
	}
}

// readAgainLater keeps a box that couldn't be read on the list, and arms the retry that
// comes back to it. Without it a failed read consumes the notification that prompted it,
// and the change stays invisible until the next email happens along.
func (w *postingsWatch) readAgainLater(box *watchedBox) {
	w.unread[box.id] = true

	if w.retry == nil {
		w.backoff = min(max(2*w.backoff, firstWatchRetry), longestWatchRetry)
		w.retry = time.After(w.backoff)
	}
}

func (w *postingsWatch) wasRead(box *watchedBox) {
	delete(w.unread, box.id)

	if len(w.unread) == 0 {
		w.backoff = 0
	}
}

// skipAhead moves a box's cursor to the server's current one, which is the only way
// back once a box has changed more than an increment can carry.
//
// A box the server no longer lists, or no longer serves a changes feed for, has no
// cursor to skip to: keeping the one it had would answer 409 on every read, and
// installing an empty one would be a usage error on every read instead. Either way the
// box can't be followed any more, so it stops being watched.
func (w *postingsWatch) skipAhead(ctx context.Context, box *watchedBox) error {
	listed, err := sdk.Boxes().List(ctx)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if listed == nil {
		return apierr.ErrAPI(0, "could not list boxes")
	}

	for _, listedBox := range *listed {
		if listedBox.Id == box.id {
			cursor, err := watchCursor(listedBox.PostingChangesUrl, "")
			if err != nil {
				return err
			}
			if cursor.Since != "" {
				box.cursor = cursor
				return nil
			}
		}
	}

	return w.stopWatching(box)
}

// stopWatching drops a box the watch can't follow any longer. When it was the last one
// there is nothing left to wait for, and saying so beats a watch that sits there for
// good, reading nothing.
func (w *postingsWatch) stopWatching(box *watchedBox) error {
	fmt.Fprintf(w.errOut, "notice: %s can no longer be followed — it is gone from this account\n", box.name)
	delete(w.boxes, box.id)
	delete(w.unread, box.id)

	if len(w.boxes) == 0 {
		return apierr.ErrNotFound("box", box.name)
	}

	return nil
}

// report hands one change on — printed, or run through the script — and says
// whether it did.
func (w *postingsWatch) report(ctx context.Context, event watchEvent, box *watchedBox, posting *generated.Posting) bool {
	if w.finished() || !w.reporting(event.Change) {
		return false
	}

	event.Box = &watchEventBox{ID: box.id, Kind: box.kind, Name: box.name}
	if posting != nil {
		event.Posting = posting
		event.ThreadID = resolvePostingTopicID(*posting)
	}
	w.reported++

	switch {
	case w.asyncScript != "":
		w.spawnScript(ctx, event)
	case w.syncScript != "":
		w.lastScriptExit = w.runScript(ctx, event)
	case w.styled:
		fmt.Fprintln(w.out, watchLine(event))
	default:
		w.writeJSON(event)
	}

	return true
}

// reporting says whether a change is handed on: --events filters the posting
// changes, and a resync is news whatever was asked for.
func (w *postingsWatch) reporting(change string) bool {
	return !slices.Contains(watchableChanges, change) || w.changes[change]
}

func (w *postingsWatch) finished() bool {
	return w.exitOnFirst && w.reported > 0
}

// announce writes a word about the watch itself to whoever reads the stream. A
// script runs per change and this is not one, so a --run-* watch isn't told,
// and it never counts towards --exit-on-first.
func (w *postingsWatch) announce(change string) {
	if w.asyncScript != "" || w.syncScript != "" {
		return
	}

	event := watchEvent{Change: change, At: watchTime(time.Now())}
	if w.styled {
		fmt.Fprintln(w.out, watchLine(event))
	} else {
		w.writeJSON(event)
	}
}

// spawnScript starts the script and leaves it to get on with it. Whether it worked,
// and whether it overlaps with the next one, is the script's business — and it outlives
// the watch, so interrupting `hey` doesn't cut a script off halfway.
//
// Only asyncScriptLimit of them run at once, though: a --since catch-up carries thousands
// of changes, and a slow script would have a process per change all fighting for the
// machine. Once they're all busy the watch waits for one to finish — or for an interrupt,
// which drops the change rather than hanging on a script that never ends.
func (w *postingsWatch) spawnScript(ctx context.Context, event watchEvent) {
	command, err := w.scriptCommand(context.WithoutCancel(ctx), w.asyncScript, event)
	if err != nil {
		fmt.Fprintf(w.errOut, "warning: could not run %q: %v\n", w.asyncScript, err)
		return
	}

	select {
	case w.running <- struct{}{}:
	case <-ctx.Done():
		return
	}

	if err := command.Start(); err != nil {
		<-w.running
		fmt.Fprintf(w.errOut, "warning: could not run %q: %v\n", w.asyncScript, err)
		return
	}

	go func() {
		_ = command.Wait()
		<-w.running
	}()
}

func (w *postingsWatch) runScript(ctx context.Context, event watchEvent) int {
	command, err := w.scriptCommand(ctx, w.syncScript, event)
	if err != nil {
		fmt.Fprintf(w.errOut, "warning: could not run %q: %v\n", w.syncScript, err)
		return 1
	}

	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			fmt.Fprintf(w.errOut, "warning: %q exited %d\n", w.syncScript, exit.ExitCode())
			return exit.ExitCode()
		}

		fmt.Fprintf(w.errOut, "warning: could not run %q: %v\n", w.syncScript, err)
		return 1
	}

	return 0
}

// scriptCommand hands the event over twice: as JSON on the script's stdin, for jq, and
// as environment variables, for a one-liner that only wants to know what happened.
func (w *postingsWatch) scriptCommand(ctx context.Context, script string, event watchEvent) (*exec.Cmd, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}

	command := shellCommand(ctx, script)
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	command.Stdout = w.out
	command.Stderr = w.errOut
	command.Env = append(os.Environ(), event.environment()...)

	return command, nil
}

func (w *postingsWatch) writeJSON(event watchEvent) {
	payload, err := output.MarshalJSON(event)
	if err != nil {
		fmt.Fprintf(w.errOut, "warning: could not write a change: %v\n", err)
		return
	}

	fmt.Fprintln(w.out, string(payload))
}

func shellCommand(ctx context.Context, script string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/c", script) // #nosec G204 -- running the command the caller asked for is the point
	}

	return exec.CommandContext(ctx, "sh", "-c", script) // #nosec G204 -- running the command the caller asked for is the point
}

func (e watchEvent) environment() []string {
	environment := []string{
		"HEY_CHANGE=" + e.Change,
		"HEY_AT=" + e.At,
	}
	if e.Box != nil {
		environment = append(environment,
			"HEY_BOX_ID="+strconv.FormatInt(e.Box.ID, 10),
			"HEY_BOX_KIND="+e.Box.Kind,
			"HEY_BOX_NAME="+e.Box.Name)
	}
	if e.PostingID != 0 {
		environment = append(environment, "HEY_POSTING_ID="+strconv.FormatInt(e.PostingID, 10))
	}
	if e.ThreadID != 0 {
		environment = append(environment, "HEY_THREAD_ID="+strconv.FormatInt(e.ThreadID, 10))
	}

	return environment
}

func watchLine(event watchEvent) string {
	var boxName, description string
	if event.Box != nil {
		boxName = event.Box.Name
	}
	switch {
	case event.Change == watchReady:
		description = "watching for changes"
	case event.Change == watchDisconnected:
		description = "connection lost — reconnecting"
	case event.Change == watchResync:
		description = "too much changed to follow one change at a time — skipped ahead"
	case event.Posting != nil:
		description = fmt.Sprintf("%s — %s (thread %d)", terminal.SanitizeLine(event.Posting.Creator.Name), truncate(terminal.SanitizeLine(event.Posting.Summary), 50), event.ThreadID)
	default:
		description = fmt.Sprintf("posting %d", event.PostingID)
	}

	return fmt.Sprintf("%s  %-12s %-24s %s", event.At, event.Change, terminal.SanitizeLine(boxName), description)
}

func watchTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.UTC().Format("2006-01-02T15:04:05.000Z")
}
