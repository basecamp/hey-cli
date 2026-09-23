package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-client/go/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	internalfolders "github.com/basecamp/hey-cli/internal/folders"
)

func TestWatchedChanges(t *testing.T) {
	command := newWatchCommand()
	changes, err := command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes["added"] || !changes["updated"] || !changes["deleted"] || !changes["resync"] || changes["new"] {
		t.Errorf("changes = %v, want every change and resync by default, and new only when asked", changes)
	}

	command.events = []string{"Added", " deleted"}
	changes, err = command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes["added"] || !changes["deleted"] || changes["updated"] {
		t.Errorf("changes = %v, want added and deleted only", changes)
	}

	command.events = []string{"new"}
	changes, err = command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes["new"] || changes["added"] || changes["updated"] || changes["deleted"] || changes["resync"] {
		t.Errorf("changes = %v, want new mail alone — not a resync", changes)
	}

	command.events = []string{"moved"}
	if _, err := command.watchedChanges(); err == nil {
		t.Error("expected an error for an unknown event")
	}

	command.events = nil
	if _, err := command.watchedChanges(); err == nil {
		t.Error("expected an error when no events are left to watch")
	}
}

func TestWatchRunFlagsAreEitherOr(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"watch", "--run-async", "./notify.sh", "--run-sync", "./triage.sh"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when both run flags are given")
	}
	if !strings.Contains(err.Error(), "either --run-async or --run-sync") {
		t.Errorf("error = %q, want it to name both flags as a choice", err.Error())
	}
}

func TestWatchingBox(t *testing.T) {
	imbox := generated.Box{Id: 24088, Kind: "imbox", Name: "Imbox"}
	feed := generated.Box{Id: 24089, Kind: "feedbox", Name: "The Feed"}

	command := newWatchCommand()
	if !command.watching(imbox) || !command.watching(feed) {
		t.Error("every box should be watched when --box isn't given")
	}

	command.boxes = []string{"IMBOX"}
	if !command.watching(imbox) || command.watching(feed) {
		t.Error("--box imbox should match the imbox by kind, case insensitively")
	}

	command.boxes = []string{"The Feed"}
	if !command.watching(feed) || command.watching(imbox) {
		t.Error("--box should match a box by name")
	}

	command.boxes = []string{"24089"}
	if !command.watching(feed) || command.watching(imbox) {
		t.Error("--box should match a box by ID")
	}
}

func TestWatchCursor(t *testing.T) {
	changesURL := "https://app.hey.com/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2"

	cursor, err := watchCursor(changesURL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor.Since != "2026-08-18T09:00:00.000Z" || cursor.Version != "2" {
		t.Errorf("cursor = %+v, want the server's own since and version", cursor)
	}

	cursor, err = watchCursor(changesURL, "2026-08-17T08:30:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor.Since != "2026-08-17T08:30:00.000Z" {
		t.Errorf("since = %q, want --since to move it back", cursor.Since)
	}
	if cursor.Version != "2" {
		t.Errorf("version = %q, want it to survive --since", cursor.Version)
	}

	cursor, err = watchCursor(changesURL, "2026-08-17")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor.Since != "2026-08-17T00:00:00.000Z" {
		t.Errorf("since = %q, want a bare date read as midnight", cursor.Since)
	}

	if _, err := watchCursor(changesURL, "last tuesday"); err == nil {
		t.Error("expected an error for a --since we can't read")
	}
}

func newTestWatch(changes ...string) (*postingsWatch, *bytes.Buffer) {
	watched := map[string]bool{}
	for _, change := range changes {
		watched[change] = true
	}

	out := &bytes.Buffer{}
	return &postingsWatch{
		boxes:      map[int64]*watchedBox{24088: {id: 24088, kind: "imbox", name: "Imbox", reported: true}},
		changes:    watched,
		newMail:    trackNewMail(watchStarted),
		out:        out,
		errOut:     &bytes.Buffer{},
		connection: make(chan struct{}, 1),
		unread:     map[int64]bool{},
		running:    make(chan struct{}, asyncScriptLimit),
	}, out
}

func TestWatchReportsJSONPerPosting(t *testing.T) {
	watch, out := newTestWatch("added", "updated", "deleted")
	posting := &generated.Posting{Id: 9001, AppUrl: "https://app.hey.com/topics/5511"}

	watch.report(context.Background(), watchEvent{Change: "added", At: "2026-08-18T09:14:22.031Z", PostingID: 9001, New: watch.classify(watch.boxes[24088], *posting)}, watch.boxes[24088], posting)
	watch.report(context.Background(), watchEvent{Change: "deleted", At: "2026-08-18T09:15:00.000Z", PostingID: 9003}, watch.boxes[24088], nil)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want one per change: %q", len(lines), out.String())
	}

	var added watchEvent
	if err := json.Unmarshal([]byte(lines[0]), &added); err != nil {
		t.Fatalf("first line isn't JSON: %v", err)
	}
	if added.Change != "added" || added.PostingID != 9001 || added.ThreadID != 5511 {
		t.Errorf("added = %+v, want posting 9001 on thread 5511", added)
	}
	if added.Box.Kind != "imbox" || added.Box.ID != 24088 {
		t.Errorf("box = %+v, want the imbox", added.Box)
	}
	if added.Posting == nil {
		t.Error("an added posting should carry the posting itself")
	}
	if added.New == nil || *added.New {
		t.Errorf("new = %v, want false for a posting active before the watch began", added.New)
	}

	var deleted watchEvent
	if err := json.Unmarshal([]byte(lines[1]), &deleted); err != nil {
		t.Fatalf("second line isn't JSON: %v", err)
	}
	if deleted.Posting != nil {
		t.Error("a deleted posting is gone, so there's nothing to carry")
	}
	if deleted.ThreadID != 0 {
		t.Errorf("thread = %d, want none for a deleted posting", deleted.ThreadID)
	}
	if strings.Contains(lines[1], `"new"`) {
		t.Errorf("deleted = %s, want no new: a deletion is not mail", lines[1])
	}
}

func TestWatchSkipsChangesItIsntWatching(t *testing.T) {
	watch, out := newTestWatch("added")

	watch.report(context.Background(), watchEvent{Change: "updated", PostingID: 9002}, watch.boxes[24088], nil)

	if out.Len() != 0 {
		t.Errorf("wrote %q, want nothing for a change outside --events", out.String())
	}
}

func TestWatchExitOnFirstReportsOnce(t *testing.T) {
	watch, out := newTestWatch("added")
	watch.exitOnFirst = true

	watch.report(context.Background(), watchEvent{Change: "added", PostingID: 9001}, watch.boxes[24088], nil)
	watch.report(context.Background(), watchEvent{Change: "added", PostingID: 9002}, watch.boxes[24088], nil)

	if lines := strings.Count(out.String(), "\n"); lines != 1 {
		t.Errorf("wrote %d lines, want only the first change", lines)
	}
	if !watch.finished() {
		t.Error("the watch should be finished after its first change")
	}
}

func TestWatchRunsScriptSynchronously(t *testing.T) {
	watch, out := newTestWatch("added")
	watch.syncScript = `printf "%s %s\n" "$HEY_CHANGE" "$HEY_POSTING_ID"; cat`

	watch.report(context.Background(), watchEvent{Change: "added", PostingID: 9001}, watch.boxes[24088], nil)

	if !strings.Contains(out.String(), "added 9001") {
		t.Errorf("script output = %q, want the change in its environment", out.String())
	}
	if !strings.Contains(out.String(), `"kind":"imbox"`) {
		t.Errorf("script output = %q, want the event JSON on its stdin", out.String())
	}
	if watch.lastScriptExit != 0 {
		t.Errorf("exit = %d, want 0", watch.lastScriptExit)
	}
}

func TestWatchKeepsGoingWhenAScriptFails(t *testing.T) {
	watch, _ := newTestWatch("added")
	watch.syncScript = "exit 3"

	watch.report(context.Background(), watchEvent{Change: "added", PostingID: 9001}, watch.boxes[24088], nil)

	if watch.lastScriptExit != 3 {
		t.Errorf("exit = %d, want the script's own 3", watch.lastScriptExit)
	}
	if warning := watch.errOut.(*bytes.Buffer).String(); !strings.Contains(warning, "exited 3") {
		t.Errorf("stderr = %q, want the failure reported", warning)
	}
}

func TestWatchEventEnvironment(t *testing.T) {
	event := watchEvent{
		Change:    "added",
		At:        "2026-08-18T09:14:22.031Z",
		Box:       &watchEventBox{ID: 24088, Kind: "imbox", Name: "Imbox"},
		PostingID: 9001,
		ThreadID:  5511,
	}

	environment := strings.Join(event.environment(), "\n")
	for _, want := range []string{"HEY_CHANGE=added", "HEY_BOX_ID=24088", "HEY_BOX_KIND=imbox", "HEY_BOX_NAME=Imbox", "HEY_POSTING_ID=9001", "HEY_THREAD_ID=5511", "HEY_AT=2026-08-18T09:14:22.031Z"} {
		if !strings.Contains(environment, want) {
			t.Errorf("environment = %q, want %s", environment, want)
		}
	}
	if !strings.Contains(environment, "HEY_NEW=0") {
		t.Errorf("environment = %q, want HEY_NEW=0 when the posting is not new", environment)
	}

	isNew := true
	event.New = &isNew
	if environment := strings.Join(event.environment(), "\n"); !strings.Contains(environment, "HEY_NEW=1") {
		t.Errorf("environment = %q, want HEY_NEW=1 for new mail", environment)
	}
}

func TestWatchReadsChangesWhenNotified(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")

	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A14%3A22.031Z&v=2>; rel="next"`)
		_, _ = w.Write([]byte(`{"added":[{"id":9001,"kind":"topic","box_id":24088,"app_url":"https://app.hey.com/topics/5511"}]}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added", "updated", "deleted")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(requested) != 1 {
		t.Fatalf("requests = %v, want one read of the changes feed", requested)
	}
	if !strings.Contains(requested[0], "since=2026-08-18T09%3A00%3A00.000Z") {
		t.Errorf("requested %q, want the box's own cursor", requested[0])
	}

	var event watchEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &event); err != nil {
		t.Fatalf("output isn't JSON: %q", out.String())
	}
	if event.Change != "added" || event.PostingID != 9001 || event.ThreadID != 5511 {
		t.Errorf("event = %+v, want added posting 9001 on thread 5511", event)
	}
	if watch.boxes[24088].cursor.Since != "2026-08-18T09:14:22.031Z" {
		t.Errorf("cursor = %+v, want it moved to where the feed left off", watch.boxes[24088].cursor)
	}

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":99999}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(requested) != 1 {
		t.Errorf("requests = %v, want none for a box we aren't watching", requested)
	}
}

func TestWatchReadsAgainAfterAFailedRead(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")

	broken := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A14%3A22.031Z&v=2>; rel="next"`)
		_, _ = w.Write([]byte(`{"added":[{"id":9001,"kind":"topic","box_id":24088,"app_url":"https://app.hey.com/topics/5511"}]}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !watch.unread[24088] {
		t.Error("a box whose read failed should be waiting to be read again")
	}
	if watch.retry == nil {
		t.Error("a failed read should arm a retry")
	}
	if watch.backoff != firstWatchRetry {
		t.Errorf("backoff = %s, want the first retry to wait %s", watch.backoff, firstWatchRetry)
	}
	if watch.boxes[24088].cursor.Since != "2026-08-18T09:00:00.000Z" {
		t.Errorf("cursor = %+v, want it left where it was so the retry picks the change up", watch.boxes[24088].cursor)
	}

	broken = false
	if err := watch.readUnreadBoxes(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(watch.unread) != 0 {
		t.Errorf("unread = %v, want the box cleared once it was read", watch.unread)
	}
	if watch.backoff != 0 {
		t.Errorf("backoff = %s, want it reset once every box was read", watch.backoff)
	}
	if !strings.Contains(out.String(), `"posting_id":9001`) {
		t.Errorf("output = %q, want the change the failed read missed", out.String())
	}
}

func TestWatchStopsOnAReadThatCannotWork(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the server was asked for %s, want a read the SDK turns down on its own", r.URL)
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, _ := newTestWatch("added")
	errOut := &bytes.Buffer{}
	watch.errOut = errOut

	// An empty cursor is a usage error on every read: waiting won't fill it in.
	err := watch.readBox(context.Background(), watch.boxes[24088])
	if err == nil {
		t.Fatal("expected a read that can never work to be reported")
	}
	if len(watch.unread) != 0 {
		t.Errorf("unread = %v, want no retry armed for a permanent error", watch.unread)
	}
	if watch.retry != nil {
		t.Error("a permanent error should not arm a retry")
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want the error reported rather than warned about", errOut.String())
	}
}

// boxesAndChanges answers the reads a skip-ahead makes: a changes feed that is too far
// behind to follow, the box list it looks for the box and its feed's version in, and
// HEY's clock, which it skips to.
func boxesAndChanges(t *testing.T, boxes string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/identity.json":
			w.Header().Set("Date", skipDate)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/boxes.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(boxes))
		default:
			w.WriteHeader(http.StatusConflict)
		}
	}))
}

// HEY's clock when a skip-ahead reads it.
const skipDate = "Fri, 21 Aug 2026 11:05:00 GMT"

// wantSkippedToHEYsClock checks a skip-ahead's point: HEY's clock when it answered — the
// millisecond before the Date header, not taken back by the request's time, which a
// skip has no gap to catch in and which could leave a busy feed still behind.
func wantSkippedToHEYsClock(t *testing.T, skippedTo time.Time) {
	t.Helper()
	if want := time.Date(2026, 8, 21, 11, 4, 59, 999000000, time.UTC); !skippedTo.Equal(want) {
		t.Errorf("skipped to %v, want HEY's clock when it answered, %v", skippedTo, want)
	}
}

func TestWatchSkipsAheadToHEYsClock(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := boxesAndChanges(t, `[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T11%3A02%3A00.518496Z&v=2"}]`)
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, _ := newTestWatch("added")
	watch.errOut = &bytes.Buffer{}
	watch.boxes[24088].cursor.Since = "2026-08-01T00:00:00.000Z"

	if err := watch.readBox(context.Background(), watch.boxes[24088]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if watch.boxes[24088] == nil {
		t.Fatal("the box should still be watched")
	}
	floor := watch.newMail.floors[24088]
	wantSkippedToHEYsClock(t, floor)
	if got := watch.boxes[24088].cursor; got.Since != watchStartSince(floor) || got.Version != "2" {
		t.Errorf("cursor = %+v, want HEY's clock at the skip, the new-mail floor, and the box's feed version", got)
	}
}

// skipHEY is HEY as a skip-ahead meets it: a feed read from before 11:00 too far behind
// to follow and one from after it clean, and the box list, the calendar list and the
// clock answering — save the reads fail answers itself, which it says it did by
// returning true.
func skipHEY(t *testing.T, fail func(http.ResponseWriter, *http.Request) bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail(w, r) {
			return
		}
		switch r.URL.Path {
		case "/identity.json":
			w.Header().Set("Date", skipDate)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/boxes.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T11%3A02%3A00.518496Z&v=2"}]`))
		case "/calendars.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"calendars": [{"calendar": {"id": 512, "name": "Household"},
				"recording_changes_url": "/calendars/512/recording/changes.json?since=2026-08-18T11%3A00%3A00.000000Z&v=1"}]}`))
		default:
			answerTooFarBehindBefore(w, r, time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC))
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("HEY_TOKEN", "test-token")
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)
}

// answerTooFarBehindBefore answers a changes feed read as HEY would for a feed that
// changed too much before behind to follow — `head :conflict`, no body — and not at
// all since.
func answerTooFarBehindBefore(w http.ResponseWriter, r *http.Request, behind time.Time) {
	since, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("since"))
	if err != nil || since.Before(behind) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

// behindWatch is a watch whose Imbox and Household calendar have both fallen too far
// behind to follow.
func behindWatch(t *testing.T) (*postingsWatch, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	watch, out := newTestWatch("added", "resync", "calendar_resync")
	errOut := &bytes.Buffer{}
	watch.errOut = errOut
	watch.boxes[24088].cursor.Since = "2026-08-01T00:00:00.000Z"
	watch.calendar = newTestCalendarsWatch(t, &watchedCalendar{id: 512, name: "Household", cursor: hey.CalendarChangesCursor{Since: "2026-08-01T00:00:00.000Z", Version: "1"}})
	return watch, out, errOut
}

// readBehind reads the box or the calendar behindWatch left behind.
func readBehind(ctx context.Context, watch *postingsWatch, feed string) error {
	if feed == "box" {
		return watch.readBox(ctx, watch.boxes[24088])
	}
	return watch.readCalendar(ctx, watch.calendar.calendars[512])
}

// ringFeed rings the doorbell for the box or the calendar behindWatch left behind, and
// answers it the way the watch's loop does.
func ringFeed(t *testing.T, watch *postingsWatch, feed string) {
	t.Helper()
	var err error
	if feed == "box" {
		err = watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`))
	} else {
		watch.calendar.ring(512)
		<-watch.calendar.wake
		err = watch.readRungCalendars(context.Background())
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// listOf is the list a skip-ahead reads for a feed.
func listOf(feed string) string {
	if feed == "box" {
		return "/boxes.json"
	}
	return "/calendars.json"
}

// An interrupt or --timeout while a skip-ahead reads its list or HEY's clock is how a
// watch is meant to end, not a failed read: nothing is warned about, no retry is armed,
// and nothing says it skipped.
func TestWatchSkipAheadEndsQuietlyWhenInterrupted(t *testing.T) {
	for _, feed := range []string{"box", "calendar"} {
		for _, at := range []string{"/identity.json", listOf(feed)} {
			t.Run(feed+at, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				skipHEY(t, func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path != at {
						return false
					}
					cancel()
					<-r.Context().Done()
					return true
				})
				watch, out, errOut := behindWatch(t)

				if err := readBehind(ctx, watch, feed); err != nil {
					t.Fatalf("read = %v, want an interrupted skip-ahead to end quietly", err)
				}
				if errOut.Len() != 0 {
					t.Errorf("stderr = %q, want nothing for an interrupt", errOut.String())
				}
				if watch.retry != nil || len(watch.unread) != 0 || len(watch.calendar.unread) != 0 {
					t.Error("an interrupted skip-ahead should not arm a retry")
				}
				if out.Len() != 0 {
					t.Errorf("wrote %q, want no resync for a skip that did not happen", out.String())
				}
				if watch.boxes[24088].cursor.Since != "2026-08-01T00:00:00.000Z" || watch.calendar.calendars[512].cursor.Since != "2026-08-01T00:00:00.000Z" {
					t.Error("an interrupted skip-ahead should leave the cursor where it was")
				}
			})
		}
	}
}

// A list that fails to read for a while is a reason to try the skip again, not to stop
// watching: the watch warns, keeps the cursor, retries on the backoff, and says it
// skipped only once it has.
func TestWatchSkipAheadRetriesAListThatFailed(t *testing.T) {
	for _, feed := range []string{"box", "calendar"} {
		t.Run(feed, func(t *testing.T) {
			var down atomic.Bool
			var listReads, feedReads atomic.Int32
			down.Store(true)
			skipHEY(t, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.Contains(r.URL.Path, "/changes") {
					feedReads.Add(1)
				}
				if r.URL.Path != listOf(feed) {
					return false
				}
				listReads.Add(1)
				if !down.Load() {
					return false
				}
				http.Error(w, "down for maintenance", http.StatusInternalServerError)
				return true
			})
			watch, out, errOut := behindWatch(t)

			if err := readBehind(context.Background(), watch, feed); err != nil {
				t.Fatalf("read = %v, want a failed list read retried, not the watch ended", err)
			}
			if !strings.Contains(errOut.String(), "warning: could not skip") || strings.Contains(errOut.String(), "notice") {
				t.Errorf("stderr = %q, want a warning and no word of a skip that has not happened", errOut.String())
			}
			if watch.retry == nil || len(watch.unread)+len(watch.calendar.unread) != 1 {
				t.Fatal("a failed list read should be retried on the backoff")
			}
			if out.Len() != 0 {
				t.Errorf("wrote %q, want no resync before the skip", out.String())
			}

			// Doorbells wait for the retry rather than trying the failing list again.
			ringFeed(t, watch, feed)
			ringFeed(t, watch, feed)
			if got := listReads.Load(); got != 1 {
				t.Errorf("read the list %d times, want once — the retry, not a doorbell, tries again", got)
			}

			// The list is back: the retry skips, reads the feed from there, and says so
			// once.
			down.Store(false)
			errOut.Reset()
			if err := watch.retryUnread(context.Background()); err != nil {
				t.Fatalf("retry = %v", err)
			}
			if lines := watchLines(t, out); len(lines) != 1 || !strings.HasSuffix(lines[0]["change"].(string), "resync") {
				t.Errorf("wrote %v, want one resync once the skip happened", lines)
			}
			if !strings.Contains(errOut.String(), "notice: too much changed") {
				t.Errorf("stderr = %q, want the skip announced once it happened", errOut.String())
			}

			// Skipped, the feed follows its doorbells again.
			reads := feedReads.Load()
			ringFeed(t, watch, feed)
			if feedReads.Load() == reads {
				t.Error("a doorbell after the skip should read the feed again")
			}
		})
	}
}

// A feed busier than one skip can outrun answers 409 again straight after the skip. That
// is one recovery: one notice, skips after the first waiting on the retry backoff —
// doubling — rather than following every doorbell, and one resync line, when a clean
// read ends it, so a reader that re-reads on it has missed nothing a later skip passed.
func TestWatchRecoversFromARepeated409Once(t *testing.T) {
	for _, feed := range []string{"box", "calendar"} {
		t.Run(feed, func(t *testing.T) {
			var feedReads atomic.Int32
			var quiet atomic.Bool
			skipHEY(t, func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.Contains(r.URL.Path, "/changes") {
					return false
				}
				feedReads.Add(1)
				if quiet.Load() {
					return false
				}
				w.WriteHeader(http.StatusConflict) // as HEY answers for a feed this busy
				return true
			})
			watch, out, errOut := behindWatch(t)
			ring := func() { ringFeed(t, watch, feed) }

			for range 5 {
				ring()
			}
			if got := feedReads.Load(); got != 2 {
				t.Errorf("read the feed %d times for five doorbells, want twice — the skip's own read, then the rest held for the retry", got)
			}
			if lines := watchLines(t, out); len(lines) != 0 {
				t.Errorf("wrote %v, want the resync kept for the clean read that ends the recovery", lines)
			}
			if got := strings.Count(errOut.String(), "notice: too much changed"); got != 1 {
				t.Errorf("stderr = %q, want the skip announced once", errOut.String())
			}
			if watch.retry == nil || watch.backoff != firstWatchRetry {
				t.Fatalf("backoff = %v, want the repeat held for the first retry", watch.backoff)
			}

			// The retry comes round, as the loop runs it: another 409, another skip,
			// the same recovery, and a longer wait before the next.
			watch.retry = nil
			if err := watch.retryUnread(context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := feedReads.Load(); got != 3 {
				t.Errorf("read the feed %d times, want the retry's read too", got)
			}
			if out.Len() != 0 {
				t.Errorf("wrote %q, want still nothing while the feed stays too busy", out.String())
			}
			if watch.backoff != 2*firstWatchRetry {
				t.Errorf("backoff = %v, want it doubled while the feed stays too busy", watch.backoff)
			}

			// The feed quietens and a clean read ends the recovery with its one resync,
			// at the last skip; the next time it falls behind is a recovery of its own.
			retryClean := func() {
				t.Helper()
				quiet.Store(true)
				watch.retry = nil
				if err := watch.retryUnread(context.Background()); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				quiet.Store(false)
			}
			retryClean()
			if len(watch.unread)+len(watch.calendar.unread) != 0 {
				t.Error("a clean read should leave nothing behind")
			}
			lines := watchLines(t, out)
			if len(lines) != 1 || !strings.HasSuffix(lines[0]["change"].(string), "resync") {
				t.Fatalf("wrote %v, want one resync for the whole recovery", lines)
			}
			skippedTo, err := time.Parse(time.RFC3339Nano, lines[0]["at"].(string))
			if err != nil {
				t.Fatalf("resync at %v: %v", lines[0]["at"], err)
			}
			wantSkippedToHEYsClock(t, skippedTo)
			ring()
			retryClean()
			if lines := watchLines(t, out); len(lines) != 2 {
				t.Errorf("wrote %v, want a second resync for a second recovery", lines)
			}
		})
	}
}

// HEY's ETag for /boxes.json is the box rows, which neither posting activity nor a new
// feed version touches, so a list the SDK revalidates answers 304 with the since the
// watch fell behind from and the version HEY now refuses — and HEY answers 409 for a
// version it no longer speaks as surely as for too many changes. The skip-ahead reads
// the list past the cache: the next read is on HEY's clock and on the new version.
func TestWatchSkipAheadReadsTheBoxListPastTheCache(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	behind := time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC) // a since before this is too far behind
	var mu sync.Mutex
	var notModified, conflicts int
	listed := `[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-01T09%3A00%3A00.000000Z&v=2"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/identity.json":
			w.Header().Set("Date", skipDate)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/boxes.json":
			w.Header().Set("ETag", `W/"boxes-unchanged"`)
			if r.Header.Get("If-None-Match") == `W/"boxes-unchanged"` {
				notModified++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(listed))
		default:
			since, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("since"))
			if since.Before(behind) || r.URL.Query().Get("v") != "3" {
				// As HEY answers: `head :conflict`, no body.
				conflicts++
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-21T11%3A06%3A12.250000Z&v=3>; rel="next"`)
			_, _ = w.Write([]byte(`{"added":[{"id":9004,"kind":"topic","box_id":24088,"name":"Re: Lunch on Thursday?","active_at":"2026-08-21T11:06:12.250Z","created_at":"2026-08-21T11:06:12.250Z","creator":{"name":"Maria Delgado"}}]}`))
		}
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	// The box list as the watch read it at its start, now in the SDK's cache. Then HEY
	// moves the feed to version 3, and the list's rows — its ETag — stay as they were.
	if _, err := sdk.Boxes().List(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mu.Lock()
	listed = `[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T11%3A04%3A58.310442Z&v=3"}]`
	mu.Unlock()

	watch, out := newTestWatch("added", "resync")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-01T09%3A00%3A00.000000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	ringBox(t, watch)

	mu.Lock()
	defer mu.Unlock()
	if notModified != 0 {
		t.Errorf("the skip-ahead read the box list from the cache %d times, want never — a 304 hands back the version HEY refused", notModified)
	}
	if conflicts != 1 {
		t.Errorf("the feed answered 409 %d times, want once — the skip-ahead should get past it", conflicts)
	}
	if got := watch.boxes[24088].cursor.Version; got != "3" {
		t.Errorf("version = %q, want the one HEY speaks now", got)
	}
	lines := watchLines(t, out)
	if len(lines) != 2 || lines[0]["change"] != watchResync || lines[1]["change"] != "added" || lines[1]["posting_id"] != float64(9004) {
		t.Fatalf("wrote %v, want one resync and then the change after it", lines)
	}
	skippedTo, err := time.Parse(time.RFC3339Nano, lines[0]["at"].(string))
	if err != nil {
		t.Fatalf("resync at %v: %v", lines[0]["at"], err)
	}
	wantSkippedToHEYsClock(t, skippedTo)
	if floor := watch.newMail.floors[24088]; watchTime(floor) != lines[0]["at"] {
		t.Errorf("new-mail floor = %v, want the skip point the resync names", floor)
	}
}

func TestWatchReadyWaitsForAFailedCatchUpRead(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	broken := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A14%3A22.031Z&v=2>; rel="next"`)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	// The catch-up's read fails: the box waits for its retry, and so does ready.
	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("wrote %q, want no ready while a box is still behind", out.String())
	}
	if !watch.catchingUp || !watch.unread[24088] {
		t.Fatalf("catchingUp = %v, unread = %v; want the ready owed and the box waiting", watch.catchingUp, watch.unread)
	}

	// The retry reads it: now ready.
	broken = false
	if err := watch.retryUnread(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"change":"ready"`) || watch.catchingUp {
		t.Errorf("wrote %q, want ready once the last box is read", out.String())
	}

	// A later retry with nothing owed announces nothing.
	out.Reset()
	if err := watch.retryUnread(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want no ready when none is owed", out.String())
	}
}

func TestWatchScriptDoesNotInheritAnotherEventsVariables(t *testing.T) {
	t.Setenv("HEY_NEW", "1")
	t.Setenv("HEY_THREAD_ID", "5511")
	t.Setenv("HEY_TOKEN", "kept")
	watch, out := newTestWatch("deleted")
	watch.syncScript = `printf "%s %s %s\n" "${HEY_NEW:-unset}" "${HEY_THREAD_ID:-unset}" "$HEY_TOKEN"`

	watch.report(context.Background(), watchEvent{Change: "deleted", PostingID: 9003}, watch.boxes[24088], nil)

	if got := out.String(); got != "0 unset kept\n" {
		t.Errorf("script saw %q, want HEY_NEW=0 and no inherited thread id, and everything else kept", got)
	}
}

func TestWatchReadyYieldsToADropQueuedDuringTheCatchUp(t *testing.T) {
	server := changesServer(t, `{}`)
	watch, out := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	// The connection dropped while the catch-up was reading: the drop is
	// queued, not yet acted on, and ready must not get ahead of it.
	watch.noteConnection(false)
	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("wrote %q, want no ready with a drop waiting", out.String())
	}

	// The drop and the reconnect drain in order: disconnected, then the
	// reconnect's own catch-up and ready.
	watch.noteConnection(true)
	if err := watch.followConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"change":"disconnected"`) || !strings.Contains(lines[1], `"change":"ready"`) {
		t.Errorf("wrote %q, want disconnected then ready", lines)
	}
}

func TestWatchDoorbellReadPaysTheReadyACatchUpOwed(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	broken := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A14%3A22.031Z&v=2>; rel="next"`)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor
	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The box rings before its retry comes round, and the read works: ready
	// now, not two minutes from now.
	broken = false
	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"change":"ready"`) || watch.catchingUp {
		t.Errorf("wrote %q, want ready once the doorbell read caught the box up", out.String())
	}
}

func TestWatchDropWhileCatchingUpCancelsTheReadyItOwed(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	broken := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A14%3A22.031Z&v=2>; rel="next"`)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The connection drops before the retry: disconnected, and the ready that
	// was owed is not — the reconnect will catch up and announce its own.
	watch.noteConnection(false)
	if err := watch.followConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	broken = false
	if err := watch.retryUnread(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"change":"disconnected"`) {
		t.Errorf("wrote %q, want disconnected alone — no ready while the connection is down", out.String())
	}

	// The reconnect catches up and says ready.
	watch.noteConnection(true)
	if err := watch.followConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"change":"ready"`) {
		t.Errorf("wrote %q, want ready after the reconnect's catch-up", out.String())
	}
}

func TestWatchGivesUpOnABoxThatHasGone(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := boxesAndChanges(t, `[{"id":31145,"kind":"papertrail","name":"The Paper Trail","posting_changes_url":"/boxes/31145/postings/changes.json?since=2026-08-21T11%3A02%3A00.000Z&v=2"}]`)
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added", "resync")
	errOut := &bytes.Buffer{}
	watch.errOut = errOut
	watch.boxes[31145] = &watchedBox{id: 31145, kind: "papertrail", name: "The Paper Trail", reported: true}
	watch.boxes[24088].cursor.Since = "2026-08-01T00:00:00.000Z"

	if err := watch.readBox(context.Background(), watch.boxes[24088]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, watching := watch.boxes[24088]; watching {
		t.Error("a box the server no longer lists should stop being watched")
	}
	if !strings.Contains(errOut.String(), "can no longer be followed") {
		t.Errorf("stderr = %q, want the box's departure reported", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want no resync for a box that is gone — there is nothing to re-read", out.String())
	}

	// Now the last box goes too, and there is nothing left to wait for.
	watch.boxes[31145].cursor.Since = "2026-08-01T00:00:00.000Z"
	server.Close()
	gone := boxesAndChanges(t, `[]`)
	defer gone.Close()
	initSDK(auth.NewManager(gone.URL, gone.Client(), t.TempDir()), gone.URL)

	err := watch.readBox(context.Background(), watch.boxes[31145])
	if err == nil {
		t.Fatal("expected an error once every watched box has gone")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to say the box is gone", err)
	}
}

func TestWatchGivesUpWhenTheLastReportedBoxHasGone(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	// The Feed is still listed, but --box imbox only ever reported the Imbox,
	// and that one is gone: following The Feed for the record alone is a watch
	// that would sit there for good, reporting nothing.
	server := boxesAndChanges(t, `[{"id":24089,"kind":"feedbox","name":"The Feed","posting_changes_url":"/boxes/24089/postings/changes.json?since=2026-08-21T11%3A02%3A00.000Z&v=2"}]`)
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, _ := newTestWatch("added")
	watch.errOut = &bytes.Buffer{}
	watch.boxes[24089] = &watchedBox{id: 24089, kind: "feedbox", name: "The Feed", reported: false}
	watch.boxes[24088].cursor.Since = "2026-08-01T00:00:00.000Z"

	err := watch.readBox(context.Background(), watch.boxes[24088])
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want the watch to give up once no reported box is left", err)
	}
}

func TestWatchClosedSubscriptionIsOnlyFineWhenItWasInterrupted(t *testing.T) {
	watch, _ := newTestWatch("added")

	interrupted, interrupt := context.WithCancel(context.Background())
	interrupt()
	if err := watch.closedError(interrupted, actioncable.ErrUnsubscribed); err != nil {
		t.Errorf("error = %v, want an interrupted watch to end cleanly", err)
	}

	err := watch.closedError(context.Background(), actioncable.ErrClosed)
	if err == nil {
		t.Fatal("a connection that went away for good should be reported")
	}
	if !strings.Contains(err.Error(), "hung up") {
		t.Errorf("error = %q, want it to say the server hung up", err.Error())
	}

	err = watch.closedError(context.Background(), fmt.Errorf("%w: %s", actioncable.ErrRejected, changesChannel))
	if err == nil || !strings.Contains(err.Error(), "turned this subscription down") {
		t.Errorf("error = %v, want a rejected subscription reported as an auth failure", err)
	}

	original := apierr.ErrAuth("the stored session has ended")
	err = watch.closedError(context.Background(), original)
	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth || classified.Message != original.Message {
		t.Errorf("error = %v, want the connection's authentication failure preserved", err)
	}
}

func TestWatchRunsBoundedAsyncScripts(t *testing.T) {
	watch, _ := newTestWatch("added")
	ran := t.TempDir() + "/ran"
	watch.asyncScript = "echo $HEY_POSTING_ID >> " + ran
	// Overlapping scripts share whatever the watch writes to, and a test's buffer isn't
	// the file descriptor they'd be handed in a terminal.
	watch.out, watch.errOut = io.Discard, io.Discard

	for posting := range int64(asyncScriptLimit * 2) {
		watch.report(context.Background(), watchEvent{Change: "added", PostingID: 9000 + posting}, watch.boxes[24088], nil)
		if len(watch.running) > asyncScriptLimit {
			t.Fatalf("%d scripts running at once, want no more than %d", len(watch.running), asyncScriptLimit)
		}
	}

	for range asyncScriptLimit {
		watch.running <- struct{}{}
	}

	lines, err := os.ReadFile(ran)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count := strings.Count(string(lines), "\n"); count != asyncScriptLimit*2 {
		t.Errorf("%d scripts ran, want one per change", count)
	}
}

func TestConnectionChangesQueueInOrderAndNeverBlock(t *testing.T) {
	watch, _ := newTestWatch("added")

	watch.noteConnection(false)
	watch.noteConnection(true)
	watch.noteConnection(false)

	select {
	case <-watch.connection:
	default:
		t.Fatal("a wake-up should be waiting")
	}
	var transitions []bool
	for {
		connected, queued := watch.nextTransition()
		if !queued {
			break
		}
		transitions = append(transitions, connected)
	}
	if !slices.Equal(transitions, []bool{false, true, false}) {
		t.Errorf("transitions = %v, want every change in the order it happened", transitions)
	}
}

func TestWatchReadyYieldsToADropQueuedBehindTheReconnect(t *testing.T) {
	server := changesServer(t, `{}`)
	watch, out := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	// A reconnect and then a drop, both queued before the loop got to them:
	// the reconnect's catch-up must see the drop still waiting behind it.
	watch.noteConnection(true)
	watch.noteConnection(false)
	if err := watch.followConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"change":"disconnected"`) {
		t.Errorf("wrote %q, want disconnected alone — no ready with the connection already down", out.String())
	}

	watch.noteConnection(true)
	if err := watch.followConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"change":"ready"`) {
		t.Errorf("wrote %q, want ready after the reconnect that stuck", out.String())
	}
}

func TestWatchDoesNotSayReadyOnItsWayOut(t *testing.T) {
	server := changesServer(t, `{"added":[{"id":9001,"kind":"topic","box_id":24088,"app_url":"https://app.hey.com/topics/5511"}]}`)

	// The catch-up reported the one change --exit-on-first was waiting for.
	watch, out := newTestWatch("added")
	watch.exitOnFirst = true
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor
	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"posting_id":9001`) || strings.Contains(out.String(), `"change":"ready"`) {
		t.Errorf("wrote %q, want the change and no ready from a watch that is exiting", out.String())
	}

	// The catch-up was interrupted mid-read.
	interrupted, out := newTestWatch("added")
	interrupted.boxes[24088].cursor = cursor
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := interrupted.catchUp(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want nothing from a catch-up cut short by an interrupt", out.String())
	}
}

func TestWatchedBoxesStartAtTheWatchsStart(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The Imbox's last activity is after the watch read HEY's clock — mail
		// landed in between; The Feed's is before, and its feed may still hold
		// changes later than that which are history all the same.
		_, _ = w.Write([]byte(`[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A30.183221Z&v=2"},` +
			`{"id":24089,"kind":"feedbox","name":"The Feed","posting_changes_url":"/boxes/24089/postings/changes.json?since=2026-08-21T08%3A00%3A00.518496Z&v=2"}]`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	command := newWatchCommand()
	boxes, err := command.watchedBoxes(context.Background(), watchStarted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []int64{24088, 24089} {
		if got := boxes[id].cursor; got.Since != "2026-08-21T09:00:00.000Z" || got.Version != "2" {
			t.Errorf("%s cursor = %+v, want the watch's start and HEY's version, whatever since HEY served", boxes[id].name, got)
		}
	}
	if !boxes[24088].reported || !boxes[24089].reported {
		t.Error("without --box every box is reported")
	}

	// --box imbox: every box is still followed, the Imbox alone is reported;
	// a --box that names nothing is not found.
	command.boxes = []string{"imbox"}
	boxes, err = command.watchedBoxes(context.Background(), watchStarted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(boxes) != 2 || !boxes[24088].reported || boxes[24089].reported {
		t.Errorf("boxes = %+v, want both followed and the Imbox alone reported", boxes)
	}
	command.boxes = []string{"trailbox"}
	if _, err := command.watchedBoxes(context.Background(), watchStarted); err == nil {
		t.Error("expected an error when --box names no box")
	}
	command.boxes = nil

	// --since is the reader's choice and wins over the start.
	command.since = "2026-08-21T09:30:00Z"
	boxes, err = command.watchedBoxes(context.Background(), watchStarted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := boxes[24088].cursor.Since; got != "2026-08-21T09:30:00.000Z" {
		t.Errorf("cursor = %q, want --since untouched", got)
	}
}

// heyHistory is HEY as a watch's startup meets it: its clock, the box list, and a
// changes feed that answers what is strictly later than its cursor, the way HEY's does.
// Each box's posting_changes_url carries what HEY puts there — the box's last posting
// activity, the latest updated_at among its unbundled postings, or the box's own
// updated_at when it has none — not the time, and a cached box list serves it as it
// was when cached. So the feed can answer a change later than that cursor that is
// history all the same.
type heyHistory struct {
	mu      sync.Mutex
	cursors map[int64]string
	changes map[int64][]historyChange
}

type historyChange struct {
	at      string
	deleted bool
	id      int64
	subject string
}

// The clock the server answers with; a watch's start is taken a moment before it.
const heyHistoryDate = "Fri, 21 Aug 2026 09:00:05 GMT"

func newHEYHistory(t *testing.T) *heyHistory {
	t.Helper()
	history := &heyHistory{
		cursors: map[int64]string{
			// The Imbox's since as a box list cached before its latest posting serves it.
			24088: "2026-08-20T23:45:00.000000Z",
			// Reply Later is empty, so its cursor is the box's own updated_at — years
			// before the posting deleted from it last week.
			24091: "2020-06-16T11:22:18.853469Z",
		},
		changes: map[int64][]historyChange{
			24088: {{at: "2026-08-20T23:45:39.083350Z", id: 9001, subject: "Lunch on Thursday?"}},
			24091: {{at: "2026-08-19T01:07:15.496840Z", id: 9002, deleted: true}},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(history.serve))
	t.Cleanup(server.Close)
	t.Setenv("HEY_TOKEN", "test-token")
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	return history
}

// land is a change arriving in the Imbox, whose cursor is now its last activity.
func (h *heyHistory) land(change historyChange) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changes[24088] = append(h.changes[24088], change)
	h.cursors[24088] = change.at
}

func (h *heyHistory) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	var boxID int64
	switch {
	case r.URL.Path == "/identity.json":
		w.Header().Set("Date", heyHistoryDate)
		_, _ = w.Write([]byte(`{"id":1}`))
	case r.URL.Path == "/boxes.json":
		_, _ = fmt.Fprintf(w, `[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=%s&v=2"},`+
			`{"id":24091,"kind":"laterbox","name":"Reply Later","posting_changes_url":"/boxes/24091/postings/changes.json?since=%s&v=2"}]`,
			h.cursors[24088], h.cursors[24091])
	case scanBox(r.URL.Path, &boxID):
		h.serveChanges(w, boxID, r.URL.Query().Get("since"))
	default:
		http.NotFound(w, r)
	}
}

func scanBox(path string, boxID *int64) bool {
	_, err := fmt.Sscanf(path, "/boxes/%d/postings/changes.json", boxID)
	return err == nil
}

func (h *heyHistory) serveChanges(w http.ResponseWriter, boxID int64, since string) {
	from, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var added, deleted []string
	var last string
	for _, change := range h.changes[boxID] {
		at, _ := time.Parse(time.RFC3339Nano, change.at)
		if !at.After(from) {
			continue
		}
		last = change.at
		if change.deleted {
			deleted = append(deleted, fmt.Sprintf(`{"id":%d,"deleted_at":%q}`, change.id, change.at))
		} else {
			added = append(added, fmt.Sprintf(`{"id":%d,"kind":"topic","box_id":%d,"name":%q,"created_at":%q,"updated_at":%q,"active_at":%q,"creator":{"name":"Maria Delgado"}}`,
				change.id, boxID, change.subject, change.at, change.at, change.at))
		}
	}
	if last != "" {
		w.Header().Set("Link", fmt.Sprintf(`</boxes/%d/postings/changes.json?since=%s&v=2>; rel="next"`, boxID, last))
	}
	_, _ = fmt.Fprintf(w, `{"added":[%s],"deleted":[%s]}`, strings.Join(added, ","), strings.Join(deleted, ","))
}

// startWatch begins a watch the way run does: HEY's clock, then the boxes, then the
// catch-up.
func startWatch(t *testing.T, command *watchCommand, watch *postingsWatch) {
	t.Helper()
	started := readServerNow(t)
	boxes, err := command.watchedBoxes(context.Background(), started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes = boxes
	watch.newMail = trackNewMail(started)
	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWatchDoesNotReportHistoryAsItStarts(t *testing.T) {
	history := newHEYHistory(t)
	watch, out := newTestWatch(defaultChanges...)
	watch.exitOnFirst = true

	startWatch(t, newWatchCommand(), watch)

	lines := watchLines(t, out)
	if len(lines) != 1 || lines[0]["change"] != "ready" {
		t.Fatalf("wrote %v, want ready alone — a deletion and a posting from before the watch began are history", lines)
	}
	if watch.finished() {
		t.Fatal("--exit-on-first should still be waiting for a change")
	}

	// A reply lands after the watch began: that is the change it was waiting for.
	history.land(historyChange{at: "2026-08-21T09:00:12.250000Z", id: 9003, subject: "Re: Lunch on Thursday?"})
	ringBox(t, watch)

	lines = watchLines(t, out)
	if len(lines) != 2 || lines[1]["change"] != "added" || lines[1]["posting_id"] != float64(9003) || lines[1]["new"] != true {
		t.Errorf("wrote %v, want the reply that landed after the start, new", lines)
	}
	if !watch.finished() {
		t.Error("--exit-on-first should end on the change that landed after the start")
	}
}

func TestWatchReadsMailThatLandedBeforeItReadTheBoxes(t *testing.T) {
	history := newHEYHistory(t)
	// The watch reads HEY's clock a moment before the Date header; this lands on the
	// header's second, after the start and before the box list, so the Imbox's cursor is
	// already past it.
	history.land(historyChange{at: "2026-08-21T09:00:05.000000Z", id: 9003, subject: "Invoice #4021"})
	watch, out := newTestWatch(defaultChanges...)

	startWatch(t, newWatchCommand(), watch)

	lines := watchLines(t, out)
	if len(lines) != 2 || lines[0]["posting_id"] != float64(9003) || lines[0]["new"] != true || lines[1]["change"] != "ready" {
		t.Errorf("wrote %v, want the mail that landed during startup, new, then ready — and no history", lines)
	}
}

// HEY's clock is read to the whole second, so the watch cannot tell a change from the
// Date header's own second that came before it from one that came after, and it reads
// both: skipping one that came after would be worse than repeating one that did not.
// Anything earlier than the second the watch read — less the request's time — is
// behind the start and not reported.
func TestWatchReadsTheWholeSecondHEYsClockWasReadIn(t *testing.T) {
	history := newHEYHistory(t)
	history.land(historyChange{at: "2026-08-21T09:00:03.990000Z", id: 9003, subject: "Invoice #4021"})
	history.land(historyChange{at: "2026-08-21T09:00:05.200000Z", id: 9004, subject: "Re: Invoice #4021"})
	watch, out := newTestWatch(defaultChanges...)

	startWatch(t, newWatchCommand(), watch)

	lines := watchLines(t, out)
	if len(lines) != 2 || lines[0]["posting_id"] != float64(9004) || lines[0]["at"] != "2026-08-21T09:00:05.200Z" || lines[0]["new"] != true || lines[1]["change"] != "ready" {
		t.Errorf("wrote %v, want the change from the Date header's second, with when it happened, and not the one before it", lines)
	}
}

func TestWatchSinceReadsTheHistoryFirst(t *testing.T) {
	newHEYHistory(t)
	command := newWatchCommand()
	command.since = "2026-08-19"
	watch, out := newTestWatch(defaultChanges...)

	startWatch(t, command, watch)

	lines := watchLines(t, out)
	if len(lines) != 3 || lines[0]["posting_id"] != float64(9001) || lines[0]["new"] != false ||
		lines[1]["change"] != "deleted" || lines[1]["posting_id"] != float64(9002) || lines[2]["change"] != "ready" {
		t.Errorf("wrote %v, want both changes since --since, not new, then ready", lines)
	}
}

func TestWatchAnnouncesADropBeforeTheReconnectThatFollowedIt(t *testing.T) {
	server := changesServer(t, `{}`)
	watch, _ := newTestWatch("added")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	// The reconnect completed before the loop got round to the drop: the
	// reader still has to see them in this order, or it ends up offline.
	watch.noteConnection(false)
	watch.noteConnection(true)
	if err := watch.followConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(watch.out.(*bytes.Buffer).String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"change":"disconnected"`) || !strings.Contains(lines[1], `"change":"ready"`) {
		t.Errorf("wrote %q, want disconnected then ready", lines)
	}
}

func TestWatchAnnouncesItselfOnStdoutOnly(t *testing.T) {
	watch, out := newTestWatch("added")
	watch.exitOnFirst = true

	watch.announce(watchReady)
	watch.announce(watchDisconnected)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want ready and disconnected: %q", len(lines), out.String())
	}
	var ready map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &ready); err != nil {
		t.Fatalf("ready isn't JSON: %v", err)
	}
	if ready["change"] != "ready" || ready["at"] == "" {
		t.Errorf("ready = %v, want its change and a time", ready)
	}
	if _, has := ready["box"]; has {
		t.Errorf("ready is about the watch, not a box: %v", ready)
	}
	if _, has := ready["posting_id"]; has {
		t.Errorf("ready has no posting: %v", ready)
	}
	if !strings.Contains(lines[1], `"change":"disconnected"`) {
		t.Errorf("second line = %q, want disconnected", lines[1])
	}
	if watch.finished() {
		t.Error("the watch's own news never counts towards --exit-on-first")
	}

	scripted, scriptOut := newTestWatch("added")
	scripted.syncScript = "cat"
	scripted.announce(watchReady)
	if scriptOut.Len() != 0 {
		t.Errorf("a script runs per change, and ready is not one: %q", scriptOut.String())
	}
}

func TestWatchReportsAResyncWhenAskedFor(t *testing.T) {
	watch, out := newTestWatch("deleted", "resync")
	watch.exitOnFirst = true

	if !watch.report(context.Background(), watchEvent{Change: watchResync, At: "2026-08-21T09:00:00.000Z"}, watch.boxes[24088], nil) {
		t.Fatal("a resync is reported when --events has it")
	}
	if !strings.Contains(out.String(), `"change":"resync"`) || !strings.Contains(out.String(), `"kind":"imbox"`) {
		t.Errorf("wrote %q, want the resync with its box", out.String())
	}
	if !watch.finished() {
		t.Error("a resync is a change, so --exit-on-first counts it")
	}

	// --events new is new mail only: a resync is not, so a script for new
	// mail never runs on one and --exit-on-first never exits on one.
	newOnly, out := newTestWatch("new")
	newOnly.exitOnFirst = true
	if newOnly.report(context.Background(), watchEvent{Change: watchResync, At: "2026-08-21T09:00:00.000Z"}, newOnly.boxes[24088], nil) {
		t.Error("--events new must leave a resync out")
	}
	if out.Len() != 0 || newOnly.finished() {
		t.Errorf("wrote %q, finished %v; want nothing for a resync under --events new", out.String(), newOnly.finished())
	}
}

func TestWatchReportsAResyncAfterSkippingAhead(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/postings/changes") {
			answerTooFarBehindBefore(w, r, time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC))
			return
		}
		w.Header().Set("Date", skipDate)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"` + server.URL + `/boxes/24088/postings/changes.json?since=2026-08-21T12%3A00%3A00.000Z&v=2"}]`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("added", "resync")
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var event watchEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &event); err != nil {
		t.Fatalf("output isn't one JSON line: %q (stderr %q)", out.String(), watch.errOut.(*bytes.Buffer).String())
	}
	if event.Change != watchResync || event.Box == nil || event.Box.ID != 24088 {
		t.Errorf("event = %+v, want a resync for the Imbox", event)
	}
	skippedTo, err := time.Parse(time.RFC3339Nano, event.At)
	if err != nil {
		t.Fatalf("resync at %q: %v", event.At, err)
	}
	wantSkippedToHEYsClock(t, skippedTo)
	if watch.boxes[24088].cursor.Since != watchStartSince(skippedTo) {
		t.Errorf("cursor = %+v, want it moved to HEY's clock at the skip the resync names", watch.boxes[24088].cursor)
	}
}

func TestWatchLineDescribesTheWatchsOwnNews(t *testing.T) {
	line := watchLine(watchEvent{Change: watchReady, At: "2026-08-21T09:00:00.000Z"})
	if !strings.Contains(line, "ready") || !strings.Contains(line, "watching for changes") {
		t.Errorf("line = %q, want ready described without a box", line)
	}
}

// A watch whose credentials the server has ended must stop, not redial. The auth
// failure comes from the CLI's own auth strategy, which the SDK returns untouched,
// so it is not a *hey.Error and the SDK's classifier reads it as a generic API
// error — the kind this retries every two minutes, for as long as the shell service
// keeps restarting it.
func TestWatchDialErrorPreservesAClientAuthenticationFailure(t *testing.T) {
	original := apierr.ErrAuth("the stored session has ended")
	err := watchDialError(fmt.Errorf("opening cable: %w", original))

	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want the original authentication classification", err)
	}
	if classified.Message != original.Message {
		t.Errorf("message = %q, want %q", classified.Message, original.Message)
	}
}

func TestPermanentReadErrorRecognizesACLIAuthFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "cli auth", err: apierr.ErrAuth("not authenticated"), want: true},
		{name: "cli auth wrapped", err: fmt.Errorf("reading changes: %w", apierr.ErrAuth("not authenticated")), want: true},
		{name: "cli usage", err: apierr.ErrUsage("bad cursor"), want: true},
		{name: "cli rate limit", err: apierr.ErrRateLimit(30), want: false},
		{name: "cli network", err: apierr.ErrNetwork(io.EOF), want: false},
		{name: "credential storage unavailable", err: errors.New("keyring is locked"), want: false},
		{name: "sdk auth", err: &hey.Error{Code: hey.CodeAuth, Message: "not authenticated"}, want: true},
		{name: "sdk server error", err: &hey.Error{Code: hey.CodeAPI, Message: "boom"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permanentReadError(tt.err); got != tt.want {
				t.Errorf("permanentReadError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestResolveWatchLabel(t *testing.T) {
	listed := []internalfolders.Label{
		{ID: 789, Name: "agent-trades"},
		{ID: 790, Name: "Receipts"},
	}

	byID, err := resolveWatchLabel(listed, "789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if byID.ID != 789 || byID.Name != "agent-trades" {
		t.Errorf("by ID = %+v, want agent-trades 789", byID)
	}

	byName, err := resolveWatchLabel(listed, "AGENT-TRADES")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if byName.ID != 789 {
		t.Errorf("by name = %+v, want case-insensitive match on 789", byName)
	}

	if _, err := resolveWatchLabel(listed, "missing"); err == nil {
		t.Error("expected not found for an unknown label")
	}
	if _, err := resolveWatchLabel(listed, "  "); err == nil {
		t.Error("expected usage error for a blank --label")
	}
}

func TestLabeling(t *testing.T) {
	labels := []watchEventLabel{{ID: 789, Name: "agent-trades"}, {ID: 12, Name: "Receipts"}}
	if labeling(nil, labels) != nil {
		t.Error("a missing posting cannot match a label")
	}
	if labeling(&generated.Posting{Id: 1}, labels) != nil {
		t.Error("a posting with no folders should not match")
	}

	matched := labeling(&generated.Posting{Id: 1, Folders: []generated.Folder{{Id: 12, Name: "Receipts"}}}, labels)
	if matched == nil || matched.ID != 12 || matched.Name != "Receipts" {
		t.Errorf("matched = %+v, want Receipts", matched)
	}
}

func TestWatchFiltersByLabel(t *testing.T) {
	watch, out := newTestWatch("added", "updated", "deleted", "resync")
	watch.labels = []watchEventLabel{{ID: 789, Name: "agent-trades"}}

	unlabeled := &generated.Posting{Id: 9001, AppUrl: "https://app.hey.com/topics/5511"}
	labeled := &generated.Posting{
		Id:      9002,
		AppUrl:  "https://app.hey.com/topics/5512",
		Folders: []generated.Folder{{Id: 789, Name: "agent-trades"}},
	}

	if watch.report(context.Background(), watchEvent{Change: "added", At: "2026-08-18T09:14:22.031Z", PostingID: 9001}, watch.boxes[24088], unlabeled) {
		t.Error("unlabeled mail should be dropped when --label is set")
	}
	if !watch.report(context.Background(), watchEvent{Change: "added", At: "2026-08-18T09:15:00.000Z", PostingID: 9002}, watch.boxes[24088], labeled) {
		t.Fatal("mail already in the label should be reported")
	}
	if watch.report(context.Background(), watchEvent{Change: "deleted", PostingID: 9003}, watch.boxes[24088], nil) {
		t.Error("a deletion carries no folders, so --label should leave it out")
	}
	if watch.report(context.Background(), watchEvent{Change: watchResync}, watch.boxes[24088], nil) {
		t.Error("a resync carries no folders, so --label should leave it out")
	}

	var event watchEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &event); err != nil {
		t.Fatalf("output isn't JSON: %q", out.String())
	}
	if event.PostingID != 9002 {
		t.Errorf("event = %+v, want labeled posting 9002", event)
	}
	if event.Label == nil || event.Label.ID != 789 || event.Label.Name != "agent-trades" {
		t.Errorf("label = %+v, want agent-trades on the JSON line", event.Label)
	}
}

func TestWatchEventLabelEnvironment(t *testing.T) {
	event := watchEvent{
		Change: "added",
		At:     "2026-08-18T09:14:22.031Z",
		Label:  &watchEventLabel{ID: 789, Name: "agent-trades"},
	}
	environment := strings.Join(event.environment(), "\n")
	for _, want := range []string{"HEY_LABEL_ID=789", "HEY_LABEL_NAME=agent-trades"} {
		if !strings.Contains(environment, want) {
			t.Errorf("environment = %q, want %s", environment, want)
		}
	}
}
