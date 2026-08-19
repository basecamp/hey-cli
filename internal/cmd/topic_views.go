package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type topicViewFetcher func(context.Context, *string) (*generated.TopicListResponse, error)

type topicViewCommand struct {
	cmd      *cobra.Command
	page     int
	title    string
	fetch    topicViewFetcher
	emptyMsg string
}

func newSentCommand() *topicViewCommand {
	return newTopicViewCommand(
		"sent",
		"List sent emails",
		"Sent",
		"Returns sent email topics. Use a topic ID with hey threads to read the full conversation.",
		func(ctx context.Context, page *string) (*generated.TopicListResponse, error) {
			return sdk.Topics().GetSent(ctx, &generated.GetSentTopicsParams{Page: page})
		},
	)
}

func newSpammedCommand() *topicViewCommand {
	return newTopicViewCommand(
		"spammed",
		"List emails in Spam",
		"Spam",
		"Returns topics in Spam. This command is read-only; hey spam <id> marks a thread as spam.",
		func(ctx context.Context, page *string) (*generated.TopicListResponse, error) {
			return sdk.Topics().GetSpam(ctx, &generated.GetSpamTopicsParams{Page: page})
		},
	)
}

func newTrashedCommand() *topicViewCommand {
	return newTopicViewCommand(
		"trashed",
		"List emails in Trash",
		"Trash",
		"Returns topics in Trash. This command is read-only; hey trash <id> moves a thread to Trash.",
		func(ctx context.Context, page *string) (*generated.TopicListResponse, error) {
			return sdk.Topics().GetTrash(ctx, &generated.GetTrashTopicsParams{Page: page})
		},
	)
}

func newEverythingCommand() *topicViewCommand {
	return newTopicViewCommand(
		"everything",
		"List all email",
		"Everything",
		"Returns topics from HEY's Everything view. Use a topic ID with hey threads to read the full conversation.",
		func(ctx context.Context, page *string) (*generated.TopicListResponse, error) {
			return sdk.Topics().GetEverything(ctx, &generated.GetEverythingTopicsParams{Page: page})
		},
	)
}

func newTopicViewCommand(name, short, title, agentNotes string, fetch topicViewFetcher) *topicViewCommand {
	viewCommand := &topicViewCommand{
		title:    title,
		fetch:    fetch,
		emptyMsg: fmt.Sprintf("No emails in %s.", title),
	}
	viewCommand.cmd = &cobra.Command{
		Use:   name,
		Short: short,
		Annotations: map[string]string{
			"agent_notes": agentNotes,
		},
		Example: fmt.Sprintf("  hey %s\n  hey %s --page 2\n  hey %s --json", name, name, name),
		RunE:    viewCommand.run,
		Args:    cobra.NoArgs,
	}
	viewCommand.cmd.Flags().IntVar(&viewCommand.page, "page", 1, "Result page (starting at 1)")
	return viewCommand
}

func (c *topicViewCommand) run(cmd *cobra.Command, _ []string) error {
	if c.page < 1 {
		return output.ErrUsage("--page must be at least 1")
	}
	if err := requireAuth(); err != nil {
		return err
	}

	var page *string
	if c.page > 1 {
		value := strconv.Itoa(c.page)
		page = &value
	}
	result, err := c.fetch(cmd.Context(), page)
	if err != nil {
		return convertSDKError(err)
	}
	if result == nil {
		result = &generated.TopicListResponse{}
	}
	topics := result.Topics
	if topics == nil {
		topics = make([]generated.Topic, 0)
	}
	title := result.Title
	if title == "" {
		title = c.title
	}

	if writer.IsStyled() {
		if len(topics) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), c.emptyMsg)
			return nil
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n", title)
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Thread", "Subject", "From", "Date"})
		for _, topic := range topics {
			table.addRow([]string{
				fmt.Sprintf("%d", topic.Id),
				truncate(topic.Name, 48),
				topicViewSender(topic),
				formatDate(topicViewDate(topic)),
			})
		}
		table.print()
		return nil
	}

	return writeOK(topics,
		output.WithSummary(topicViewSummary(len(topics), title)),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "read",
			Command:     "hey threads <id>",
			Description: "Read an email thread",
		}),
	)
}

func topicViewSummary(count int, title string) string {
	noun := "emails"
	if count == 1 {
		noun = "email"
	}
	return fmt.Sprintf("%d %s in %s", count, noun, title)
}

func topicViewSender(topic generated.Topic) string {
	if topic.Creator.Name != "" {
		return topic.Creator.Name
	}
	return topic.Creator.EmailAddress
}

func topicViewDate(topic generated.Topic) time.Time {
	if !topic.ActiveAt.IsZero() {
		return topic.ActiveAt
	}
	if !topic.UpdatedAt.IsZero() {
		return topic.UpdatedAt
	}
	return topic.CreatedAt
}
