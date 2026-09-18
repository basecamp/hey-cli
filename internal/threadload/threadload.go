// Package threadload reads a thread — its entries and, when asked, each entry's
// message — within limits it states up front, and says exactly what it got.
//
// HEY serves a thread's entries newest first, a page at a time, and keeps each body on
// the message rather than the entry, so a full read is one page walk and then one
// request per entry. Every one of those is a request a server of somebody else's
// choosing answers, so each has a cap: on pages, on entries, on message requests, on
// bytes kept, on time. Nothing here decides what to do when a cap is hit; the result
// records it per entry and the caller decides, which is what keeps `hey thread read
// --count` from reading bodies it will not show and `hey attachment list` reading bodies
// it cannot do without.
package threadload

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
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

// ErrSystemic marks an error a Source returns that is about the client rather than
// the one request — a rate limit, an expired credential. A message request that fails
// with it is not retried and not recorded as one failed body: the whole load stops and
// returns the error, since every request that follows would meet the same answer.
var ErrSystemic = errors.New("the service is refusing requests")

// ErrOverLimit marks an error a Source returns for one message that was too large to
// read — a response past the transport's cap. It is not retried, since it would be
// too large again, and not a failed body: the entry is over_limit, like one the byte
// budget refused.
var ErrOverLimit = errors.New("the message was too large to read")

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
	// MaxRetainedBytes is how much thread data is kept in total — index metadata,
	// message content and metadata, and recipient or inbound-delivery identities
	// when requested. A message whose retained data would exceed it is over_limit,
	// and an index that would exceed it is truncated.
	MaxRetainedBytes int64
	// Deadline is how long the whole load may take; entries not yet requested when it
	// passes are over_limit.
	Deadline time.Duration
}

// DefaultLimits are what the CLI reads with: a hundred pages past the first, two
// thousand entries and as many bodies, two tries per body on top of the SDK's own
// retries, eight at a time, 64 MiB of thread data kept, two minutes in all.
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
	// RetainRecipients keeps and charges the message's To, Cc and Bcc identities.
	// Callers that need only bodies leave it false so recipient lists cannot consume
	// the body's budget.
	RetainRecipients bool
	// RetainReceivedVia keeps and charges the exact account addresses HEY recorded
	// an inbound message arriving through, and their optional contact identities.
	// Callers that do not publish delivery metadata leave it false.
	RetainReceivedVia bool
	Limits            Limits
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

// Truncation is what stopped the index walk short of its end.
type Truncation string

const (
	TruncatedByPages    Truncation = "pages"
	TruncatedByEntries  Truncation = "entries"
	TruncatedByBytes    Truncation = "bytes"
	TruncatedByDeadline Truncation = "deadline"
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
	// IndexTruncated reports that the index was not read to its end — more pages than
	// MaxPages, more entries than MaxEntries, or the Deadline passed — so entries older
	// than the last one here exist unseen. IndexTruncatedBy names which.
	IndexTruncated   bool
	IndexTruncatedBy Truncation
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
// is not a thread — or a caller that stopped waiting: its context ending is its
// decision, not a limit, and is returned as the error it is. A body that could not be
// read within the limits is an entry in the failed or over_limit state, not an error.
func Load(ctx context.Context, source Source, request Request) (*Thread, error) {
	caller := ctx
	limits := request.Limits
	if limits.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limits.Deadline)
		defer cancel()
	}

	budget := &byteBudget{remaining: limits.MaxRetainedBytes}
	entries, truncated, err := readIndex(ctx, caller, source, request.TopicID, limits, budget)
	if err != nil {
		return nil, err
	}

	thread := &Thread{Entries: make([]Entry, len(entries)), IndexTruncated: truncated != "", IndexTruncatedBy: truncated}
	for i, entry := range entries {
		thread.Entries[i] = Entry{Entry: entry, State: StateNotRequested}
	}
	if request.Hydrate {
		if err := hydrate(ctx, source, thread, limits, budget, request.RetainRecipients, request.RetainReceivedVia); err != nil {
			return nil, err
		}
		if err := caller.Err(); err != nil {
			return nil, err
		}
	}

	// Admission is newest first, which is the order the index serves; reading order
	// is oldest first, which is the one reversal.
	slices.Reverse(thread.Entries)
	return thread, nil
}

// readIndex walks the entries index newest first until it ends or a limit is reached.
// The loader's own deadline running out part way is a limit like the page cap — what
// was read is returned as truncated — where the caller's context ending, or a page
// that fails, is an error.
func readIndex(ctx, caller context.Context, source Source, topicID int64, limits Limits, budget *byteBudget) (entries []generated.Entry, truncated Truncation, err error) {
	cursor := ""
	for page := 0; ; page++ {
		if page >= limits.MaxPages {
			return entries, TruncatedByPages, nil
		}
		result, err := source.EntriesPage(ctx, topicID, cursor)
		if err != nil {
			if page > 0 && ctx.Err() != nil && caller.Err() == nil {
				return entries, TruncatedByDeadline, nil
			}
			if page == 0 {
				return nil, "", err
			}
			return nil, "", fmt.Errorf("thread %d: entries page %d: %w", topicID, page+1, err)
		}
		for _, entry := range result.Entries {
			if len(entries) >= limits.MaxEntries {
				return entries, TruncatedByEntries, nil
			}
			// The index is charged to the same budget as the bodies: a page is capped
			// by the transport, but a hundred of them are not. Project it before
			// admission so every retained creator field is represented by that charge.
			kept, size := retainedEntry(entry)
			if !budget.admit(size) {
				return entries, TruncatedByBytes, nil
			}
			entries = append(entries, kept)
		}
		// An empty page ends the index whatever cursor came with it; a page that
		// brought the count exactly to the cap ends the walk before another request.
		if result.Next == "" || len(result.Entries) == 0 {
			return entries, "", nil
		}
		if len(entries) >= limits.MaxEntries {
			return entries, TruncatedByEntries, nil
		}
		cursor = result.Next
	}
}

// hydrate reads each entry's message, newest first, Concurrency at a time, within the
// request, byte and time limits. Requests run concurrently, but each one's result is
// admitted into the byte budget in entry order — a worker that finishes early waits
// its turn — so what a large thread keeps is decided newest first and is the same on
// every run, and no more than the budget plus the requests in flight is ever held.
// Once a body did not fit, nothing older is kept either, so the kept run is
// contiguous. Entries past a limit are over_limit; a request that fails after its
// retries leaves its entry failed, with its reason bounded; a systemic error —
// ErrSystemic from the Source — stops every request in flight and is returned.
func hydrate(ctx context.Context, source Source, thread *Thread, limits Limits, budget *byteBudget, retainRecipients, retainReceivedVia bool) error {
	turns := newTurns()
	refused := make([]bool, len(thread.Entries))
	var exhausted atomic.Bool
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(max(limits.Concurrency, 1))
	for i := range thread.Entries {
		entry := &thread.Entries[i]
		if i >= limits.MaxMessageRequests {
			entry.State = StateOverLimit
			continue
		}
		group.Go(func() error {
			defer turns.done(i)
			// The group's context ending is the deadline, or a systemic error in
			// another request; an entry it reaches is over the limit, not failed. So
			// is one reached after the budget refused a body: nothing older would be
			// kept, so nothing older is asked for.
			if groupCtx.Err() != nil || exhausted.Load() {
				entry.State = StateOverLimit
				return nil //nolint:nilerr // the deadline is a state, not a failure
			}
			message, err := readMessage(groupCtx, source, entry.Entry.Id, limits.MaxRetries)
			switch {
			case errors.Is(err, ErrSystemic):
				entry.State = StateOverLimit
				turns.abort()
				return err
			case errors.Is(err, ErrOverLimit):
				entry.State = StateOverLimit
			case err != nil && groupCtx.Err() != nil:
				entry.State = StateOverLimit
			case err != nil:
				entry.State, entry.Err = StateFailed, boundedError(err)
			default:
				kept, size := retained(message, retainRecipients, retainReceivedVia)
				turns.wait(i)
				switch {
				case !budget.admit(size):
					entry.State, refused[i] = StateOverLimit, true
					exhausted.Store(true)
				case kept.Content == "":
					entry.Message, entry.State = kept, StateBodyless
				default:
					entry.Message, entry.State = kept, StateHydrated
				}
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}

	// Once the budget refused a body, nothing older is kept either: the run of kept
	// bodies stays contiguous from the newest entry. A single message too large to
	// read is its own over_limit and casts no shadow on older ones.
	dropping := false
	for i := range thread.Entries {
		entry := &thread.Entries[i]
		switch {
		case dropping && (entry.State == StateHydrated || entry.State == StateBodyless):
			entry.Message, entry.State = nil, StateOverLimit
		case refused[i]:
			dropping = true
		}
	}

	for _, entry := range thread.Entries {
		if entry.State == StateOverLimit || entry.State == StateFailed {
			thread.Omitted++
		}
	}
	return nil
}

func readMessage(ctx context.Context, source Source, entryID int64, retries int) (*generated.Message, error) {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message, err := source.Message(ctx, entryID)
		switch {
		case errors.Is(err, ErrSystemic), errors.Is(err, ErrOverLimit):
			return nil, err
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

// turns hands the byte budget to workers in entry order: a worker waits until every
// entry before it has taken its turn, whether that entry was admitted, refused, failed
// or skipped, so admission is newest first however the requests finished.
type turns struct {
	mu      sync.Mutex
	cond    *sync.Cond
	next    int
	aborted bool
}

func newTurns() *turns {
	t := &turns{}
	t.cond = sync.NewCond(&t.mu)
	return t
}

func (t *turns) wait(i int) {
	t.mu.Lock()
	for t.next != i && !t.aborted {
		t.cond.Wait()
	}
	t.mu.Unlock()
}

// done is called by every worker on its way out, turn taken or not; it waits for its
// turn first so that the order holds even for a worker that had nothing to admit.
func (t *turns) done(i int) {
	t.mu.Lock()
	for t.next != i && !t.aborted {
		t.cond.Wait()
	}
	t.next++
	t.cond.Broadcast()
	t.mu.Unlock()
}

// abort releases every worker from its turn: a systemic error is about to end the
// load, and the worker carrying it must not queue behind slower requests to say so.
func (t *turns) abort() {
	t.mu.Lock()
	t.aborted = true
	t.cond.Broadcast()
	t.mu.Unlock()
}

// retainedEntry is the part of an index entry a thread keeps, and how many bytes it
// costs the budget. Creator is projected to its identity fields before admission so
// an uncharged contact presentation field cannot escape the retention limit.
func retainedEntry(entry generated.Entry) (generated.Entry, int64) {
	entry.Creator = retainedContact(entry.Creator)
	return entry, int64(len(entry.Summary)+len(entry.Subject)+len(entry.AppUrl)+len(entry.Kind)+
		len(entry.AlternativeSenderName)+len(entry.Creator.Name)+len(entry.Creator.EmailAddress)) + retainedOverhead
}

// boundedError keeps why a read failed without keeping the failure: an error carrying
// a response body is cut to a sentence.
func boundedError(err error) error {
	const limit = 256
	message := err.Error()
	if len(message) > limit {
		message = message[:limit] + "…"
	}
	return errors.New(message)
}

// retained is the part of a message a thread keeps — its body and metadata, plus
// recipients and inbound delivery records when the caller needs them — and how many
// bytes it costs the budget. All contacts are projected to id, name and email before
// admission so presentation fields that are neither needed nor charged cannot escape
// the retention limit.
func retained(message *generated.Message, retainRecipients, retainReceivedVia bool) (*generated.Message, int64) {
	kept := &generated.Message{
		Id:        message.Id,
		Content:   message.Content,
		Subject:   message.Subject,
		Url:       message.Url,
		Creator:   retainedContact(message.Creator),
		CreatedAt: message.CreatedAt,
		UpdatedAt: message.UpdatedAt,
	}
	if retainRecipients {
		kept.Addressed = generated.Addressed{
			Directly:    retainedContacts(message.Addressed.Directly),
			Copied:      retainedContacts(message.Addressed.Copied),
			Blindcopied: retainedContacts(message.Addressed.Blindcopied),
		}
	}
	if retainReceivedVia {
		kept.ReceivedVia = retainedReceivedVia(message.ReceivedVia)
	}
	size := int64(len(kept.Content)+len(kept.Subject)+len(kept.Url)+
		len(kept.Creator.Name)+len(kept.Creator.EmailAddress)) + retainedOverhead
	if retainRecipients {
		size += contactsSize(kept.Addressed.Directly) + contactsSize(kept.Addressed.Copied) + contactsSize(kept.Addressed.Blindcopied)
	}
	if retainReceivedVia {
		size += receivedViaSize(kept.ReceivedVia)
	}
	return kept, size
}

func retainedContact(contact generated.Contact) generated.Contact {
	return generated.Contact{
		Id:           contact.Id,
		Name:         contact.Name,
		EmailAddress: contact.EmailAddress,
	}
}

func retainedContacts(contacts []generated.Contact) []generated.Contact {
	if len(contacts) == 0 {
		return nil
	}
	kept := make([]generated.Contact, len(contacts))
	for i, contact := range contacts {
		kept[i] = retainedContact(contact)
	}
	return kept
}

func retainedReceivedVia(deliveries []generated.MessageReceivedVia) []generated.MessageReceivedVia {
	if len(deliveries) == 0 {
		return nil
	}
	kept := make([]generated.MessageReceivedVia, len(deliveries))
	for i, delivery := range deliveries {
		kept[i].EmailAddress = delivery.EmailAddress
		if delivery.Contact != nil {
			contact := retainedContact(*delivery.Contact)
			kept[i].Contact = &contact
		}
	}
	return kept
}

// contactsSize is what a kept recipient list costs the budget: each address's strings
// and an overhead. A message to a thousand addresses is a thousand addresses held, so
// each is charged.
func contactsSize(contacts []generated.Contact) int64 {
	size := int64(0)
	for _, contact := range contacts {
		size += int64(len(contact.Name)+len(contact.EmailAddress)) + retainedContactOverhead
	}
	return size
}

// receivedViaSize charges every delivery address and, when HEY resolved one, the
// retained contact identity. The complete size is admitted with the rest of the message,
// so a delivery list never survives only in part.
func receivedViaSize(deliveries []generated.MessageReceivedVia) int64 {
	size := int64(0)
	for _, delivery := range deliveries {
		size += int64(len(delivery.EmailAddress)) + retainedDeliveryOverhead
		if delivery.Contact != nil {
			size += int64(len(delivery.Contact.Name)+len(delivery.Contact.EmailAddress)) + retainedContactOverhead
		}
	}
	return size
}

// retainedOverhead is what a kept message costs beyond its strings: the struct, the
// entry, the timestamps.
const retainedOverhead = 512

// retainedContactOverhead is what a kept recipient or resolved delivery contact costs
// beyond its strings.
const retainedContactOverhead = 128

// retainedDeliveryOverhead is what a received-via record costs beyond its address and
// optional contact.
const retainedDeliveryOverhead = 128

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
