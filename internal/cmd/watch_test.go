package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if !changes["added"] || !changes["updated"] || !changes["deleted"] {
		t.Errorf("changes = %v, want every change by default", changes)
	}

	command.events = []string{"Added", " deleted"}
	changes, err = command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes["added"] || !changes["deleted"] || changes["updated"] {
		t.Errorf("changes = %v, want added and deleted only", changes)
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
		boxes:   map[int64]*watchedBox{24088: {id: 24088, kind: "imbox", name: "Imbox"}},
		changes: watched,
		out:     out,
		errOut:  &bytes.Buffer{},
		catchUp: make(chan struct{}, 1),
	}, out
}

func TestWatchReportsJSONPerPosting(t *testing.T) {
	watch, out := newTestWatch("added", "updated", "deleted")
	posting := &generated.Posting{Id: 9001, AppUrl: "https://app.hey.com/topics/5511"}

	watch.report(context.Background(), watchEvent{Change: "added", At: "2026-08-18T09:14:22.031Z", PostingID: 9001}, watch.boxes[24088], posting)
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
		Box:       watchEventBox{ID: 24088, Kind: "imbox", Name: "Imbox"},
		PostingID: 9001,
		ThreadID:  5511,
	}

	environment := strings.Join(event.environment(), "\n")
	for _, want := range []string{"HEY_CHANGE=added", "HEY_BOX_ID=24088", "HEY_BOX_KIND=imbox", "HEY_BOX_NAME=Imbox", "HEY_POSTING_ID=9001", "HEY_THREAD_ID=5511", "HEY_AT=2026-08-18T09:14:22.031Z"} {
		if !strings.Contains(environment, want) {
			t.Errorf("environment = %q, want %s", environment, want)
		}
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

func TestAskForCatchUpNeverBlocks(t *testing.T) {
	watch, _ := newTestWatch("added")

	watch.askForCatchUp()
	watch.askForCatchUp()

	select {
	case <-watch.catchUp:
	default:
		t.Fatal("a catch-up should be waiting")
	}
}
