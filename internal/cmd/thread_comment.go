package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// threadCommentCommand posts a private internal note on a thread. This is not in the
// SDK's OpenAPI surface, so it goes straight through Client.PostForm rather than a typed
// service — the same way Collections and Publications reach the form endpoints HEY has
// no JSON for.
type threadCommentCommand struct {
	cmd     *cobra.Command
	message string
}

func newThreadCommentCommand() *threadCommentCommand {
	commentCommand := &threadCommentCommand{}
	commentCommand.cmd = &cobra.Command{
		Use:     "comment <thread-id>",
		Short:   "Add a private internal note to a thread",
		Long:    "Add a private internal note to a thread. This is visible only to you and anyone else on the account — it is not mailed to anyone. Use hey reply to send a mailed reply instead.",
		Example: `  hey thread comment 12345 -m "Following up with accounting on this."`,
		Annotations: map[string]string{
			"agent_notes": "Posts a private internal note on the thread, not a mailed reply — use hey reply for that. Accepts the topic_id from hey box view, hey label view, or hey search output. The note is plain text; Markdown is not converted.",
		},
		RunE: commentCommand.run,
		Args: usageExactOneArg(),
	}
	commentCommand.cmd.Flags().StringVarP(&commentCommand.message, "message", "m", "", "Note text (required)")

	return commentCommand
}

func (c *threadCommentCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := parsePositiveID(args[0], "thread")
	if err != nil {
		return err
	}
	message := strings.TrimSpace(c.message)
	if message == "" {
		return apierr.ErrUsage("--message is required")
	}

	topic, err := rootSDK.Topics().Get(cmd.Context(), threadID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if topic == nil || topic.AccountId == 0 {
		return apierr.ErrAPI(0, fmt.Sprintf("thread %d did not identify its mail account", threadID))
	}

	values := url.Values{}
	values.Set("comment[content]", message)
	path := fmt.Sprintf("/topics/%d/comments?account_id=%d", threadID, topic.AccountId)
	if _, err := rootSDK.PostForm(cmd.Context(), path, values); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("Comment added to thread %d", threadID), map[string]any{"thread_id": threadID},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "read", Command: fmt.Sprintf("hey thread read %d", threadID), Description: "Read this thread"}),
	)
}
