package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
)

const (
	bubbleVerificationAttempts = 8
	bubbleVerificationDelay    = 250 * time.Millisecond
)

type bubbleAction string

const (
	bubbleNowAction bubbleAction = "bubble_up_now"
	popAction       bubbleAction = "pop"
)

type bubbleActionCommand struct {
	cmd     *cobra.Command
	action  bubbleAction
	topicID int64
}

// bubblePostingState is deliberately limited to the two boxes that establish
// whether an exact posting is actively or prospectively bubbled. It never
// guesses state from a missing or partial page.
type bubblePostingState struct {
	PostingID int64 `json:"posting_id"`
	TopicID   int64 `json:"topic_id"`
	Present   bool  `json:"present"`
	InImbox   bool  `json:"in_imbox"`

	InBubbleUp bool `json:"in_bubble_up"`
	BubbledUp  bool `json:"bubbled_up"`
	Scheduled  bool `json:"scheduled"`
}

type bubbleActionResult struct {
	Action    string             `json:"action"`
	PostingID int64              `json:"posting_id"`
	TopicID   int64              `json:"topic_id"`
	Changed   bool               `json:"changed"`
	NoOp      bool               `json:"no_op"`
	Verified  bool               `json:"verified"`
	Reason    string             `json:"reason,omitempty"`
	Before    bubblePostingState `json:"before"`
	After     bubblePostingState `json:"after"`
}

func newBubbleUpNowCommand() *bubbleActionCommand {
	return newBubbleActionCommand(bubbleNowAction)
}

func newPopCommand() *bubbleActionCommand {
	return newBubbleActionCommand(popAction)
}

func newBubbleActionCommand(action bubbleAction) *bubbleActionCommand {
	c := &bubbleActionCommand{action: action}
	use := "bubble-up-now <box-item-id> --topic-id <thread-id>"
	short := "Bubble one exact thread to the top of the Imbox now"
	long := "Immediately Bubble Up one exact HEY box item. Its box item ID and thread ID are both required and verified before any change."
	example := "  hey bubble-up-now 731245 --topic-id 912876\n  hey bubble-up-now 731245 --topic-id 912876 --json"
	agentNotes := "Requires the exact box item ID and thread ID from one row returned by `hey box view imbox --all --json`. Reads Imbox and Bubble Up completely, uses the SDK operation, and verifies the result. Repeating an already-applied action is a no-op."
	if action == popAction {
		use = "pop <box-item-id> --topic-id <thread-id>"
		short = "Remove one exact thread from Bubble Up"
		long = "Remove one exact HEY box item from Bubble Up. Its box item ID and thread ID are both required and verified before any change."
		example = "  hey pop 731245 --topic-id 912876\n  hey pop 731245 --topic-id 912876 --json"
	}

	c.cmd = &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		Example: example,
		Annotations: map[string]string{
			"agent_notes": agentNotes,
		},
		Args: usageExactOneArg(),
		RunE: c.run,
	}
	c.cmd.Flags().Int64Var(&c.topicID, "topic-id", 0, "Exact HEY thread ID paired with the box item (required)")
	return c
}

func (c *bubbleActionCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	postingID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || postingID <= 0 {
		return apierr.ErrUsageHint("invalid box item ID: must be a positive integer", "Pass the ID from one row returned by `hey box view imbox --all --json`.")
	}
	if c.topicID <= 0 {
		return apierr.ErrUsageHint("invalid or missing --topic-id: must be a positive integer", "Pass the topic_id from the same box item row.")
	}

	ctx := cmd.Context()
	before, err := readExactBubblePostingState(ctx, postingID, c.topicID)
	if err != nil {
		return err
	}
	if !before.Present {
		return bubbleTargetNotFoundError(postingID, c.topicID)
	}

	if reason := c.noOpReason(before); reason != "" {
		return c.writeResult(cmd, bubbleActionResult{
			Action:    string(c.action),
			PostingID: postingID,
			TopicID:   c.topicID,
			NoOp:      true,
			Verified:  true,
			Reason:    reason,
			Before:    before,
			After:     before,
		})
	}

	mutationErr := c.mutate(ctx, postingID)
	if mutationErr != nil {
		// Bubble Up Now is non-idempotent and the generated SDK does not retry it.
		// A read can still resolve an ambiguous response without replaying it.
		after, verified, _ := c.verify(ctx, postingID)
		if verified {
			return c.writeResult(cmd, bubbleActionResult{
				Action:    string(c.action),
				PostingID: postingID,
				TopicID:   c.topicID,
				Changed:   true,
				Verified:  true,
				Reason:    "state verified after an ambiguous mutation response",
				Before:    before,
				After:     after,
			})
		}
		converted := apierr.AsError(apierr.FromSDK(mutationErr))
		return &apierr.Error{
			Code:    "mutation_unconfirmed",
			Message: fmt.Sprintf("%s was not confirmed: %s", c.displayName(), converted.Message),
			Hint:    "The CLI did not replay the operation. Re-run this command; it will return a no-op if HEY already applied it.",
			Cause:   mutationErr,
			Meta:    map[string]any{"retryable": true},
		}
	}

	after, verified, verifyErr := c.verify(ctx, postingID)
	if !verified {
		message := fmt.Sprintf("%s returned success, but the exact box item/thread state was not confirmed", c.displayName())
		if verifyErr != nil {
			message += ": " + verifyErr.Error()
		}
		return &apierr.Error{
			Code:    "verification_failed",
			Message: message,
			Hint:    "The CLI did not replay the operation. Re-run this command; it will return a no-op if HEY already applied it.",
			Cause:   verifyErr,
			Meta:    map[string]any{"retryable": true},
		}
	}

	return c.writeResult(cmd, bubbleActionResult{
		Action:    string(c.action),
		PostingID: postingID,
		TopicID:   c.topicID,
		Changed:   true,
		Verified:  true,
		Before:    before,
		After:     after,
	})
}

func (c *bubbleActionCommand) mutate(ctx context.Context, postingID int64) error {
	if c.action == popAction {
		return sdk.Postings().CancelBubbleUp(ctx, postingID)
	}
	return sdk.Postings().BubbleUpNow(ctx, postingID)
}

func (c *bubbleActionCommand) verify(ctx context.Context, postingID int64) (bubblePostingState, bool, error) {
	var last bubblePostingState
	var lastErr error
	for attempt := 0; attempt < bubbleVerificationAttempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(bubbleVerificationDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return last, false, ctx.Err()
			case <-timer.C:
			}
		}

		state, err := readExactBubblePostingState(ctx, postingID, c.topicID)
		if err != nil {
			lastErr = err
			continue
		}
		last = state
		lastErr = nil
		if c.action == bubbleNowAction && state.Present && state.InImbox && state.BubbledUp {
			return state, true, nil
		}
		if c.action == popAction && state.Present && state.InImbox && !state.InBubbleUp && !state.BubbledUp && !state.Scheduled {
			return state, true, nil
		}
	}
	return last, false, lastErr
}

func (c *bubbleActionCommand) noOpReason(state bubblePostingState) string {
	if c.action == bubbleNowAction && state.InImbox && state.BubbledUp {
		return "box item is already bubbled to the top of the Imbox"
	}
	if c.action == popAction && state.InImbox && !state.InBubbleUp && !state.BubbledUp && !state.Scheduled {
		return "box item is already out of Bubble Up"
	}
	return ""
}

func (c *bubbleActionCommand) displayName() string {
	if c.action == popAction {
		return "Pop"
	}
	return "Bubble Up Now"
}

func (c *bubbleActionCommand) writeResult(cmd *cobra.Command, result bubbleActionResult) error {
	summary := fmt.Sprintf("%s verified for box item %d / thread %d", c.displayName(), result.PostingID, result.TopicID)
	if result.NoOp {
		summary = fmt.Sprintf("No-op: %s", result.Reason)
	}
	return writeMutation(cmd, summary, result)
}

type bubbleBoxSource struct {
	name string
	key  string
}

func readExactBubblePostingState(ctx context.Context, postingID, topicID int64) (bubblePostingState, error) {
	state := bubblePostingState{PostingID: postingID, TopicID: topicID}
	var conflictPostingID int64
	var conflictTopicID int64
	incompletePosting := false
	sources := []bubbleBoxSource{
		{name: "Imbox", key: "imbox"},
		{name: "Bubble Up", key: "bubblebox"},
	}

	for _, source := range sources {
		resp, err := resolveBox(ctx, source.key, "")
		if err != nil {
			return state, err
		}
		if resp == nil {
			return state, bubbleLookupIncompleteError(source.name, "HEY returned no box page")
		}

		mailSource := mail.BoxSource(resp)
		collected, err := collectPages(ctx,
			pageResult[generated.Posting]{Items: resp.Postings, Cursor: resp.NextHistoryUrl},
			pageRequest{All: true, MaxPages: maxPostingPages},
			readSourcePage(mailSource),
		)
		if err != nil {
			return state, err
		}
		if collected.Truncated || collected.Cursor != "" {
			return state, bubbleLookupIncompleteError(source.name, "pagination ended before every box item was read")
		}

		for _, posting := range collected.Items {
			resolvedTopicID := resolvePostingTopicID(posting)
			if posting.Id == postingID && resolvedTopicID == 0 {
				incompletePosting = true
				continue
			}
			if posting.Id != postingID || resolvedTopicID != topicID {
				if conflictPostingID == 0 && (posting.Id == postingID || resolvedTopicID == topicID) {
					conflictPostingID = posting.Id
					conflictTopicID = resolvedTopicID
				}
				continue
			}

			state.Present = true
			state.BubbledUp = state.BubbledUp || posting.BubbledUp
			if source.key == "imbox" {
				state.InImbox = true
			} else {
				state.InBubbleUp = true
			}
		}
	}
	state.Scheduled = state.InBubbleUp && !state.BubbledUp
	if state.Present {
		return state, nil
	}
	if incompletePosting {
		return state, bubbleTargetIncompleteError(postingID, topicID)
	}
	if conflictPostingID != 0 {
		return state, bubbleTargetMismatchError(postingID, topicID, conflictPostingID, conflictTopicID)
	}
	return state, nil
}

func bubbleLookupIncompleteError(source, detail string) error {
	return &apierr.Error{
		Code:    "target_lookup_incomplete",
		Message: fmt.Sprintf("%s lookup was incomplete: %s", source, detail),
		Hint:    "No change was attempted. Retry after HEY can return the complete box history.",
		Meta:    map[string]any{"retryable": true},
	}
}

func bubbleTargetMismatchError(wantPostingID, wantTopicID, foundPostingID, foundTopicID int64) error {
	return &apierr.Error{
		Code:       "target_mismatch",
		Message:    fmt.Sprintf("box item/thread pair does not match: requested %d/%d, HEY returned %d/%d", wantPostingID, wantTopicID, foundPostingID, foundTopicID),
		Hint:       "No change was attempted. Copy both IDs from the same row returned by `hey box view imbox --all --json`.",
		HTTPStatus: 409,
	}
}

func bubbleTargetIncompleteError(postingID, topicID int64) error {
	return &apierr.Error{
		Code:       "target_incomplete",
		Message:    fmt.Sprintf("box item %d was present, but its thread ID was missing while looking for topic %d", postingID, topicID),
		Hint:       "No change was attempted. Retry only after a complete box read shows this row with its topic_id.",
		HTTPStatus: 409,
		Meta:       map[string]any{"retryable": true},
	}
}

func bubbleTargetNotFoundError(postingID, topicID int64) error {
	return &apierr.Error{
		Code:       apierr.CodeNotFound,
		Message:    fmt.Sprintf("box item %d / thread %d was not found in Imbox or Bubble Up", postingID, topicID),
		Hint:       "HEY box reads can be transiently incomplete. Retry only after `hey box view imbox --all --json` or `hey box view bubblebox --all --json` shows this exact pair.",
		HTTPStatus: 404,
		Meta:       map[string]any{"retryable": true},
	}
}
