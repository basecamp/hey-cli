package cmd

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
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
	activeAt map[int64]time.Time
}

// trackNewMail starts the record. started is the moment before which activity
// is backlog, on the server's clock, since that is the clock every posting's
// active_at is on.
func trackNewMail(started time.Time) *newMail {
	return &newMail{started: started, activeAt: map[int64]time.Time{}}
}

// serverNow is HEY's clock, read off the Date header of one cheap request, so
// that the cutoff between backlog and new mail sits on the same clock as every
// posting's active_at and a workstation running fast or slow can neither call
// the backlog new nor sit on new mail. Date is whole seconds, rounded down,
// which errs towards calling mail a moment old new rather than mail a moment
// new old. The SDK caches GETs by URL, so a query the server ignores keeps
// this one out of the cache; and when the server's clock can't be read, the
// local one stands in.
func serverNow(ctx context.Context) time.Time {
	now := time.Now()
	response, err := rootSDK.Get(ctx, "/identity.json?clock="+strconv.FormatInt(now.UnixNano(), 10))
	if err != nil || response == nil || response.FromCache {
		return now
	}
	if at, err := http.ParseTime(response.Headers.Get("Date")); err == nil {
		return at
	}

	return now
}

// isNew says whether a posting is new mail: unseen, not muted, and active since
// this watch last saw the thread — or since the watch began, for a thread it
// has no record of, because anything active before that was already there: the
// backlog a box's first read carries from the server's cursor, or a thread that
// merely moved in. active_at moves on new mail only, not when a thread is read,
// muted or moved, so none of those is new and a reply on a known thread is.
func (n *newMail) isNew(posting generated.Posting) bool {
	last, known := n.activeAt[posting.Id]
	if !known {
		last = n.started
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
