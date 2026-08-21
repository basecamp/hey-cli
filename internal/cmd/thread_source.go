package cmd

import (
	"context"
	"fmt"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// threadLimits is what the CLI reads a thread within. A variable so tests can lower it.
var threadLimits = threadload.DefaultLimits

// loadThread reads a thread within threadLimits, with or without bodies.
func loadThread(ctx context.Context, threadID int64, hydrate bool) (*threadload.Thread, error) {
	thread, err := threadload.Load(ctx, threadload.NewSDKSource(sdk), threadload.Request{
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
