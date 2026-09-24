package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// maxSentPages bounds one invocation even if the server returns a bad pagination loop.
const maxSentPages = 100

type sentCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

type sentRecipient struct {
	Name         string `json:"name,omitempty"`
	EmailAddress string `json:"email_address"`
}

type sentMessage struct {
	ID      int64           `json:"id"`
	Subject string          `json:"subject"`
	To      []sentRecipient `json:"to"`
	CC      []sentRecipient `json:"cc"`
	BCC     []sentRecipient `json:"bcc"`
	Summary string          `json:"summary"`
	SentAt  *time.Time      `json:"sent_at"`
	AppURL  string          `json:"app_url"`
}

type sentTableRow struct {
	ID         int64  `json:"id"`
	Subject    string `json:"subject"`
	Recipients string `json:"recipients"`
	Summary    string `json:"summary"`
	Sent       string `json:"sent"`
	AppURL     string `json:"app_url"`
}

func newSentCommand() *sentCommand {
	sentCommand := &sentCommand{}
	sentCommand.cmd = &cobra.Command{
		Use:   "sent",
		Short: "List sent email",
		Long: `List the latest message you sent in each thread, newest first.

One page arrives by default. --limit reads pages until it has enough messages, and
--all reads up to 100 pages. Recipient lists in JSON preserve To, CC, and BCC separately.`,
		Annotations: map[string]string{
			"agent_notes": "Returns outbound mail with thread IDs, exact To/CC/BCC recipients, sent times, and HEY app URLs. IDs are thread IDs for `hey thread read <id>`.",
		},
		Example: `  hey sent
  hey sent --limit 10 --json
  hey sent --all --json
  hey sent --ids-only`,
		RunE: sentCommand.run,
		Args: cobra.NoArgs,
	}

	sentCommand.cmd.Flags().IntVar(&sentCommand.limit, "limit", 0, "Maximum number of sent messages to show")
	sentCommand.cmd.Flags().BoolVar(&sentCommand.all, "all", false, "Fetch up to 100 results pages (override --limit)")
	sentCommand.cmd.Flags().StringVar(&sentCommand.page, "page", "", "Results page cursor")
	return sentCommand
}

func (c *sentCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.limit < 0 {
		return apierr.ErrUsage("--limit must be at least 0")
	}

	first, err := readSentPage(cmd.Context(), c.page)
	if err != nil {
		return err
	}
	collected, err := collectPages(cmd.Context(), first, pageRequest{Limit: c.limit, All: c.all, MaxPages: maxSentPages}, readSentPage)
	if err != nil {
		return err
	}

	messages := makeSentMessages(collected.Items)
	nextPage := collected.Cursor
	notice := sentListingNotice(len(messages), collected.Read, nextPage, collected.Truncated, c.all)
	if c.limit > 0 && !c.all && len(messages) > c.limit {
		messages = messages[:c.limit]
		nextPage = ""
		if collected.Cursor != "" {
			notice = sentListingNotice(len(messages), collected.Read, collected.Cursor, false, false)
		} else {
			notice = output.TruncationNotice(len(messages), len(collected.Items))
		}
	}

	format := writer.EffectiveFormat()
	if stderrNotice := paginationNoticeForStderr(format, notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	switch format {
	case output.FormatStyled:
		return writeSentStyled(cmd, messages, notice)
	case output.FormatMarkdown:
		return writeOK(makeSentTableRows(messages))
	default:
		opts := []output.ResponseOption{
			output.WithSummary(fmt.Sprintf("%d %s", len(messages), sentMessageNoun(len(messages)))),
			output.WithNotice(notice),
			output.WithMeta("page", c.page),
			output.WithMeta("pages_fetched", collected.Read),
			output.WithBreadcrumbs(output.Breadcrumb{
				Action:      "read",
				Command:     "hey thread read <id>",
				Description: "Read a sent email thread",
			}),
		}
		if nextPage != "" {
			opts = append(opts, output.WithMeta("next_page", nextPage))
		}
		return writeOK(messages, opts...)
	}
}

func readSentPage(ctx context.Context, cursor string) (pageResult[generated.Topic], error) {
	page, err := sdk.Topics().GetSentPage(ctx, cursor)
	if err != nil {
		return pageResult[generated.Topic]{}, apierr.FromSDK(err)
	}
	if page == nil {
		return pageResult[generated.Topic]{}, nil
	}
	return pageResult[generated.Topic]{Items: page.Topics, Cursor: page.NextPage}, nil
}

func makeSentMessages(topics []generated.Topic) []sentMessage {
	messages := make([]sentMessage, 0, len(topics))
	for _, topic := range topics {
		entry := topic.LatestEntry
		var sentAt *time.Time
		switch {
		case !entry.ActiveAt.IsZero():
			activeAt := entry.ActiveAt
			sentAt = &activeAt
		case !entry.CreatedAt.IsZero():
			createdAt := entry.CreatedAt
			sentAt = &createdAt
		}
		messages = append(messages, sentMessage{
			ID:      topic.Id,
			Subject: topic.Name,
			To:      makeSentRecipients(entry.Addressed.Directly),
			CC:      makeSentRecipients(entry.Addressed.Copied),
			BCC:     makeSentRecipients(entry.Addressed.Blindcopied),
			Summary: entry.Summary,
			SentAt:  sentAt,
			AppURL:  topic.AppUrl,
		})
	}
	return messages
}

func makeSentRecipients(contacts []generated.Contact) []sentRecipient {
	recipients := make([]sentRecipient, len(contacts))
	for i, contact := range contacts {
		recipients[i] = sentRecipient{Name: contact.Name, EmailAddress: contact.EmailAddress}
	}
	return recipients
}

func writeSentStyled(cmd *cobra.Command, messages []sentMessage, notice string) error {
	if len(messages) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No sent messages.")
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}

	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"Thread", "Subject", "Recipients", "Summary", "Sent"})
	for _, message := range messages {
		table.addRow([]string{
			strconv.FormatInt(message.ID, 10),
			truncate(message.Subject, 42),
			truncate(sentRecipientSummary(message), 32),
			truncate(message.Summary, 52),
			formatSentTimestamp(message.SentAt),
		})
	}
	table.print()
	if notice != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
	}
	return nil
}

func makeSentTableRows(messages []sentMessage) []sentTableRow {
	rows := make([]sentTableRow, len(messages))
	for i, message := range messages {
		rows[i] = sentTableRow{
			ID:         message.ID,
			Subject:    message.Subject,
			Recipients: sentRecipientSummary(message),
			Summary:    message.Summary,
			Sent:       formatSentTimestamp(message.SentAt),
			AppURL:     message.AppURL,
		}
	}
	return rows
}

func sentRecipientSummary(message sentMessage) string {
	recipients := make([]sentRecipient, 0, len(message.To)+len(message.CC)+len(message.BCC))
	recipients = append(recipients, message.To...)
	recipients = append(recipients, message.CC...)
	recipients = append(recipients, message.BCC...)
	if len(recipients) == 0 {
		return "Me"
	}

	name := strings.TrimSpace(recipients[0].Name)
	if name == "" {
		name = recipients[0].EmailAddress
	}
	if len(recipients) == 1 {
		return "Me → " + name
	}
	return fmt.Sprintf("Me → %s + %d", name, len(recipients)-1)
}

func formatSentTimestamp(sentAt *time.Time) string {
	if sentAt == nil {
		return "Unavailable"
	}
	return formatTimestamp(sentAt.Local())
}

func sentListingNotice(shown, pages int, nextPage string, truncated, all bool) string {
	switch {
	case truncated:
		return fmt.Sprintf("Sent listing stopped after %d pages. Continue with --page %s.", pages, terminal.SanitizeLine(nextPage))
	case all && nextPage != "":
		return fmt.Sprintf("Showing %d %s. Continue with --page %s.", shown, sentMessageNoun(shown), terminal.SanitizeLine(nextPage))
	case nextPage != "":
		return fmt.Sprintf("Showing %d %s. Use --all to see everything.", shown, sentMessageNoun(shown))
	default:
		return ""
	}
}

func sentMessageNoun(count int) string {
	if count == 1 {
		return "sent message"
	}
	return "sent messages"
}
