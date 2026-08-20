package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
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
// guesses state from a missing first page.
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
	use := "bubble-up-now <posting-id> --topic-id <topic-id>"
	short := "Bubble one exact posting to the top of the Imbox now"
	long := "Immediately Bubble Up one exact posting. Both posting and topic IDs are required and verified before any mutation."
	example := "  hey bubble-up-now 1232578819 --topic-id 2101829422\n  hey bubble-up-now 1232578819 --topic-id 2101829422 --json"
	agentNotes := "Requires an exact posting/topic pair. Reads Imbox and Bubble Up fully, uses the SDK posting operation, and verifies the resulting state. Repeating an already-applied action is a no-op."
	if action == popAction {
		use = "pop <posting-id> --topic-id <topic-id>"
		short = "Pop one exact posting out of Bubble Up"
		long = "Remove one exact posting from Bubble Up. Both posting and topic IDs are required and verified before any mutation."
		example = "  hey pop 1232578819 --topic-id 2101829422\n  hey pop 1232578819 --topic-id 2101829422 --json"
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
	c.cmd.Flags().Int64Var(&c.topicID, "topic-id", 0, "Exact HEY topic ID paired with the posting (required)")
	return c
}

func (c *bubbleActionCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	postingID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || postingID <= 0 {
		return output.ErrUsageHint("invalid posting ID: must be a positive integer", "Pass the posting ID shown by `hey box imbox --all`.")
	}
	if c.topicID <= 0 {
		return output.ErrUsageHint("invalid or missing --topic-id: must be a positive integer", "Pair the posting with the topic ID parsed from its app_url.")
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
		result := bubbleActionResult{
			Action:    string(c.action),
			PostingID: postingID,
			TopicID:   c.topicID,
			NoOp:      true,
			Verified:  true,
			Reason:    reason,
			Before:    before,
			After:     before,
		}
		return c.writeResult(cmd, result)
	}

	mutationErr := c.mutate(ctx, postingID)
	if mutationErr != nil {
		// Bubble Up Now is non-idempotent and the generated SDK does not retry it.
		// A read can still resolve an ambiguous response without replaying it.
		after, verified, _ := c.verify(ctx, postingID)
		if verified {
			result := bubbleActionResult{
				Action:    string(c.action),
				PostingID: postingID,
				TopicID:   c.topicID,
				Changed:   true,
				Verified:  true,
				Reason:    "state verified after an ambiguous mutation response",
				Before:    before,
				After:     after,
			}
			return c.writeResult(cmd, result)
		}
		return &output.Error{
			Code:      "mutation_unconfirmed",
			Message:   fmt.Sprintf("%s was not confirmed: %v", c.displayName(), mutationErr),
			Hint:      "The CLI did not replay the operation. Re-run this command; it will return a no-op if HEY already applied it.",
			Retryable: true,
			Cause:     mutationErr,
		}
	}

	after, verified, verifyErr := c.verify(ctx, postingID)
	if !verified {
		message := fmt.Sprintf("%s returned success, but the exact posting/topic state was not confirmed", c.displayName())
		if verifyErr != nil {
			message += ": " + verifyErr.Error()
		}
		return &output.Error{
			Code:      "verification_failed",
			Message:   message,
			Hint:      "The CLI did not replay the operation. Re-run this command; it will return a no-op if HEY already applied it.",
			Retryable: true,
			Cause:     verifyErr,
		}
	}

	result := bubbleActionResult{
		Action:    string(c.action),
		PostingID: postingID,
		TopicID:   c.topicID,
		Changed:   true,
		Verified:  true,
		Before:    before,
		After:     after,
	}
	return c.writeResult(cmd, result)
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
		return "posting is already bubbled to the top of the Imbox"
	}
	if c.action == popAction && state.InImbox && !state.InBubbleUp && !state.BubbledUp && !state.Scheduled {
		return "posting is already out of Bubble Up"
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
	summary := fmt.Sprintf("%s verified for posting %d / topic %d", c.displayName(), result.PostingID, result.TopicID)
	if result.NoOp {
		summary = fmt.Sprintf("No-op: %s", result.Reason)
	}
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary)
		return nil
	}
	return writeOK(result, output.WithSummary(summary))
}

type bubbleBoxSource struct {
	name  string
	fetch func(context.Context) (*generated.BoxShowResponse, error)
}

func readExactBubblePostingState(ctx context.Context, postingID, topicID int64) (bubblePostingState, error) {
	state := bubblePostingState{PostingID: postingID, TopicID: topicID}
	var conflictPostingID int64
	var conflictTopicID int64
	incompletePosting := false
	sources := []bubbleBoxSource{
		{
			name: "Imbox",
			fetch: func(ctx context.Context) (*generated.BoxShowResponse, error) {
				resp, err := sdk.Boxes().GetImbox(ctx, nil)
				if err != nil {
					return nil, convertSDKError(err)
				}
				return resp, nil
			},
		},
		{
			name: "Bubble Up",
			fetch: func(ctx context.Context) (*generated.BoxShowResponse, error) {
				resp, err := sdk.Boxes().GetBubblebox(ctx, nil)
				if err != nil {
					return nil, convertSDKError(err)
				}
				return resp, nil
			},
		},
	}

	for _, source := range sources {
		resp, err := source.fetch(ctx)
		if err != nil {
			return state, err
		}
		postings, nextURL, err := paginateBoxPostings(ctx, resp, 0, true, fetchNextBoxPage)
		if err != nil {
			return state, err
		}
		if nextURL != "" {
			return state, &output.Error{
				Code:      "target_lookup_incomplete",
				Message:   fmt.Sprintf("%s pagination ended before every posting was read", source.name),
				Hint:      "No mutation was attempted. Retry the command after HEY can return the complete box history.",
				Retryable: true,
			}
		}
		for _, posting := range postings {
			resolvedTopicID := exactPostingTopicID(posting)
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
			if source.name == "Imbox" {
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

func exactPostingTopicID(posting generated.Posting) int64 {
	marker := "/topics/"
	index := strings.LastIndex(posting.AppUrl, marker)
	if index < 0 {
		return 0
	}
	segment := posting.AppUrl[index+len(marker):]
	if end := strings.IndexAny(segment, "/?#"); end >= 0 {
		segment = segment[:end]
	}
	topicID, err := strconv.ParseInt(segment, 10, 64)
	if err != nil || topicID <= 0 {
		return 0
	}
	return topicID
}

func bubbleTargetMismatchError(wantPostingID, wantTopicID, foundPostingID, foundTopicID int64) error {
	return &output.Error{
		Code:       "target_mismatch",
		Message:    fmt.Sprintf("posting/topic pair does not match: requested %d/%d, HEY returned %d/%d", wantPostingID, wantTopicID, foundPostingID, foundTopicID),
		Hint:       "No mutation was attempted. Copy both IDs from the same posting returned by `hey box imbox --all`.",
		HTTPStatus: 409,
	}
}

func bubbleTargetIncompleteError(postingID, topicID int64) error {
	return &output.Error{
		Code:       "target_incomplete",
		Message:    fmt.Sprintf("posting %d was present, but its topic ID was missing while looking for topic %d", postingID, topicID),
		Hint:       "No mutation was attempted. Retry only after a complete box read shows this posting with its /topics/<id> app_url.",
		HTTPStatus: 409,
		Retryable:  true,
	}
}

func bubbleTargetNotFoundError(postingID, topicID int64) error {
	return &output.Error{
		Code:       "not_found",
		Message:    fmt.Sprintf("posting %d / topic %d was not found in Imbox or Bubble Up", postingID, topicID),
		Hint:       "HEY box reads can be transiently incomplete. Retry only after `hey box imbox --all` or `hey box bubblebox --all` shows this exact pair.",
		HTTPStatus: 404,
		Retryable:  true,
	}
}
