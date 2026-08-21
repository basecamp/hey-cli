// Package threadload reads a thread — its entries and, when asked, each entry's
// message — within limits it states up front, and says exactly what it got.
//
// HEY serves a thread's entries newest first, a page at a time, and keeps each body on
// the message rather than the entry, so a full read is one page walk and then one
// request per entry. Every one of those is a request a server of somebody else's
// choosing answers, so each has a cap: on pages, on entries, on message requests, on
// bytes kept, on time. Nothing here decides what to do when a cap is hit; the result
// records it per entry and the caller decides, which is what keeps `hey threads
// --count` from reading bodies it will not show and `hey attachments` reading bodies it
// cannot do without.
package threadload

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// Source is what a thread is read through: the entries index and each entry's message.
// The SDK satisfies it through a small adapter; tests satisfy it directly.
type Source interface {
	EntriesPage(ctx context.Context, topicID int64, cursor string) (Page, error)
	Message(ctx context.Context, entryID int64) (*generated.Message, error)
}

// Page is one page of the entries index, newest first, and the cursor for the next one
// — empty on the last page.
type Page struct {
	Entries []generated.Entry
	Next    string
}

// Limits bounds one load. Each is a hard number rather than a knob: the defaults are
// what the CLI runs with, and a caller that needs less can lower them.
type Limits struct {
	// MaxPages is how many pages of the index are read, counting the first.
	MaxPages int
	// MaxEntries is how many entries are admitted, newest first.
	MaxEntries int
	// MaxMessageRequests is how many messages are requested, newest first; entries
	// past it are over_limit.
	MaxMessageRequests int
	// MaxRetries is how many more times a failed message request is tried.
	MaxRetries int
	// Concurrency is how many message requests are in flight at once.
	Concurrency int
	// MaxRetainedBytes is how much message content is kept in total; an entry whose
	// content would exceed it is over_limit.
	MaxRetainedBytes int64
	// Deadline is how long the whole load may take; entries not yet requested when it
	// passes are over_limit.
	Deadline time.Duration
}

// DefaultLimits are what the CLI reads with: a hundred pages past the first, two
// thousand entries and as many bodies, two tries per body on top of the SDK's own
// retries, eight at a time, 64 MiB of content kept, two minutes in all.
var DefaultLimits = Limits{
	MaxPages:           101,
	MaxEntries:         2000,
	MaxMessageRequests: 2000,
	MaxRetries:         1,
	Concurrency:        8,
	MaxRetainedBytes:   64 << 20,
	Deadline:           2 * time.Minute,
}

// Request is one thread to read, and whether its bodies are wanted.
type Request struct {
	TopicID int64
	// Hydrate asks for each entry's message. Without it every entry is not_requested,
	// which is what a count or a list of IDs needs.
	Hydrate bool
	Limits  Limits
}

// State is what became of one entry's body.
type State string

const (
	// StateHydrated: the message was read and carries content.
	StateHydrated State = "hydrated"
	// StateBodyless: the message was read and HEY served no content for it.
	StateBodyless State = "bodyless"
	// StateNotRequested: the caller did not ask for bodies.
	StateNotRequested State = "not_requested"
	// StateOverLimit: a limit — requests, bytes, time — was reached before this entry's
	// message was read.
	StateOverLimit State = "over_limit"
	// StateFailed: the message request failed, retries included.
	StateFailed State = "failed"
)

// Entry is one entry of the thread with whatever was read for it.
type Entry struct {
	Entry   generated.Entry
	Message *generated.Message
	State   State
	// Err is why a failed entry failed.
	Err error
}

// Thread is what a load produced: the entries admitted, oldest first, and an account of
// what is missing.
type Thread struct {
	Entries []Entry
	// IndexTruncated reports that the index had more pages than MaxPages, or more
	// entries than MaxEntries, so entries older than the last one here exist unseen.
	IndexTruncated bool
	// Omitted counts the entries whose body was wanted and not read: over_limit or
	// failed.
	Omitted int
}

// Complete reports whether the caller got everything it asked for.
func (t *Thread) Complete() bool {
	return !t.IndexTruncated && t.Omitted == 0
}

// Load reads one thread. An error is a thread that could not be read at all — the
// first page failed, or the index failed part way, since an index with a hole in it
// is not a thread. A body that could not be read is an entry in the failed state, not
// an error.
func Load(ctx context.Context, source Source, request Request) (*Thread, error) {
	limits := request.Limits
	if limits.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limits.Deadline)
		defer cancel()
	}

	entries, truncated, err := readIndex(ctx, source, request.TopicID, limits)
	if err != nil {
		return nil, err
	}

	thread := &Thread{Entries: make([]Entry, len(entries)), IndexTruncated: truncated}
	for i, entry := range entries {
		thread.Entries[i] = Entry{Entry: entry, State: StateNotRequested}
	}
	if request.Hydrate {
		hydrate(ctx, source, thread, limits)
	}

	// Admission is newest first, which is the order the index serves; reading order
	// is oldest first, which is the one reversal.
	slices.Reverse(thread.Entries)
	return thread, nil
}

// readIndex walks the entries index newest first until it ends or a limit is reached.
func readIndex(ctx context.Context, source Source, topicID int64, limits Limits) (entries []generated.Entry, truncated bool, err error) {
	cursor := ""
	for page := 0; ; page++ {
		if page >= limits.MaxPages {
			return entries, true, nil
		}
		result, err := source.EntriesPage(ctx, topicID, cursor)
		if err != nil {
			if page == 0 {
				return nil, false, err
			}
			return nil, false, fmt.Errorf("thread %d: entries page %d: %w", topicID, page+1, err)
		}
		for _, entry := range result.Entries {
			if len(entries) >= limits.MaxEntries {
				return entries, true, nil
			}
			entries = append(entries, entry)
		}
		// An empty page ends the index whatever cursor came with it.
		if result.Next == "" || len(result.Entries) == 0 {
			return entries, false, nil
		}
		cursor = result.Next
	}
}

// hydrate reads each entry's message, newest first, Concurrency at a time, within the
// request, byte and time limits. Entries past a limit are over_limit; a request that
// fails after its retries leaves its entry failed.
func hydrate(ctx context.Context, source Source, thread *Thread, limits Limits) {
	budget := &byteBudget{remaining: limits.MaxRetainedBytes}
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(max(limits.Concurrency, 1))
	for i := range thread.Entries {
		entry := &thread.Entries[i]
		if i >= limits.MaxMessageRequests {
			entry.State = StateOverLimit
			continue
		}
		group.Go(func() error {
			// Nothing returned here is an error: the group's context ending is the
			// deadline, and an entry it reaches is over the limit, not failed.
			if groupCtx.Err() != nil {
				entry.State = StateOverLimit
				return nil //nolint:nilerr // the deadline is a state, not a failure
			}
			message, err := readMessage(groupCtx, source, entry.Entry.Id, limits.MaxRetries)
			switch {
			case err != nil && groupCtx.Err() != nil:
				entry.State = StateOverLimit
			case err != nil:
				entry.State, entry.Err = StateFailed, err
			case !budget.admit(int64(len(message.Content))):
				entry.State = StateOverLimit
			case message.Content == "":
				entry.Message, entry.State = message, StateBodyless
			default:
				entry.Message, entry.State = message, StateHydrated
			}
			return nil
		})
	}
	_ = group.Wait()

	for _, entry := range thread.Entries {
		if entry.State == StateOverLimit || entry.State == StateFailed {
			thread.Omitted++
		}
	}
}

func readMessage(ctx context.Context, source Source, entryID int64, retries int) (*generated.Message, error) {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message, err := source.Message(ctx, entryID)
		switch {
		case err != nil:
			lastErr = err
		case message == nil:
			lastErr = errors.New("message returned no data")
		default:
			return message, nil
		}
	}
	return nil, lastErr
}

type byteBudget struct {
	mu        sync.Mutex
	remaining int64
}

// admit reserves size bytes of the budget, reporting false when they do not fit.
func (b *byteBudget) admit(size int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size > b.remaining {
		return false
	}
	b.remaining -= size
	return true
}
