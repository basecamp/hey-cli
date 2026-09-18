package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// threadLimits is what the CLI reads a thread within. A variable so tests can lower it.
var threadLimits = threadload.DefaultLimits

// loadThread reads a thread within threadLimits, with or without bodies. It does not
// retain recipients for consumers, such as attachments, that only need the body.
func loadThread(ctx context.Context, threadID int64, hydrate bool) (*threadload.Thread, error) {
	return loadThreadRetainingRecipients(ctx, threadID, hydrate, false)
}

// loadThreadWithRecipients is the thread-read path: when it reads messages, it keeps
// their To, Cc and Bcc identities for the JSON and HTML representations.
func loadThreadWithRecipients(ctx context.Context, threadID int64, hydrate bool) (*threadload.Thread, error) {
	return loadThreadRetainingRecipients(ctx, threadID, hydrate, true)
}

func loadThreadRetainingRecipients(ctx context.Context, threadID int64, hydrate, retainRecipients bool) (*threadload.Thread, error) {
	thread, err := threadload.Load(ctx, threadload.NewSDKSource(sdk), threadload.Request{
		TopicID:          threadID,
		Hydrate:          hydrate,
		RetainRecipients: retainRecipients,
		Limits:           threadLimits,
	})
	if err != nil {
		return nil, describeBundleMisread(ctx, threadID, err)
	}
	if len(thread.Entries) == 0 {
		return nil, apierr.ErrNotFound("entries for thread", fmt.Sprint(threadID))
	}
	return thread, nil
}

// describeBundleMisread names the mistake behind a thread read that 404s on a bundle's
// own id. A bundle row has no topic_id, and the likeliest id to be tried in its place is
// the row's own — which is not a topic, so the topic route answers not-found and a caller
// reads "no content" where the truth is "not a thread". A not-found is therefore checked
// against the bundle route, which answers exactly for postings that are bundles, and the
// error then says what the id really is and where its mail lives. Any other id 404s on
// the probe too and keeps its own error; the probe costs one request, on the error path
// alone.
func describeBundleMisread(ctx context.Context, threadID int64, err error) error {
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierr.CodeNotFound {
		return err
	}
	bundle, probeErr := sdk.Postings().BundleUnseenPage(ctx, threadID, "")
	if probeErr != nil || bundle == nil {
		return err
	}
	name := terminal.SanitizeLine(bundle.Contact.Name)
	return &apierr.Error{
		Code:       apierr.CodeNotFound,
		Message:    fmt.Sprintf("%d is a bundle, not a thread: it groups mail from %s and names no topic", threadID, name),
		Hint:       fmt.Sprintf("List its unseen threads with hey bundle view %d, or every thread with %s via hey contact threads %d.", threadID, name, bundle.Contact.Id),
		HTTPStatus: 404,
	}
}

// threadNotice is the thread's notice in terms of the CLI's limits.
func threadNotice(thread *threadload.Thread) string {
	return thread.Notice(threadLimits)
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
