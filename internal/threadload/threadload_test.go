package threadload

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// fakeSource serves a thread the way HEY does: the index newest first, a page at a
// time, and a message per entry.
type fakeSource struct {
	pages     [][]int64
	bodies    map[int64]string
	failing   map[int64]int // failures before a message succeeds; -1 fails forever
	systemic  map[int64]bool
	subjects  map[int64]string
	pageErr   map[int]error
	slow      time.Duration
	slowPages time.Duration
	mu        sync.Mutex
	messages  atomic.Int64
	index     atomic.Int64
}

func (f *fakeSource) EntriesPage(ctx context.Context, _ int64, cursor string) (Page, error) {
	f.index.Add(1)
	page := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "page-%d", &page)
	}
	if f.slowPages > 0 {
		select {
		case <-time.After(f.slowPages):
		case <-ctx.Done():
			return Page{}, ctx.Err()
		}
	}
	if err := f.pageErr[page]; err != nil {
		return Page{}, err
	}
	if page >= len(f.pages) {
		return Page{}, nil
	}
	entries := make([]generated.Entry, 0, len(f.pages[page]))
	for _, id := range f.pages[page] {
		entries = append(entries, generated.Entry{Id: id, Kind: "message", Summary: fmt.Sprintf("summary %d", id)})
	}
	next := ""
	if page+1 < len(f.pages) {
		next = fmt.Sprintf("page-%d", page+1)
	}
	return Page{Entries: entries, Next: next}, nil
}

func (f *fakeSource) Message(ctx context.Context, id int64) (*generated.Message, error) {
	f.messages.Add(1)
	if f.slow > 0 {
		select {
		case <-time.After(f.slow):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.systemic[id] {
		return nil, fmt.Errorf("%w: rate limited", ErrSystemic)
	}
	f.mu.Lock()
	remaining, failing := f.failing[id]
	if failing && remaining != 0 {
		if remaining > 0 {
			f.failing[id]--
		}
		f.mu.Unlock()
		return nil, fmt.Errorf("message %d: boom", id)
	}
	f.mu.Unlock()
	return &generated.Message{Id: id, Content: f.bodies[id], Subject: f.subjects[id],
		Addressed: generated.Addressed{Directly: []generated.Contact{{Name: "Rick Sanchez"}}}}, nil
}

func ids(entries []Entry) []int64 {
	out := make([]int64, len(entries))
	for i, entry := range entries {
		out[i] = entry.Entry.Id
	}
	return out
}

func states(entries []Entry) map[State]int {
	counts := map[State]int{}
	for _, entry := range entries {
		counts[entry.State]++
	}
	return counts
}

func limits() Limits {
	l := DefaultLimits
	l.Deadline = 5 * time.Second
	return l
}

// Two pages, newest first, come back as one thread, oldest first, every body read.
func TestLoadReadsEveryPageOldestFirst(t *testing.T) {
	source := &fakeSource{
		pages:  [][]int64{{13, 12}, {11}},
		bodies: map[int64]string{11: "<p>first</p>", 12: "<p>reply</p>", 13: "<p>last</p>"},
	}
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(thread.Entries); fmt.Sprint(got) != "[11 12 13]" {
		t.Errorf("entries = %v, want oldest first", got)
	}
	if !thread.Complete() || thread.Omitted != 0 || thread.IndexTruncated {
		t.Errorf("thread = %+v, want complete", thread)
	}
	if thread.Entries[0].Message.Content != "<p>first</p>" || thread.Entries[0].State != StateHydrated {
		t.Errorf("entry = %+v", thread.Entries[0])
	}
	if source.index.Load() != 2 || source.messages.Load() != 3 {
		t.Errorf("read %d pages and %d messages, want 2 and 3", source.index.Load(), source.messages.Load())
	}
}

// Without Hydrate no message is requested, which is what a count or an ID list needs.
func TestLoadWithoutHydrateRequestsNoMessages(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{12, 11}}}
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	if source.messages.Load() != 0 {
		t.Errorf("requested %d messages, want none", source.messages.Load())
	}
	if got := states(thread.Entries); got[StateNotRequested] != 2 {
		t.Errorf("states = %v", got)
	}
	if !thread.Complete() {
		t.Error("an unhydrated thread is complete when its index is")
	}
}

func TestLoadDistinguishesABodylessMessage(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{12, 11}}, bodies: map[int64]string{11: "<p>x</p>"}}
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Entries[0].State != StateHydrated || thread.Entries[1].State != StateBodyless {
		t.Errorf("states = %v", states(thread.Entries))
	}
	if !thread.Complete() {
		t.Error("a bodyless entry is not an omission")
	}
}

// The index stops at the page cap and says so; what was read is newest-first admission,
// so the oldest entries are the ones unseen.
func TestLoadStopsAtThePageCap(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{15, 14}, {13, 12}, {11}}}
	l := limits()
	l.MaxPages = 2
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	if !thread.IndexTruncated || thread.Complete() {
		t.Errorf("thread = %+v, want truncated", thread)
	}
	if got := ids(thread.Entries); fmt.Sprint(got) != "[12 13 14 15]" {
		t.Errorf("entries = %v, want the newest four", got)
	}
	if source.index.Load() != 2 {
		t.Errorf("read %d pages, want 2", source.index.Load())
	}
}

func TestLoadStopsAtTheEntryCap(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{15, 14, 13}, {12, 11}}}
	l := limits()
	l.MaxEntries = 2
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	if !thread.IndexTruncated || thread.IndexTruncatedBy != TruncatedByEntries || fmt.Sprint(ids(thread.Entries)) != "[14 15]" {
		t.Errorf("thread = %+v", thread)
	}
	if source.index.Load() != 1 {
		t.Errorf("read %d pages, want the cap to stop the walk", source.index.Load())
	}

	// A page that brings the count exactly to the cap ends the walk before another
	// request is made for a page nothing from would be kept.
	source = &fakeSource{pages: [][]int64{{15, 14}, {13}}}
	l.MaxEntries = 2
	thread, err = Load(context.Background(), source, Request{TopicID: 7, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	if !thread.IndexTruncated || source.index.Load() != 1 {
		t.Errorf("thread = %+v after %d page reads, want truncated after one", thread, source.index.Load())
	}
}

// What a thread keeps of a message is charged to the budget whole — body, subject,
// URL, creator — and what is not kept is not held: the recipient lists go.
func TestLoadChargesWhatItKeeps(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{12, 11}}, bodies: map[int64]string{12: "x", 11: "y"}, subjects: map[int64]string{12: strings.Repeat("s", 200)}}
	l := limits()
	l.MaxRetainedBytes = retainedOverhead + 100
	l.Concurrency = 1
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	// Newest first: 12's subject does not fit, so nothing is kept.
	if got := states(thread.Entries); got[StateOverLimit] != 2 {
		t.Errorf("states = %v, want both over the limit once the subject is charged", got)
	}
	l.MaxRetainedBytes = 2 * (retainedOverhead + 300)
	thread, err = Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range thread.Entries {
		if entry.Message == nil || len(entry.Message.Addressed.Directly) != 0 {
			t.Errorf("entry %d kept %+v, want the body without the recipient lists", entry.Entry.Id, entry.Message)
		}
	}
}

func TestLoadStopsRequestingMessagesAtTheRequestCap(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{14, 13, 12, 11}}, bodies: map[int64]string{14: "a", 13: "b", 12: "c", 11: "d"}}
	l := limits()
	l.MaxMessageRequests = 2
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	if source.messages.Load() != 2 {
		t.Errorf("requested %d messages, want 2", source.messages.Load())
	}
	// Newest first: 14 and 13 are read, 12 and 11 are over the limit.
	for _, entry := range thread.Entries {
		want := StateOverLimit
		if entry.Entry.Id >= 13 {
			want = StateHydrated
		}
		if entry.State != want {
			t.Errorf("entry %d = %s, want %s", entry.Entry.Id, entry.State, want)
		}
	}
	if thread.Omitted != 2 || thread.Complete() {
		t.Errorf("omitted = %d, complete = %v", thread.Omitted, thread.Complete())
	}
}

// Once a body does not fit the byte budget nothing older is kept, so the kept run is
// contiguous from the newest entry, and no more than the budget is ever held.
func TestLoadSpendsTheByteBudgetNewestFirst(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{14, 13, 12, 11}}, bodies: map[int64]string{
		14: strings.Repeat("a", 10), 13: strings.Repeat("b", 10), 12: strings.Repeat("c", 10), 11: "d",
	}}
	l := limits()
	l.MaxRetainedBytes = 2*retainedOverhead + 25
	l.Concurrency = 1
	for range 5 {
		thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
		if err != nil {
			t.Fatal(err)
		}
		want := map[int64]State{14: StateHydrated, 13: StateHydrated, 12: StateOverLimit, 11: StateOverLimit}
		for _, entry := range thread.Entries {
			if entry.State != want[entry.Entry.Id] {
				t.Errorf("entry %d = %s, want %s", entry.Entry.Id, entry.State, want[entry.Entry.Id])
			}
			if entry.State == StateOverLimit && entry.Message != nil {
				t.Error("an over-limit entry must not keep its content")
			}
		}
	}
}

// The loader's own deadline running out on a later index page is a truncation, like
// the page cap; the caller's deadline is still an error.
func TestLoadTreatsItsOwnIndexDeadlineAsTruncation(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{13, 12}, {11}}, slowPages: 150 * time.Millisecond}
	l := limits()
	l.Deadline = 220 * time.Millisecond
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Limits: l})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !thread.IndexTruncated || fmt.Sprint(ids(thread.Entries)) != "[12 13]" {
		t.Errorf("thread = %+v, want the first page, truncated", thread)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Millisecond)
	defer cancel()
	if _, err := Load(ctx, source, Request{TopicID: 7, Limits: limits()}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the caller's deadline", err)
	}
}

// A systemic error — a rate limit — stops the fan-out and is the load's error: no
// retry, no two thousand more requests, no partial thread reported as read.
func TestLoadStopsOnASystemicError(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{14, 13, 12, 11}}, bodies: map[int64]string{}, systemic: map[int64]bool{14: true}}
	l := limits()
	l.Concurrency = 1
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if !errors.Is(err, ErrSystemic) || thread != nil {
		t.Fatalf("Load = %+v, %v; want ErrSystemic", thread, err)
	}
	if source.messages.Load() > 2 {
		t.Errorf("requested %d messages after the first refusal, want the fan-out stopped", source.messages.Load())
	}
}

func TestLoadRetriesAFailedMessageAndThenGivesUp(t *testing.T) {
	source := &fakeSource{
		pages:   [][]int64{{13, 12, 11}},
		bodies:  map[int64]string{13: "a", 12: "b", 11: "c"},
		failing: map[int64]int{12: 1, 11: -1},
	}
	l := limits()
	l.MaxRetries = 1
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]Entry{}
	for _, entry := range thread.Entries {
		byID[entry.Entry.Id] = entry
	}
	if byID[12].State != StateHydrated {
		t.Errorf("entry 12 = %s, want hydrated after one retry", byID[12].State)
	}
	if byID[11].State != StateFailed || byID[11].Err == nil {
		t.Errorf("entry 11 = %+v, want failed with its error", byID[11])
	}
	if thread.Omitted != 1 {
		t.Errorf("omitted = %d", thread.Omitted)
	}
	// 13 once, 12 twice, 11 twice.
	if source.messages.Load() != 5 {
		t.Errorf("requested %d messages, want 5", source.messages.Load())
	}
}

func TestLoadMarksWhatTheDeadlineLeftUnread(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{14, 13, 12, 11}}, bodies: map[int64]string{}, slow: 200 * time.Millisecond}
	l := limits()
	l.Concurrency = 1
	l.Deadline = 300 * time.Millisecond
	l.MaxRetries = 0
	start := time.Now()
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("load took %s, want the deadline to end it", time.Since(start))
	}
	got := states(thread.Entries)
	if got[StateOverLimit] == 0 || got[StateFailed] != 0 {
		t.Errorf("states = %v, want the unread entries over_limit, none failed", got)
	}
	if thread.Complete() {
		t.Error("a thread the deadline cut short is not complete")
	}
}

// An index that fails is a thread that cannot be read, whichever page failed: an
// index with a hole in it is not a thread.
func TestLoadFailsClosedOnAnIndexError(t *testing.T) {
	boom := errors.New("boom")
	for page := range 2 {
		source := &fakeSource{pages: [][]int64{{12}, {11}}, pageErr: map[int]error{page: boom}}
		_, err := Load(context.Background(), source, Request{TopicID: 7, Limits: limits()})
		if !errors.Is(err, boom) {
			t.Errorf("page %d: error = %v, want the index failure", page, err)
		}
	}
}

func TestLoadEndsTheIndexOnAnEmptyPage(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{12, 11}, {}, {10}}}
	thread, err := Load(context.Background(), source, Request{TopicID: 7, Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(thread.Entries)) != "[11 12]" || thread.IndexTruncated {
		t.Errorf("thread = %+v, want the walk to stop at the empty page without claiming truncation", thread)
	}
}

// A caller that stops waiting gets its cancellation back as the error it is, not a
// partial thread whose unread entries look like a limit was reached.
func TestLoadReturnsTheCallersCancellation(t *testing.T) {
	source := &fakeSource{pages: [][]int64{{14, 13, 12, 11}}, bodies: map[int64]string{}, slow: 100 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	l := limits()
	l.Concurrency = 1
	l.MaxRetries = 0

	thread, err := Load(ctx, source, Request{TopicID: 7, Hydrate: true, Limits: l})
	if !errors.Is(err, context.DeadlineExceeded) || thread != nil {
		t.Fatalf("Load = %+v, %v; want the caller's deadline as the error", thread, err)
	}
}
