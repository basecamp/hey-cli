package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/auth"
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

// boxesAndChanges answers the two reads a skip-ahead makes: a changes feed that is too far
// behind to follow, and the box list it then looks for a fresh cursor in.
func boxesAndChanges(t *testing.T, boxes string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/boxes.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(boxes))
		default:
			w.WriteHeader(http.StatusConflict)
		}
	}))
}

func TestWatchSkipsAheadToTheBoxesOwnCursor(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := boxesAndChanges(t, `[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T11%3A02%3A00.000Z&v=2"}]`)
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
	if got := watch.boxes[24088].cursor.Since; got != "2026-08-21T11:02:00.000Z" {
		t.Errorf("cursor = %q, want the server's current one", got)
	}
	if got := watch.newMail.floors[24088]; !got.Equal(time.Date(2026, 8, 21, 11, 2, 0, 0, time.UTC)) {
		t.Errorf("new-mail floor = %v, want the box's floor at the cursor it skipped to", got)
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

	watch, _ := newTestWatch("added")
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

func TestWatchClosedSubscriptionIsOnlyFineWhenItWasInterrupted(t *testing.T) {
	watch, _ := newTestWatch("added")

	interrupted, interrupt := context.WithCancel(context.Background())
	interrupt()
	if err := watch.closedError(interrupted); err != nil {
		t.Errorf("error = %v, want an interrupted watch to end cleanly", err)
	}

	err := watch.closedError(context.Background())
	if err == nil {
		t.Fatal("a connection that went away for good should be reported")
	}
	if !strings.Contains(err.Error(), "hung up") {
		t.Errorf("error = %q, want it to say the server hung up", err.Error())
	}

	watch.rejected.Store(true)
	err = watch.closedError(context.Background())
	if err == nil || !strings.Contains(err.Error(), "turned this subscription down") {
		t.Errorf("error = %v, want a rejected subscription reported as an auth failure", err)
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

func TestWatchedBoxesStartNoLaterThanTheWatchDid(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The Imbox's last activity is after the watch read HEY's clock — mail
		// landed in between; The Feed's is before.
		_, _ = w.Write([]byte(`[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A30.000Z&v=2"},` +
			`{"id":24089,"kind":"feedbox","name":"The Feed","posting_changes_url":"/boxes/24089/postings/changes.json?since=2026-08-21T08%3A00%3A00.000Z&v=2"}]`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	command := newWatchCommand()
	boxes, err := command.watchedBoxes(context.Background(), watchStarted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := boxes[24088].cursor.Since; got != "2026-08-21T09:00:00.000Z" {
		t.Errorf("Imbox cursor = %q, want it moved back to the watch's start so the mail in between is read", got)
	}
	if got := boxes[24089].cursor.Since; got != "2026-08-21T08:00:00.000Z" {
		t.Errorf("Feed cursor = %q, want the box's own when it is earlier", got)
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

	// --since is the reader's choice and wins over both.
	command.since = "2026-08-21T09:30:00Z"
	boxes, err = command.watchedBoxes(context.Background(), watchStarted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := boxes[24088].cursor.Since; got != "2026-08-21T09:30:00.000Z" {
		t.Errorf("cursor = %q, want --since untouched", got)
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
			// As haystack answers: `head :conflict`, no body.
			w.WriteHeader(http.StatusConflict)
			return
		}
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
	if watch.boxes[24088].cursor.Since != "2026-08-21T12:00:00.000Z" {
		t.Errorf("cursor = %+v, want it moved to the server's current one", watch.boxes[24088].cursor)
	}
}

func TestWatchLineDescribesTheWatchsOwnNews(t *testing.T) {
	line := watchLine(watchEvent{Change: watchReady, At: "2026-08-21T09:00:00.000Z"})
	if !strings.Contains(line, "ready") || !strings.Contains(line, "watching for changes") {
		t.Errorf("line = %q, want ready described without a box", line)
	}
}
