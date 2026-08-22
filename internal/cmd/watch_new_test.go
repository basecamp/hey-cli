package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/auth"
)

var watchStarted = time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

func newPosting(id int64, sender, subject string, activeAt time.Time) generated.Posting {
	return generated.Posting{Id: id, Name: subject, ActiveAt: activeAt, Creator: generated.Contact{Name: sender}}
}

// classifyRead runs one read's worth of postings through the tracker the way readBox
// does — every posting decided against the record from before the read, then the
// whole read recorded — and answers which were new.
func classifyRead(tracker *newMail, postings ...generated.Posting) []int64 {
	return classifyReadOf(tracker, 24088, postings...)
}

func classifyReadOf(tracker *newMail, boxID int64, postings ...generated.Posting) []int64 {
	var fresh []int64
	for _, posting := range postings {
		if tracker.isNew(boxID, posting) {
			fresh = append(fresh, posting.Id)
		}
	}
	tracker.record(postings)
	return fresh
}

func TestNewMailIsSinceTheWatchBeganNotTheBacklog(t *testing.T) {
	tracker := trackNewMail(watchStarted)
	before := watchStarted.Add(-time.Hour)
	after := watchStarted.Add(30 * time.Second)

	// A box's first read carries everything since the server's cursor — the
	// box's last activity — which may be hours of backlog, plus mail that
	// arrived while the watch was starting up.
	backlog := newPosting(101, "Maria Delgado", "Lunch on Thursday?", before)
	arrived := newPosting(102, "Northwind Invoicing", "Invoice #4021", after)
	if fresh := classifyRead(tracker, backlog, arrived); len(fresh) != 1 || fresh[0] != 102 {
		t.Fatalf("new = %v, want the arrival and not the backlog", fresh)
	}

	// The backlog thread read later: recorded by the first read, so not new.
	read := backlog
	read.Seen = true
	if fresh := classifyRead(tracker, read); len(fresh) != 0 {
		t.Errorf("new = %v, want a backlog thread read later left alone", fresh)
	}
}

func TestNewMailIsNeverSeenOrMuted(t *testing.T) {
	tracker := trackNewMail(watchStarted)
	later := watchStarted.Add(30 * time.Second)

	seen := newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)
	seen.Seen = true
	muted := newPosting(103, "Weekend Deals", "48 hours only", later)
	muted.Muted = true

	if fresh := classifyRead(tracker, seen, muted); len(fresh) != 0 {
		t.Errorf("new = %v, want neither a seen nor a muted thread", fresh)
	}
}

func TestNewMailIsNewActivityOnly(t *testing.T) {
	tracker := trackNewMail(watchStarted)
	before := watchStarted.Add(-time.Hour)
	after := watchStarted.Add(30 * time.Second)

	// A thread the watch has never seen, updated without new mail: a seen flip,
	// a move into the box. Its activity predates the watch.
	if fresh := classifyRead(tracker, newPosting(101, "Maria Delgado", "Lunch on Thursday?", before)); len(fresh) != 0 {
		t.Fatalf("new = %v, want an update without new activity left alone", fresh)
	}

	// A reply on it.
	if fresh := classifyRead(tracker, newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)); len(fresh) != 1 {
		t.Fatalf("new = %v, want new activity on a known thread", fresh)
	}

	// Marking the reply unseen again: the same activity, not new twice.
	if fresh := classifyRead(tracker, newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)); len(fresh) != 0 {
		t.Errorf("new = %v, want the same activity not counted twice", fresh)
	}

	// A thread that arrived during the watch, then was read: the arrival
	// recorded its activity, so the seen flip's update is not new.
	arrived := newPosting(102, "Northwind Invoicing", "Invoice #4021", after)
	classifyRead(tracker, arrived)
	read := arrived
	read.Seen = true
	if fresh := classifyRead(tracker, read); len(fresh) != 0 {
		t.Errorf("new = %v, want reading a thread not to count", fresh)
	}
}

func TestNewMailRemembersEveryBox(t *testing.T) {
	tracker := trackNewMail(watchStarted)
	after := watchStarted.Add(30 * time.Second)

	// A newsletter lands in The Feed — new there — and is then moved to the
	// Imbox: the same activity, known from the other box, so not new again.
	newsletter := newPosting(201, "Weekend Deals", "48 hours only", after)
	if fresh := classifyRead(tracker, newsletter); len(fresh) != 1 {
		t.Fatalf("new = %v, want the arrival in The Feed", fresh)
	}
	if fresh := classifyRead(tracker, newsletter); len(fresh) != 0 {
		t.Errorf("new = %v, want a thread moved in from another box left alone", fresh)
	}
}

func TestNewMailAfterASkipAheadIsSinceTheSkip(t *testing.T) {
	tracker := trackNewMail(watchStarted)
	beforeTheWatch := watchStarted.Add(-time.Hour)
	inTheGap := watchStarted.Add(30 * time.Minute)
	afterTheSkip := watchStarted.Add(61 * time.Minute)

	// A thread the watch knows from before the gap.
	known := newPosting(100, "Sam Whitfield", "Draft agenda for Monday", beforeTheWatch)
	classifyRead(tracker, known)

	tracker.skippedTo(24088, hey.PostingChangesCursor{Since: "2026-08-21T10:00:00.000Z", Version: "2"})

	// The known thread got a reply inside the gap — mail the watch missed —
	// and is then moved while still unseen: its activity is at the floor, not
	// new. The resync line was the cue to re-read the box.
	moved := newPosting(100, "Sam Whitfield", "Draft agenda for Monday", inTheGap)
	if fresh := classifyRead(tracker, moved); len(fresh) != 0 {
		t.Errorf("new = %v, want a known thread's gap activity left alone", fresh)
	}
	// A thread that arrived in the gap, then moved or labelled while unseen.
	if fresh := classifyRead(tracker, newPosting(101, "Maria Delgado", "Lunch on Thursday?", inTheGap)); len(fresh) != 0 {
		t.Errorf("new = %v, want an unknown thread active in the gap left alone", fresh)
	}
	// Mail after the cursor the watch skipped to is new, on both kinds.
	if fresh := classifyRead(tracker, newPosting(100, "Sam Whitfield", "Draft agenda for Monday", afterTheSkip), newPosting(102, "Northwind Invoicing", "Invoice #4021", afterTheSkip)); len(fresh) != 2 {
		t.Errorf("new = %v, want mail since the skip on a known and an unknown thread", fresh)
	}
	// The floor is the box's alone: another box measures against the start.
	if fresh := classifyReadOf(tracker, 24089, newPosting(103, "Weekend Deals", "48 hours only", inTheGap)); len(fresh) != 1 {
		t.Errorf("new = %v, want another box unaffected by this one's skip", fresh)
	}

	// A cursor that cannot be read moves nothing.
	tracker.skippedTo(24089, hey.PostingChangesCursor{Since: "later"})
	if _, has := tracker.floors[24089]; has {
		t.Error("an unreadable cursor must not become a floor")
	}
}

func TestNewMailIsDecidedBeforeTheReadIsRecorded(t *testing.T) {
	tracker := trackNewMail(watchStarted)
	after := watchStarted.Add(30 * time.Second)

	// One read carrying a thread twice — added, then updated with a seen flip —
	// decides both against the record from before the read: the arrival is
	// new, the flip is not, and the order within the read does not matter.
	arrived := newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)
	read := arrived
	read.Seen = true
	if fresh := classifyRead(tracker, arrived, read); len(fresh) != 1 || fresh[0] != 101 {
		t.Errorf("new = %v, want the arrival alone", fresh)
	}
}

func TestServerNowReadsTheServersClock(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.String())
		w.Header().Set("Date", "Fri, 21 Aug 2026 09:00:05 GMT")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	date := time.Date(2026, 8, 21, 9, 0, 5, 0, time.UTC)
	got := serverNow(context.Background())
	// The Date header, less the instant the request took: on the server's
	// clock, whatever the local one says, and no later than the header.
	if got.After(date) || date.Sub(got) > time.Second {
		t.Errorf("serverNow = %v, want the Date header translated back to the request's start", got)
	}
	if len(requested) != 1 || !strings.Contains(requested[0], "/identity.json?clock=") {
		t.Errorf("requested %v, want one uncacheable identity request", requested)
	}

	second := serverNow(context.Background())
	if len(requested) != 2 || requested[1] == requested[0] {
		t.Errorf("requested %v, want a fresh request each time, never the cache", requested)
	}
	if second.After(date) || date.Sub(second) > time.Second {
		t.Errorf("second = %v, want the server's clock again", second)
	}
}

func TestCutoffBeforeIsAWholeMillisecondStrictlyBefore(t *testing.T) {
	within := time.Date(2026, 8, 21, 9, 0, 5, 123456789, time.UTC)
	if got := cutoffBefore(within); !got.Equal(time.Date(2026, 8, 21, 9, 0, 5, 122000000, time.UTC)) {
		t.Errorf("cutoffBefore(%v) = %v, want the millisecond before the instant's own", within, got)
	}
	exact := time.Date(2026, 8, 21, 9, 0, 5, 0, time.UTC)
	if got := cutoffBefore(exact); !got.Equal(exact.Add(-time.Millisecond)) {
		t.Errorf("cutoffBefore(%v) = %v, want strictly before even on a boundary", exact, got)
	}

	// So mail in the start's own millisecond is new, and a cursor at that
	// millisecond is moved back to before it.
	tracker := trackNewMail(cutoffBefore(within))
	landed := within.Truncate(time.Millisecond)
	if !tracker.isNew(24088, newPosting(101, "Maria Delgado", "Lunch on Thursday?", landed)) {
		t.Error("mail in the same millisecond as the watch's start is new")
	}
	cursor := noLaterThan(hey.PostingChangesCursor{Since: landed.Format(watchCursorTimeLayout)}, cutoffBefore(within))
	if cursor.Since != "2026-08-21T09:00:05.122Z" {
		t.Errorf("cursor = %q, want it moved back to before the millisecond the mail landed in", cursor.Since)
	}
}

func TestServerNowIsTheClockWhenTheRequestBeganNotWhenItWasAnswered(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	const delay = 300 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Header().Set("Date", "Fri, 21 Aug 2026 09:00:05 GMT")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	got := serverNow(context.Background())

	// Mail that lands while the server is answering is later than the start;
	// a start taken at the Date header would put it before.
	date := time.Date(2026, 8, 21, 9, 0, 5, 0, time.UTC)
	if date.Sub(got) < delay {
		t.Errorf("serverNow = %v, want at least the request's %s before the Date header %v", got, delay, date)
	}
	if got.Nanosecond()%int(time.Millisecond) != 0 {
		t.Errorf("serverNow = %v, want a whole millisecond, the resolution of every cursor and active_at", got)
	}
}

// TestWatchReadsMailThatLandedWhileItReadTheClock is the whole startup window
// end to end: the clock request is slow, a posting lands in the Imbox while the
// server is answering it, so the box's cursor — its last posting activity — is
// earlier than the Date header. The watch's start is translated back to before
// the posting, the cursor is moved back to the start, the catch-up reads the
// posting, and it is new.
func TestWatchReadsMailThatLandedWhileItReadTheClock(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	const delay = 300 * time.Millisecond
	// The Date header is 09:00:05; the posting landed at 09:00:04.900, inside
	// the request; the box's cursor is therefore 09:00:04.900 as well.
	var changesSince []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/identity.json"):
			time.Sleep(delay)
			w.Header().Set("Date", "Fri, 21 Aug 2026 09:00:05 GMT")
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/boxes.json":
			_, _ = w.Write([]byte(`[{"id":24088,"kind":"imbox","name":"Imbox","posting_changes_url":"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A04.900Z&v=2"}]`))
		case strings.Contains(r.URL.Path, "/postings/changes"):
			changesSince = append(changesSince, r.URL.Query().Get("since"))
			w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-21T09%3A00%3A04.900Z&v=2>; rel="next"`)
			_, _ = w.Write([]byte(`{"added":[{"id":9001,"kind":"topic","box_id":24088,"name":"Lunch on Thursday?","active_at":"2026-08-21T09:00:04.900Z","creator":{"name":"Maria Delgado"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	// As run does: the clock, then the boxes, then the catch-up.
	started := serverNow(context.Background())
	command := newWatchCommand()
	boxes, err := command.watchedBoxes(context.Background(), started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch, out := newTestWatch("added")
	watch.boxes = boxes
	watch.newMail = trackNewMail(started)
	if err := watch.catchUp(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	landed := time.Date(2026, 8, 21, 9, 0, 4, 900000000, time.UTC)
	if len(changesSince) != 1 {
		t.Fatalf("changes read %d times, want once", len(changesSince))
	}
	since, err := time.Parse(watchCursorTimeLayout, changesSince[0])
	if err != nil || !since.Before(landed) {
		t.Errorf("read the feed since %q, want a cursor moved back to before the posting landed", changesSince[0])
	}
	lines := watchLines(t, out)
	if len(lines) != 2 || lines[0]["posting_id"] != float64(9001) || lines[0]["new"] != true || lines[1]["change"] != "ready" {
		t.Errorf("wrote %v, want the posting that landed during the clock request, new, and then ready", lines)
	}
}

func TestServerNowFallsBackToTheLocalClock(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	before := time.Now()
	got := serverNow(context.Background())
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("serverNow = %v, want the local clock when the server's can't be read", got)
	}
}

// changesServer serves the changes feed bodies in turn, the last one for every
// read after that.
func changesServer(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := bodies[min(reads, len(bodies)-1)]
		reads++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-21T09%3A00%3A30.000Z&v=2>; rel="next"`)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	t.Setenv("HEY_TOKEN", "test-token")
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)
	return server
}

// watchOf is a watch on the Imbox at the server, begun at watchStarted, reporting
// the given --events.
func watchOf(t *testing.T, server *httptest.Server, events ...string) (*postingsWatch, *bytes.Buffer) {
	t.Helper()
	watch, out := newTestWatch(events...)
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor
	return watch, out
}

func ringBox(t *testing.T, watch *postingsWatch) {
	t.Helper()
	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func watchLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		lines = append(lines, event)
	}
	return lines
}

// A backlog thread and an arrival since the watch began, in one read.
const backlogAndArrival = `{"added":[
  {"id":9001,"kind":"topic","box_id":24088,"name":"Lunch on Thursday?","active_at":"2026-08-21T08:00:00Z","creator":{"name":"Maria Delgado"}},
  {"id":9002,"kind":"topic","box_id":24088,"name":"Invoice #4021","active_at":"2026-08-21T09:00:25Z","creator":{"name":"Northwind Invoicing"}}
]}`

func TestWatchSaysWhichPostingsAreNew(t *testing.T) {
	server := changesServer(t, backlogAndArrival, `{"deleted":[{"id":9003,"deleted_at":"2026-08-21T09:00:40Z"}]}`)
	watch, out := watchOf(t, server, "added", "updated", "deleted")

	ringBox(t, watch)
	ringBox(t, watch)

	lines := watchLines(t, out)
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want both arrivals and the deletion: %q", len(lines), out.String())
	}
	if lines[0]["new"] != false || lines[1]["new"] != true {
		t.Errorf("new = %v, %v; want the backlog thread not new and the arrival new", lines[0]["new"], lines[1]["new"])
	}
	if _, has := lines[2]["new"]; has {
		t.Errorf("a deletion is not mail, so it carries no new: %v", lines[2])
	}
}

func TestWatchEventsNewReportsNewMailAlone(t *testing.T) {
	server := changesServer(t, backlogAndArrival,
		`{"updated":[{"id":9002,"kind":"topic","box_id":24088,"name":"Invoice #4021","seen":true,"active_at":"2026-08-21T09:00:25Z","creator":{"name":"Northwind Invoicing"}}]}`,
		`{"updated":[{"id":9002,"kind":"topic","box_id":24088,"name":"Invoice #4021","active_at":"2026-08-21T09:01:00Z","creator":{"name":"Northwind Invoicing"}}]}`)
	watch, out := watchOf(t, server, "new")

	ringBox(t, watch) // the backlog and the arrival: the arrival alone is new
	ringBox(t, watch) // the arrival read: an update, not new
	ringBox(t, watch) // a reply on it: new again

	lines := watchLines(t, out)
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want the arrival and the reply: %q", len(lines), out.String())
	}
	if lines[0]["change"] != "added" || lines[0]["posting_id"] != float64(9002) || lines[0]["new"] != true {
		t.Errorf("first = %v, want the arrival, new", lines[0])
	}
	if lines[1]["change"] != "updated" || lines[1]["new"] != true {
		t.Errorf("second = %v, want the reply, new", lines[1])
	}
}

func TestWatchEventsIsAUnion(t *testing.T) {
	server := changesServer(t, backlogAndArrival,
		`{"updated":[{"id":9001,"kind":"topic","box_id":24088,"name":"Lunch on Thursday?","active_at":"2026-08-21T09:01:00Z","creator":{"name":"Maria Delgado"}}]}`)
	watch, out := watchOf(t, server, "deleted", "new")

	ringBox(t, watch) // --events deleted,new: the backlog arrival is neither, the new one is new
	ringBox(t, watch) // a reply on the backlog thread: new

	lines := watchLines(t, out)
	if len(lines) != 2 || lines[0]["posting_id"] != float64(9002) || lines[1]["posting_id"] != float64(9001) {
		t.Errorf("wrote %v, want the new arrival and then the reply, nothing else", lines)
	}
}

func TestWatchRecordsWhatEventsFiltersOut(t *testing.T) {
	server := changesServer(t, backlogAndArrival,
		`{"updated":[{"id":9002,"kind":"topic","box_id":24088,"name":"Invoice #4021","active_at":"2026-08-21T09:00:25Z","creator":{"name":"Northwind Invoicing"}}]}`)
	watch, out := watchOf(t, server, "updated")

	ringBox(t, watch) // --events updated: the arrivals are not reported…
	ringBox(t, watch) // …and a later update of one of them is known not to be new

	lines := watchLines(t, out)
	if len(lines) != 1 || lines[0]["change"] != "updated" || lines[0]["new"] != false {
		t.Errorf("wrote %v, want one update, not new — the arrival was recorded though unreported", lines)
	}
}

func TestWatchExitOnFirstWithEventsNewWaitsForNewMail(t *testing.T) {
	server := changesServer(t, backlogAndArrival)
	watch, out := watchOf(t, server, "new")
	watch.exitOnFirst = true

	ringBox(t, watch)

	lines := watchLines(t, out)
	if len(lines) != 1 || lines[0]["posting_id"] != float64(9002) {
		t.Errorf("wrote %v, want the first new posting alone — not the backlog thread before it", lines)
	}
	if !watch.finished() {
		t.Error("the watch should be finished on the first new posting")
	}
}

func TestWatchScriptSeesNewMail(t *testing.T) {
	server := changesServer(t, backlogAndArrival)
	watch, out := watchOf(t, server, "added")
	watch.syncScript = `printf "%s new=%s\n" "$HEY_POSTING_ID" "${HEY_NEW:-0}"`

	ringBox(t, watch)

	if got := out.String(); got != "9001 new=0\n9002 new=1\n" {
		t.Errorf("scripts saw %q, want HEY_NEW=1 for the arrival alone", got)
	}
}

func TestWatchLineMarksNewMail(t *testing.T) {
	isNew, notNew := true, false
	box := &watchEventBox{ID: 24088, Kind: "imbox", Name: "Imbox"}
	posting := &generated.Posting{Summary: "Lunch on Thursday?", Creator: generated.Contact{Name: "Maria Delgado"}}

	if line := watchLine(watchEvent{Change: "added", At: "2026-08-21T09:00:25.000Z", Box: box, New: &isNew, Posting: posting}); !strings.Contains(line, "added (new)") {
		t.Errorf("line = %q, want the change marked new", line)
	}
	if line := watchLine(watchEvent{Change: "updated", At: "2026-08-21T09:00:25.000Z", Box: box, New: &notNew, Posting: posting}); strings.Contains(line, "(new)") {
		t.Errorf("line = %q, want no mark on a posting that is not new", line)
	}
}
