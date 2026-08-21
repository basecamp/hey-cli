package threadload

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// NewSDKSource reads a thread through the SDK: the paginated entries index, which
// carries the cursor `Topics().GetEntries` throws away, and each entry's message, which
// is where HEY keeps the body. It is the Source both the CLI and the TUI hand Load.
func NewSDKSource(client *hey.Client) Source {
	return sdkSource{client: client}
}

type sdkSource struct {
	client *hey.Client
}

func (s sdkSource) EntriesPage(ctx context.Context, topicID int64, cursor string) (Page, error) {
	page, err := s.client.Topics().GetEntriesPage(ctx, topicID, cursor)
	if err != nil {
		// A request the context ended mid-flight surfaces as a network error; that
		// is the context ending, which the loader handles, not the index failing.
		if ctx.Err() != nil {
			return Page{}, ctx.Err()
		}
		return Page{}, apierr.FromSDK(err)
	}
	if page == nil {
		return Page{}, fmt.Errorf("thread %d answered no entry page at cursor %q", topicID, cursor)
	}
	return Page{Entries: page.Entries, Next: page.NextPage}, nil
}

// Message reads one entry's message. A rate limit, an expired credential, a server
// error or a lost connection is about the service, not the message, and is marked
// systemic so the loader stops the fan-out rather than asking two thousand more times.
// A response the transport refused as too large is ErrOverLimit for that entry alone.
func (s sdkSource) Message(ctx context.Context, entryID int64) (*generated.Message, error) {
	message, err := s.client.Messages().Get(ctx, entryID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The transport wraps ErrOverLimit; the SDK may or may not keep the chain.
		if errors.Is(err, ErrOverLimit) || strings.Contains(err.Error(), ErrOverLimit.Error()) {
			return nil, fmt.Errorf("%w: %w", ErrOverLimit, err)
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
			return fmt.Errorf("%w: %w", ErrSystemic, err)
		}
	}
	return err
}

// Notice says what a load did not get, or nothing when it got everything, in terms of
// the limits it was read within. It is the notice a partial result carries and the
// message a refused one is refused with.
func (t *Thread) Notice(limits Limits) string {
	var parts []string
	if t.IndexTruncated {
		reason := ""
		switch t.IndexTruncatedBy {
		case TruncatedByPages:
			reason = fmt.Sprintf("beyond the %d-page limit", limits.MaxPages)
		case TruncatedByEntries:
			reason = fmt.Sprintf("beyond the %d-entry limit", limits.MaxEntries)
		case TruncatedByBytes:
			reason = fmt.Sprintf("beyond the %d-byte limit on what a read keeps", limits.MaxRetainedBytes)
		case TruncatedByDeadline:
			reason = fmt.Sprintf("the %s read limit passed before the index was read to its end", limits.Deadline)
		}
		parts = append(parts, fmt.Sprintf("only the newest %d entries were read; older ones exist, %s", len(t.Entries), reason))
	}
	if t.Omitted > 0 {
		failed, overLimit := 0, 0
		for _, entry := range t.Entries {
			switch entry.State {
			case StateFailed:
				failed++
			case StateOverLimit:
				overLimit++
			case StateHydrated, StateBodyless, StateNotRequested:
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
		parts = append(parts, fmt.Sprintf("%d of %d bodies could not be read%s", t.Omitted, len(t.Entries), detail))
	}
	return strings.Join(parts, "; ")
}
