package cmd

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// New mail is a watch event: every added and updated line says whether the
// posting is new, and --events new selects the ones that are. What counts as
// new needs HEY's semantics and state across events, which is why it is decided
// here, once, rather than by every script and widget that reads the stream —
// what to do about it (a toast, a bell, a re-read) is the reader's.
//
// There is no state file: new means active since the watch began, so the
// backlog --since reads is not new, and what the watch remembers —
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
// to re-read the box either way. HEY writes the cursor to the microsecond, so
// it is read as RFC 3339 with any fraction rather than in the watch's own
// millisecond layout, which refuses it.
func (n *newMail) skippedTo(boxID int64, cursor hey.PostingChangesCursor) {
	if at, err := time.Parse(time.RFC3339Nano, cursor.Since); err == nil {
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
// cache. The start is handed out as a cutoff: a whole millisecond, strictly
// before the instant it stands for.
//
// A watch that cannot read HEY's clock does not start. The workstation's clock
// is no stand-in: every feed starts at the start (watchStartSince) and new mail
// is measured against it, so a fast clock would put both in HEY's future —
// changes unread and new mail called old until the clocks met — and a slow one
// would report history. A request that fails here would fail at the box list
// next anyway.
func serverNow(ctx context.Context) (time.Time, error) {
	started := time.Now()
	response, err := rootSDK.Get(ctx, "/identity.json?clock="+strconv.FormatInt(started.UnixNano(), 10))
	if err != nil {
		return time.Time{}, apierr.FromSDK(err)
	}
	if response == nil || response.FromCache {
		return time.Time{}, apierr.ErrAPI(0, "could not read HEY's clock — the watch needs it to tell what happened after it began")
	}
	at, err := http.ParseTime(response.Headers.Get("Date"))
	if err != nil {
		return time.Time{}, apierr.ErrAPI(0, "HEY's answer carried no Date header — the watch needs HEY's clock to tell what happened after it began")
	}

	return cutoffBefore(at.Add(-time.Since(started))), nil
}

// watchStartSince is where a feed's first read begins without --since: the
// watch's start, in place of the since HEY's changes URL carries. That since is
// not HEY's clock. A box's is its last posting activity — the latest updated_at
// among its unbundled postings, or the box's own when it has none — and a
// calendar's is its updated_at, the list's the latest of them; the feeds answer
// changes later than that which are history by now: a deletion, a bundled
// posting, a calendar deleted after the rest last changed. Nor is it always
// even that: the box list comes through the SDK's ETag cache, HEY's ETag for it
// is the box rows, and posting activity does not touch them, so a 304 hands
// back the since as it stood when the list was cached — hours or days behind.
// Read from there, the catch-up reported what came after as news on every
// start, and --exit-on-first stopped on the first of it.
//
// From the start a feed reports what happened after it and nothing before —
// including mail that landed after the watch read HEY's clock and before it
// read the box list, which a since later than the start would leave behind it,
// read by nothing; that mail is new, too.
func watchStartSince(started time.Time) string {
	return started.UTC().Format(watchCursorTimeLayout)
}

// cutoffBefore makes an instant usable as the watch's start: a cursor is
// milliseconds and so is every active_at, the feed answers what is strictly
// later than its cursor, and isNew asks whether activity is strictly later
// than the start — so the start has to be a whole millisecond, and the one
// before the instant, or mail in the instant's own millisecond would be
// neither read nor new.
func cutoffBefore(at time.Time) time.Time {
	return at.Truncate(time.Millisecond).Add(-time.Millisecond)
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

// record keeps a thread's latest activity, new or not, reported or not. A
// posting is classified and then recorded, so the same activity read twice —
// or carried twice by one read — is new once.
func (n *newMail) record(posting generated.Posting) {
	n.activeAt[posting.Id] = posting.ActiveAt
}
