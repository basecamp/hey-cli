package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/auth"
)

var watchStarted = time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

// recordingNotifier is a desktopNotifier whose notify-send is a recorder
// answering with the given toast id, the way `notify-send -p` prints it.
func recordingNotifier(printedID string) (*desktopNotifier, *[][]string) {
	var calls [][]string
	notifier := &desktopNotifier{
		errOut: &bytes.Buffer{},
		send: func(args ...string) (string, error) {
			calls = append(calls, args)
			return printedID, nil
		},
		now:      func() time.Time { return watchStarted.Add(time.Minute) },
		started:  watchStarted,
		boxes:    map[int64]bool{24088: true},
		activeAt: map[int64]time.Time{},
	}
	return notifier, &calls
}

func newPosting(id int64, sender, subject string, activeAt time.Time) generated.Posting {
	return generated.Posting{Id: id, Name: subject, ActiveAt: activeAt, Creator: generated.Contact{Name: sender}}
}

func TestWatchNotifyKeepsTheBacklogQuietButNotMailSinceItBegan(t *testing.T) {
	notifier, calls := recordingNotifier("7\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	before := watchStarted.Add(-time.Hour)
	after := watchStarted.Add(30 * time.Second)

	// A box's first read carries everything since the server's cursor — the
	// box's last activity — which may be hours of backlog, plus mail that
	// arrived while the watch was starting up.
	backlog := newPosting(101, "Maria Delgado", "Lunch on Thursday?", before)
	arrived := newPosting(102, "Northwind Invoicing", "Invoice #4021", after)
	notifier.batch(box, []generated.Posting{backlog, arrived}, []generated.Posting{backlog, arrived})
	if len(*calls) != 1 || !slices.Contains((*calls)[0], "Northwind Invoicing — Invoice #4021") {
		t.Fatalf("mail since the watch began toasts, the backlog does not, sent %v", *calls)
	}

	// The backlog thread read later: recorded by the first batch, so not new.
	read := backlog
	read.Seen = true
	notifier.batch(box, []generated.Posting{read}, []generated.Posting{read})
	if len(*calls) != 1 {
		t.Errorf("a backlog thread read later is not new, sent %v", *calls)
	}
}

func TestWatchNotifyRecordsWhatItDoesNotReport(t *testing.T) {
	notifier, calls := recordingNotifier("7\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	after := watchStarted.Add(30 * time.Second)

	// --events updated: the arrival is observed but not reported, so no toast…
	arrived := newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)
	notifier.batch(box, []generated.Posting{arrived}, nil)
	if len(*calls) != 0 {
		t.Fatalf("an unreported change must not toast, sent %v", *calls)
	}

	// …and its seen flip, which is reported, is not mistaken for new mail.
	read := arrived
	read.Seen = true
	unread := arrived
	notifier.batch(box, []generated.Posting{read}, []generated.Posting{read})
	notifier.batch(box, []generated.Posting{unread}, []generated.Posting{unread})
	if len(*calls) != 0 {
		t.Errorf("a thread known from an unreported change keeps its activity, sent %v", *calls)
	}
}

func TestWatchNotifyToastsTheNotifiedBoxesOnly(t *testing.T) {
	notifier, calls := recordingNotifier("7\n")
	after := watchStarted.Add(30 * time.Second)

	feed := &watchedBox{id: 24089, kind: "feedbox", name: "The Feed"}
	newsletter := newPosting(201, "Weekend Deals", "48 hours only", after)
	notifier.batch(feed, []generated.Posting{newsletter}, []generated.Posting{newsletter})
	if len(*calls) != 0 {
		t.Fatalf("mail in a box --notify does not cover must not toast, sent %v", *calls)
	}

	// Its activity was still recorded: moved to the Imbox, it is not new.
	imbox := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	notifier.batch(imbox, []generated.Posting{newsletter}, []generated.Posting{newsletter})
	if len(*calls) != 0 {
		t.Errorf("a thread moved in from another box is not new mail, sent %v", *calls)
	}
}

func TestWatchNotifiedBoxesDefaultToTheImbox(t *testing.T) {
	watched := map[int64]*watchedBox{
		24088: {id: 24088, kind: "imbox", name: "Imbox"},
		24089: {id: 24089, kind: "feedbox", name: "The Feed"},
	}

	command := newWatchCommand()
	notified, err := command.notifiedBoxes(watched)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !notified[24088] || notified[24089] {
		t.Errorf("notified = %v, want the Imbox alone by default", notified)
	}

	command.notifyBoxes = []string{"The Feed", "24088"}
	notified, err = command.notifiedBoxes(watched)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !notified[24088] || !notified[24089] {
		t.Errorf("notified = %v, want every --notify-box, by name or ID", notified)
	}

	command.notifyBoxes = []string{"trailbox"}
	if _, err := command.notifiedBoxes(watched); err == nil || !strings.Contains(err.Error(), `"trailbox"`) {
		t.Errorf("err = %v, want a refusal naming the box that isn't watched", err)
	}

	command.notifyBoxes = nil
	delete(watched, 24088)
	if _, err := command.notifiedBoxes(watched); err == nil || !strings.Contains(err.Error(), `"imbox"`) {
		t.Errorf("err = %v, want a refusal when the Imbox itself isn't watched", err)
	}
}

func TestWatchNotifyFlagsParse(t *testing.T) {
	command := newWatchCommand()
	if err := command.cmd.ParseFlags([]string{"--notify", "--notify-box", "imbox", "--notify-box", "The Feed"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !command.notify {
		t.Error("--notify should be set")
	}
	if !slices.Equal(command.notifyBoxes, []string{"imbox", "The Feed"}) {
		t.Errorf("notifyBoxes = %v, want both --notify-box values", command.notifyBoxes)
	}
}

func TestWatchNotifyToastsABatchOnce(t *testing.T) {
	notifier, calls := recordingNotifier("42\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	later := watchStarted.Add(30 * time.Second)

	pair := []generated.Posting{
		newPosting(101, "Maria Delgado", "Lunch on Thursday?", later),
		newPosting(102, "Northwind Invoicing", "Invoice #4021", later),
	}
	notifier.batch(box, pair, pair)

	if len(*calls) != 1 {
		t.Fatalf("want one notification for the batch, sent %v", *calls)
	}
	argv := (*calls)[0]
	for flag, value := range map[string]string{"-a": "HEY", "-u": "low"} {
		i := slices.Index(argv, flag)
		if i < 0 || argv[i+1] != value {
			t.Errorf("%s %q missing from %v", flag, value, argv)
		}
	}
	if !slices.Contains(argv, "-p") {
		t.Errorf("-p is how the toast id is learned, missing from %v", argv)
	}
	if slices.Contains(argv, "-r") {
		t.Errorf("no toast to replace yet, must not pass -r: %v", argv)
	}
	if !slices.Contains(argv, "2 new in Imbox") || !slices.Contains(argv, "Maria Delgado, Northwind Invoicing") {
		t.Errorf("headline and senders missing from %v", argv)
	}

	notifier.batch(box, []generated.Posting{newPosting(103, "Sam Whitfield", "Draft agenda for Monday", later)}, []generated.Posting{newPosting(103, "Sam Whitfield", "Draft agenda for Monday", later)})

	if len(*calls) != 2 {
		t.Fatalf("want a second notification, sent %v", *calls)
	}
	argv = (*calls)[1]
	if i := slices.Index(argv, "-r"); i < 0 || argv[i+1] != "42" {
		t.Errorf("the second toast should replace the first via -r 42: %v", argv)
	}
	if !slices.Contains(argv, "Sam Whitfield — Draft agenda for Monday") {
		t.Errorf("a single thread is headlined Sender — Subject: %v", argv)
	}
}

func TestWatchNotifyDoesNotReplaceAStaleToast(t *testing.T) {
	notifier, calls := recordingNotifier("42\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	later := watchStarted.Add(30 * time.Second)

	notifier.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)})
	// A toast id from before a shell restart may now belong to another application.
	notifier.now = func() time.Time { return watchStarted.Add(time.Minute + toastReplaceWindow + time.Second) }
	notifier.batch(box, []generated.Posting{newPosting(102, "Northwind Invoicing", "Invoice #4021", later)}, []generated.Posting{newPosting(102, "Northwind Invoicing", "Invoice #4021", later)})

	if len(*calls) != 2 || slices.Contains((*calls)[1], "-r") {
		t.Errorf("a stale toast id must not be passed as -r, sent %v", *calls)
	}
}

func TestWatchNotifySkipsSeenAndMutedMail(t *testing.T) {
	notifier, calls := recordingNotifier("7\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	later := watchStarted.Add(30 * time.Second)

	seen := newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)
	seen.Seen = true
	muted := newPosting(103, "Weekend Deals", "48 hours only", later)
	muted.Muted = true
	notifier.batch(box, []generated.Posting{seen, muted}, []generated.Posting{seen, muted})

	if len(*calls) != 0 {
		t.Errorf("seen and muted threads must never toast, sent %v", *calls)
	}
}

func TestWatchNotifyToastsNewActivityOnly(t *testing.T) {
	notifier, calls := recordingNotifier("7\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	before := watchStarted.Add(-time.Hour)
	after := watchStarted.Add(30 * time.Second)

	// A thread the watch has never seen, updated without new mail: a seen flip,
	// a move into the box. Its activity predates the watch.
	notifier.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", before)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", before)})
	if len(*calls) != 0 {
		t.Fatalf("an update without new activity must not toast, sent %v", *calls)
	}

	// A reply on it.
	notifier.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)})
	if len(*calls) != 1 || !slices.Contains((*calls)[0], "Maria Delgado — Lunch on Thursday?") {
		t.Fatalf("new activity on a known thread should toast, sent %v", *calls)
	}

	// Marking the reply unseen again: the same activity, no toast.
	notifier.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", after)})
	if len(*calls) != 1 {
		t.Errorf("the same activity must not toast twice, sent %v", *calls)
	}

	// A thread that arrived during the watch, then was read: the added toast
	// recorded its activity, so the seen flip's update is not new.
	arrived := newPosting(102, "Northwind Invoicing", "Invoice #4021", after)
	notifier.batch(box, []generated.Posting{arrived}, []generated.Posting{arrived})
	read := arrived
	read.Seen = true
	notifier.batch(box, []generated.Posting{read}, []generated.Posting{read})
	if len(*calls) != 2 {
		t.Errorf("reading a thread must not toast, sent %v", *calls)
	}
}

func TestWatchNotifyCarriesOmarchyHintsOnlyOnOmarchy(t *testing.T) {
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	later := watchStarted.Add(30 * time.Second)

	plain, sent := recordingNotifier("7\n")
	plain.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)})
	if slices.Contains((*sent)[0], "-h") {
		t.Errorf("without Omarchy the argv must carry no hints: %v", (*sent)[0])
	}

	omarchy, sent := recordingNotifier("7\n")
	omarchy.omarchy = true
	omarchy.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)})
	argv := (*sent)[0]
	if !slices.Contains(argv, "string:omarchy-glyph:"+omarchyBarGlyph) || !slices.Contains(argv, "string:omarchy-exec:"+omarchyFocusCommand) {
		t.Errorf("on Omarchy the toast carries the glyph and the focus exec as hints: %v", argv)
	}
	if i := slices.Index(argv, "string:omarchy-glyph:"+omarchyBarGlyph); argv[i-1] != "-h" {
		t.Errorf("hints are passed with -h: %v", argv)
	}
}

func TestWatchNotifyKeepsMailTextOutOfOptionParsing(t *testing.T) {
	notifier, calls := recordingNotifier("7\n")
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}

	fresh := newPosting(101, "-r Systems Ltd", "--help with the quarterly numbers", watchStarted.Add(time.Second))
	fresh.Summary = "-p please see attached"
	notifier.batch(box, []generated.Posting{fresh}, []generated.Posting{fresh})

	argv := (*calls)[0]
	for _, arg := range argv {
		if strings.HasPrefix(arg, "-") && !slices.Contains([]string{"-a", "-u", "-p", "-r", "-h"}, arg) {
			t.Errorf("mail-derived text must never arrive as an option-looking argument: %q in %v", arg, argv)
		}
	}
	if !slices.Contains(argv, "\u2060-r Systems Ltd — --help with the quarterly numbers") || !slices.Contains(argv, "\u2060-p please see attached") {
		t.Errorf("the text itself must be preserved behind the word joiner: %v", argv)
	}
}

func TestWatchNotifyWarnsAndTriesAgainAfterAFailedSend(t *testing.T) {
	notifier, _ := recordingNotifier("7\n")
	errOut := &bytes.Buffer{}
	notifier.errOut = errOut
	failing := true
	var sent int
	notifier.send = func(args ...string) (string, error) {
		sent++
		if failing {
			return "", errors.New("no notification daemon")
		}
		return "7\n", nil
	}
	box := &watchedBox{id: 24088, kind: "imbox", name: "Imbox"}
	later := watchStarted.Add(30 * time.Second)

	notifier.batch(box, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)}, []generated.Posting{newPosting(101, "Maria Delgado", "Lunch on Thursday?", later)})
	if sent != 1 || !strings.Contains(errOut.String(), "warning: could not send a notification: no notification daemon") {
		t.Fatalf("a failed send is a warning, got %d sends and %q", sent, errOut.String())
	}

	failing = false
	notifier.batch(box, []generated.Posting{newPosting(102, "Northwind Invoicing", "Invoice #4021", later)}, []generated.Posting{newPosting(102, "Northwind Invoicing", "Invoice #4021", later)})
	if sent != 2 {
		t.Errorf("the next batch toasts again, got %d sends", sent)
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

	got := serverNow(context.Background())
	if !got.Equal(time.Date(2026, 8, 21, 9, 0, 5, 0, time.UTC)) {
		t.Errorf("serverNow = %v, want the Date header, whatever the local clock says", got)
	}
	if len(requested) != 1 || !strings.Contains(requested[0], "/identity.json?clock=") {
		t.Errorf("requested %v, want one uncacheable identity request", requested)
	}

	second := serverNow(context.Background())
	if len(requested) != 2 || requested[1] == requested[0] {
		t.Errorf("requested %v, want a fresh request each time, never the cache", requested)
	}
	if !second.Equal(got) {
		t.Errorf("second = %v, want the server's clock again", second)
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

func TestWatchNotifyWithoutNotifySendKeepsWatching(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	errOut := &bytes.Buffer{}

	if notifier := newDesktopNotifier(errOut, watchStarted); notifier != nil {
		t.Error("without notify-send there is nothing to notify with")
	}
	if !strings.Contains(errOut.String(), "notice: --notify needs notify-send (libnotify); watching without notifications") {
		t.Errorf("the user should be told once, got %q", errOut.String())
	}
}

func TestComposeMailToastDropsADuplicateDescription(t *testing.T) {
	posting := generated.Posting{Id: 101, Summary: "Your August invoice is attached.", Creator: generated.Contact{EmailAddress: "billing@example.com"}}

	headline, description := composeMailToast("Imbox", []generated.Posting{posting})

	if headline != "billing@example.com — Your August invoice is attached." {
		t.Errorf("headline = %q, want the sender's address and the summary standing in for the subject", headline)
	}
	if description != "" {
		t.Errorf("description = %q, want none when the summary is already the headline", description)
	}
}

func TestComposeMailToastListsTheFirstSenders(t *testing.T) {
	batch := []generated.Posting{
		newPosting(101, "Maria Delgado", "Lunch on Thursday?", watchStarted),
		newPosting(102, "Northwind Invoicing", "Invoice #4021", watchStarted),
		newPosting(103, "Sam Whitfield", "Draft agenda for Monday", watchStarted),
		newPosting(104, "Priya Raman", "Photos from the offsite", watchStarted),
	}
	batch[0].AlternativeSenderName = "Maria (personal)"

	headline, description := composeMailToast("The Feed", batch)

	if headline != "4 new in The Feed" {
		t.Errorf("headline = %q, want the count and the box", headline)
	}
	if description != "Maria (personal), Northwind Invoicing, Sam Whitfield, …" {
		t.Errorf("description = %q, want the first three senders and an ellipsis", description)
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

func notifyingWatch(t *testing.T, server *httptest.Server, changes ...string) (*postingsWatch, *[][]string) {
	t.Helper()
	watch, _ := newTestWatch(changes...)
	cursor, err := watchCursor(server.URL+"/boxes/24088/postings/changes.json?since=2026-08-21T09%3A00%3A00.000Z&v=2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.boxes[24088].cursor = cursor
	notifier, calls := recordingNotifier("7\n")
	watch.notifier = notifier
	return watch, calls
}

const twoArrivals = `{"added":[
  {"id":9001,"kind":"topic","box_id":24088,"name":"Lunch on Thursday?","active_at":"2026-08-21T09:00:20Z","creator":{"name":"Maria Delgado"}},
  {"id":9002,"kind":"topic","box_id":24088,"name":"Invoice #4021","active_at":"2026-08-21T09:00:25Z","creator":{"name":"Northwind Invoicing"}}
]}`

func TestWatchNotifiesOncePerRead(t *testing.T) {
	server := changesServer(t, twoArrivals)
	watch, calls := notifyingWatch(t, server, "added", "updated", "deleted")

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*calls) != 1 || !slices.Contains((*calls)[0], "2 new in Imbox") {
		t.Errorf("one read is one toast, sent %v", *calls)
	}
	if lines := strings.Count(watch.out.(*bytes.Buffer).String(), "\n"); lines != 2 {
		t.Errorf("wrote %d lines, want the changes reported as usual alongside the toast", lines)
	}
}

func TestWatchNotifiesOnlyWatchedEvents(t *testing.T) {
	server := changesServer(t, twoArrivals,
		`{"updated":[{"id":9001,"kind":"topic","box_id":24088,"name":"Lunch on Thursday?","active_at":"2026-08-21T09:00:20Z","creator":{"name":"Maria Delgado"}}]}`)
	watch, calls := notifyingWatch(t, server, "updated")

	// --events updated: the arrivals are not reported, so not toasted…
	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("--events updated leaves an arrival untoasted, sent %v", *calls)
	}

	// …and a later update of one of them, which is reported, is known not to be new.
	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("an update without new activity must not toast, even on a thread whose arrival was filtered out, sent %v", *calls)
	}
}

func TestWatchNotifiesNothingForDeletions(t *testing.T) {
	server := changesServer(t, `{"deleted":[{"id":9003,"deleted_at":"2026-08-21T09:00:20Z"}]}`)
	watch, calls := notifyingWatch(t, server, "added", "updated", "deleted")

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"removed","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*calls) != 0 {
		t.Errorf("a deletion is not new mail, sent %v", *calls)
	}
}

func TestWatchNotifiesOnlyWhatItReportedBeforeExiting(t *testing.T) {
	server := changesServer(t, twoArrivals)
	watch, calls := notifyingWatch(t, server, "added")
	watch.exitOnFirst = true

	if err := watch.read(context.Background(), actioncable.Message(`{"change":"upsert","box_id":24088}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*calls) != 1 || !slices.Contains((*calls)[0], "Maria Delgado — Lunch on Thursday?") {
		t.Errorf("--exit-on-first reports one change, so the toast is for that one, sent %v", *calls)
	}
}
