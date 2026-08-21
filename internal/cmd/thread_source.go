package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// sdkThreadSource reads a thread through the SDK for threadload: the paginated entries
// index, which carries the cursor `Topics().GetEntries` throws away, and each entry's
// message, which is where HEY keeps the body.
type sdkThreadSource struct {
	client *hey.Client
}

func (s sdkThreadSource) EntriesPage(ctx context.Context, topicID int64, cursor string) (threadload.Page, error) {
	page, err := s.client.Topics().GetEntriesPage(ctx, topicID, cursor)
	if err != nil {
		return threadload.Page{}, apierr.FromSDK(err)
	}
	if page == nil {
		return threadload.Page{}, fmt.Errorf("thread %d answered no entry page at cursor %q", topicID, cursor)
	}
	return threadload.Page{Entries: page.Entries, Next: page.NextPage}, nil
}

// Message reads one entry's message. A rate limit, an expired credential, a server
// error or a lost connection is about the service, not the message, and is marked
// systemic so the loader stops the fan-out rather than asking two thousand more times.
func (s sdkThreadSource) Message(ctx context.Context, entryID int64) (*generated.Message, error) {
	message, err := s.client.Messages().Get(ctx, entryID)
	if err != nil {
		// A request the context ended mid-flight surfaces as a network error; that
		// is the context ending, which the loader handles, not the service failing.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrResponseTooLarge) || strings.Contains(err.Error(), ErrResponseTooLarge.Error()) {
			return nil, fmt.Errorf("%w: %w", threadload.ErrOverLimit, err)
		}
		return nil, systemic(apierr.FromSDK(err))
	}
	return message, nil
}

func systemic(err error) error {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == apierr.CodeRateLimit, apiErr.Code == apierr.CodeAuth, apiErr.Code == apierr.CodeForbidden,
			apiErr.Code == apierr.CodeNetwork, apiErr.HTTPStatus >= 500:
			return fmt.Errorf("%w: %w", threadload.ErrSystemic, err)
		}
	}
	return err
}

// threadLimits is what the CLI reads a thread within. A variable so tests can lower it.
var threadLimits = threadload.DefaultLimits

// loadThread reads a thread within threadLimits, with or without bodies.
func loadThread(ctx context.Context, threadID int64, hydrate bool) (*threadload.Thread, error) {
	thread, err := threadload.Load(ctx, sdkThreadSource{client: sdk}, threadload.Request{
		TopicID: threadID,
		Hydrate: hydrate,
		Limits:  threadLimits,
	})
	if err != nil {
		return nil, err
	}
	if len(thread.Entries) == 0 {
		return nil, apierr.ErrNotFound("entries for thread", fmt.Sprint(threadID))
	}
	return thread, nil
}

// threadNotice says what a load did not get, or nothing when it got everything. It is
// the notice a partial result carries and the message a refused one is refused with.
func threadNotice(thread *threadload.Thread) string {
	var parts []string
	if thread.IndexTruncated {
		reason := ""
		switch thread.IndexTruncatedBy {
		case threadload.TruncatedByPages:
			reason = fmt.Sprintf("beyond the %d-page limit", threadLimits.MaxPages)
		case threadload.TruncatedByEntries:
			reason = fmt.Sprintf("beyond the %d-entry limit", threadLimits.MaxEntries)
		case threadload.TruncatedByBytes:
			reason = fmt.Sprintf("beyond the %d-byte limit on what a read keeps", threadLimits.MaxRetainedBytes)
		case threadload.TruncatedByDeadline:
			reason = fmt.Sprintf("the %s read limit passed before the index was read to its end", threadLimits.Deadline)
		}
		parts = append(parts, fmt.Sprintf("only the newest %d entries were read; older ones exist, %s", len(thread.Entries), reason))
	}
	if thread.Omitted > 0 {
		failed, overLimit := 0, 0
		for _, entry := range thread.Entries {
			switch entry.State {
			case threadload.StateFailed:
				failed++
			case threadload.StateOverLimit:
				overLimit++
			case threadload.StateHydrated, threadload.StateBodyless, threadload.StateNotRequested:
			}
		}
		detail := ""
		switch {
		case failed > 0 && overLimit > 0:
			detail = fmt.Sprintf(" (%d failed, %d over the read limits)", failed, overLimit)
		case failed > 0:
			detail = " (failed)"
		case overLimit > 0:
			detail = " (over the read limits)"
		}
		parts = append(parts, fmt.Sprintf("%d of %d bodies could not be read%s", thread.Omitted, len(thread.Entries), detail))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return parts[0] + "; " + parts[1]
	}
}

// errPartialThread is how a partial read is refused without --allow-partial: an API
// error naming what is missing and the flag that accepts it.
func errPartialThread(threadID int64, notice string) error {
	return &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: fmt.Sprintf("thread %d was read only in part: %s", threadID, notice),
		Hint:    "pass --allow-partial to take what could be read",
	}
}
