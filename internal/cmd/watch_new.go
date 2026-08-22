package cmd

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// New mail is a watch event: every added and updated line says whether the
// posting is new, and --events new selects the ones that are. What counts as
// new needs HEY's semantics and state across events, which is why it is decided
// here, once, rather than by every script and widget that reads the stream —
// what to do about it (a toast, a bell, a re-read) is the reader's.
//
// There is no state file: new means active since the watch began, so the
// backlog a box's first read carries is not new, and what the watch remembers —
// when each thread was last active — lives and dies with it.

// newMail keeps up with every thread the watch reads, in every box and whatever
// --events reports, so a thread moved into a box, or known from a change that
// was filtered out, is not mistaken for new when its next change comes.
type newMail struct {
	started  time.Time
	floors   map[int64]time.Time
	activeAt map[int64]time.Time
}

// trackNewMail starts the record. started is the moment before which activity
// is backlog, on the server's clock, since that is the clock every posting's
// active_at is on.
func trackNewMail(started time.Time) *newMail {
	return &newMail{started: started, floors: map[int64]time.Time{}, activeAt: map[int64]time.Time{}}
}

// skippedTo sets a box's floor at the cursor the watch skipped ahead to. A box
// that changed more than the feed can list was never read across that gap, so
// whatever was active in it is mail the watch missed: a thread it knows, or
// one it does not, updated later while still unseen — moved, say — would
// otherwise measure its gap activity against an older record and read as
// new. Activity at or before the floor is never new in that box; the cursor is
// the box's last posting activity, which bounds every thread in it. The floor
// is the box's alone — a gap thread that moves to another box is measured
// there, and may still read as new once. The resync line is the reader's cue
// to re-read the box either way.
func (n *newMail) skippedTo(boxID int64, cursor hey.PostingChangesCursor) {
	if at, err := time.Parse(watchCursorTimeLayout, cursor.Since); err == nil {
		n.floors[boxID] = at
	}
}

// serverNow is HEY's clock at the moment the watch began, read off the Date
// header of one cheap request, so that the cutoff between backlog and new mail
// sits on the same clock as every posting's active_at and a workstation running
// fast or slow can neither call the backlog new nor sit on new mail.
//
// Date is the server's clock when it answered, and the watch began when it
// asked: mail that lands in between is later than the start but no later than
// Date, and a start taken at Date would leave it behind a box's cursor, read by
// nothing. So the answer is translated back to the request's start by the time
// the request took — the local monotonic clock, which a wrong wall clock does
// not touch — and a slow request, or one the SDK retried, only moves the start
// earlier. Date is whole seconds, rounded down, which errs the same way: towards
// calling mail a moment old new rather than mail a moment new old. The SDK
// caches GETs by URL, so a query the server ignores keeps this one out of the
// cache; and when the server's clock can't be read, the local clock at the
// start stands in.
func serverNow(ctx context.Context) time.Time {
	started := time.Now()
	response, err := rootSDK.Get(ctx, "/identity.json?clock="+strconv.FormatInt(started.UnixNano(), 10))
	if err != nil || response == nil || response.FromCache {
		return started
	}
	if at, err := http.ParseTime(response.Headers.Get("Date")); err == nil {
		return at.Add(-time.Since(started))
	}

	return started
}

// isNew says whether a posting is new mail: unseen, not muted, and active since
// this watch last saw the thread — or since the watch began, for a thread it
// has no record of, because anything active before that was already there: the
// backlog a box's first read carries from the server's cursor, or a thread that
// merely moved in. active_at moves on new mail only, not when a thread is read,
// muted or moved, so none of those is new and a reply on a known thread is.
func (n *newMail) isNew(boxID int64, posting generated.Posting) bool {
	last, known := n.activeAt[posting.Id]
	if !known {
		last = n.started
	}
	if floor, ok := n.floors[boxID]; ok && floor.After(last) {
		last = floor
	}

	return !posting.Seen && !posting.Muted && posting.ActiveAt.After(last)
}

// record keeps every thread's latest activity, new or not, reported or not.
// A batch is classified before it is recorded, so the record a posting is
// measured against is the one from before its own read.
func (n *newMail) record(postings []generated.Posting) {
	for _, posting := range postings {
		n.activeAt[posting.Id] = posting.ActiveAt
	}
}
