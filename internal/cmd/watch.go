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
Pass one or the other.`,
		Annotations: map[string]string{
			"agent_notes": "Long-running. Writes one JSON object per changed thread to stdout (NDJSON), not the usual envelope. Use --exit-on-first to block until one change lands and then exit.",
		},
		Example: `  hey watch
  hey watch --box imbox --events added
  hey watch --box imbox --exit-on-first
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

	boxes, err := c.watchedBoxes(ctx)
	if err != nil {
		return err
	}

	watch := &postingsWatch{
		boxes:       boxes,
		changes:     changes,
		asyncScript: c.asyncScript,
		syncScript:  c.syncScript,
		exitOnFirst: c.exitOnFirst,
		out:         cmd.OutOrStdout(),
		errOut:      cmd.ErrOrStderr(),
		styled:      writer.IsStyled(),
		catchUp:     make(chan struct{}, 1),
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
				watch.askForCatchUp()
			}
		}),
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

	return slices.ContainsFunc(c.boxes, func(wanted string) bool {
		return strings.EqualFold(wanted, box.Kind) ||
			strings.EqualFold(wanted, box.Name) ||
			wanted == strconv.FormatInt(box.Id, 10)
	})
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

// watchEvent is one changed posting, as a line of NDJSON or as a script's stdin.
type watchEvent struct {
	Change    string             `json:"change"`
	At        string             `json:"at"`
	Box       watchEventBox      `json:"box"`
	PostingID int64              `json:"posting_id"`
	ThreadID  int64              `json:"thread_id,omitempty"`
	Posting   *generated.Posting `json:"posting,omitempty"`
}

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
	out            io.Writer
	errOut         io.Writer
	styled         bool
	catchUp        chan struct{}
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

	for !w.finished() {
		select {
		case <-ctx.Done():
			// An interrupt or a --timeout is how a watch is meant to end.
			return nil
		case <-w.catchUp:
			if err := w.readEveryBox(ctx); err != nil {
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

// askForCatchUp is called from the cable client's own goroutine, so it drops the ask
// when one is already waiting rather than blocking the connection.
func (w *postingsWatch) askForCatchUp() {
	select {
	case w.catchUp <- struct{}{}:
	default:
	}
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
		return w.skipAhead(ctx, box)
	}

	if changes.NextCursor != nil {
		box.cursor = *changes.NextCursor
	}

	for _, posting := range changes.Added {
		w.report(ctx, watchEvent{Change: "added", At: watchTime(posting.CreatedAt), PostingID: posting.Id}, box, &posting)
	}
	for _, posting := range changes.Updated {
		w.report(ctx, watchEvent{Change: "updated", At: watchTime(posting.UpdatedAt), PostingID: posting.Id}, box, &posting)
	}
	for _, posting := range changes.Deleted {
		w.report(ctx, watchEvent{Change: "deleted", At: watchTime(posting.DeletedAt), PostingID: posting.Id}, box, nil)
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

func (w *postingsWatch) report(ctx context.Context, event watchEvent, box *watchedBox, posting *generated.Posting) {
	if w.finished() || !w.changes[event.Change] {
		return
	}

	event.Box = watchEventBox{ID: box.id, Kind: box.kind, Name: box.name}
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
}

func (w *postingsWatch) finished() bool {
	return w.exitOnFirst && w.reported > 0
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
		"HEY_BOX_ID=" + strconv.FormatInt(e.Box.ID, 10),
		"HEY_BOX_KIND=" + e.Box.Kind,
		"HEY_BOX_NAME=" + e.Box.Name,
		"HEY_POSTING_ID=" + strconv.FormatInt(e.PostingID, 10),
	}
	if e.ThreadID != 0 {
		environment = append(environment, "HEY_THREAD_ID="+strconv.FormatInt(e.ThreadID, 10))
	}

	return environment
}

func watchLine(event watchEvent) string {
	description := fmt.Sprintf("posting %d", event.PostingID)
	if event.Posting != nil {
		description = fmt.Sprintf("%s — %s (thread %d)", event.Posting.Creator.Name, truncate(event.Posting.Summary, 50), event.ThreadID)
	}

	return fmt.Sprintf("%s  %-8s %-24s %s", event.At, event.Change, event.Box.Name, description)
}

func watchTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.UTC().Format("2006-01-02T15:04:05.000Z")
}
