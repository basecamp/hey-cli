package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	internalfolders "github.com/basecamp/hey-cli/internal/folders"
	"github.com/basecamp/hey-cli/internal/output"
)

type labelsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newLabelsCommand() *labelsCommand {
	labelsCommand := &labelsCommand{}
	labelsCommand.cmd = &cobra.Command{
		Use:   "labels",
		Short: "List your email labels",
		Annotations: map[string]string{
			"agent_notes": "Returns label IDs and names. Use an ID with hey label and hey label add/remove.",
		},
		Example: `  hey labels
  hey labels --limit 10
  hey labels --json`,
		RunE: labelsCommand.run,
	}

	labelsCommand.cmd.Flags().IntVar(&labelsCommand.limit, "limit", 0, "Maximum number of labels to show")
	labelsCommand.cmd.Flags().BoolVar(&labelsCommand.all, "all", false, "Show all results (override --limit)")

	return labelsCommand
}

func (c *labelsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	folders, err := internalfolders.List(cmd.Context(), sdk)
	if err != nil {
		return convertSDKError(err)
	}

	total := len(folders)
	if c.limit > 0 && !c.all && len(folders) > c.limit {
		folders = folders[:c.limit]
	}
	notice := output.TruncationNotice(len(folders), total)

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Name"})
		for _, label := range folders {
			table.addRow([]string{fmt.Sprintf("%d", label.ID), terminalSafeText(label.Name)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(folders,
		output.WithSummary(fmt.Sprintf("%d %s", len(folders), labelNoun(len(folders)))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey label <id>",
			Description: "View email threads with a label",
		}),
	)
}

type labelCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

type folderOutput struct {
	ID         int64                 `json:"id"`
	Name       string                `json:"name,omitempty"`
	AppURL     string                `json:"app_url,omitempty"`
	CreatedAt  *time.Time            `json:"created_at,omitempty"`
	UpdatedAt  *time.Time            `json:"updated_at,omitempty"`
	Postings   []folderPostingOutput `json:"postings"`
	NextPage   string                `json:"next_page,omitempty"`
	TotalCount int                   `json:"total_count"`
}

type folderPostingOutput struct {
	generated.Posting
	TopicID int64 `json:"topic_id,omitempty"`
}

func newLabelCommand() *labelCommand {
	labelCommand := &labelCommand{}
	labelCommand.cmd = &cobra.Command{
		Use:   "label <id>",
		Short: "View and manage an email label",
		Annotations: map[string]string{
			"agent_notes": "The ID comes from hey labels. Returns labeled email threads; subcommands add, create, and remove labels.",
		},
		Example: `  hey label 123
  hey label 123 --limit 10
  hey label 123 --json`,
		RunE: labelCommand.run,
		Args: usageExactOneArg(),
	}

	labelCommand.cmd.Flags().IntVar(&labelCommand.limit, "limit", 0, "Maximum number of threads to show")
	labelCommand.cmd.Flags().BoolVar(&labelCommand.all, "all", false, "Fetch all results (override --limit)")
	labelCommand.cmd.Flags().StringVar(&labelCommand.page, "page", "", "Continue from a next_page cursor")
	labelCommand.cmd.AddCommand(newLabelAddCommand().cmd)
	labelCommand.cmd.AddCommand(newLabelCreateCommand().cmd)
	labelCommand.cmd.AddCommand(newLabelRemoveCommand().cmd)

	return labelCommand
}

func (c *labelCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	folderID, err := parsePositiveID(args[0], "label")
	if err != nil {
		return err
	}

	var params *generated.GetFolderParams
	if c.page != "" {
		params = &generated.GetFolderParams{Page: &c.page}
	}
	page, err := sdk.Folders().GetPage(cmd.Context(), folderID, params)
	if err != nil {
		return convertSDKError(err)
	}
	if page == nil || page.Folder == nil {
		return output.ErrNotFound("label", args[0])
	}

	folder, nextPage, total, err := paginateFolder(cmd.Context(), folderID, page, c.limit, c.all)
	if err != nil {
		return err
	}
	notice := folderTruncationNotice(len(folder.Postings), total, nextPage != "", c.all, c.page != "")

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Label: %s\n\n", terminalSafeText(folder.Name))
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Thread", "From", "Summary", "Date"})
		for _, posting := range folder.Postings {
			topicID := resolvePostingTopicID(posting)
			creator := terminalSafeText(posting.Creator.Name)
			summary := truncate(terminalSafeText(posting.Summary), 60)
			table.addRow([]string{fmt.Sprintf("%d", posting.Id), fmt.Sprintf("%d", topicID), creator, summary, formatDate(posting.CreatedAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(makeFolderOutput(folder, nextPage, total),
		output.WithSummary(fmt.Sprintf("%d %s labeled %s", len(folder.Postings), threadNoun(len(folder.Postings)), folder.Name)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "read", Command: "hey threads <id>", Description: "Read an email thread"},
			output.Breadcrumb{Action: "add_label", Command: "hey label add <id> --to <label-id>", Description: "Add another label to a thread"},
			output.Breadcrumb{Action: "remove_label", Command: "hey label remove <id> --from <label-id|all>", Description: "Remove labels from a thread"},
		),
	)
}

type labelAddCommand struct {
	cmd *cobra.Command
	to  string
}

func newLabelAddCommand() *labelAddCommand {
	labelAddCommand := &labelAddCommand{}
	labelAddCommand.cmd = &cobra.Command{
		Use:   "add <id>...",
		Short: "Add a label to email threads",
		Example: `  hey label add 12345 --to 789
  hey label add 12345 67890 --to 789`,
		RunE: labelAddCommand.run,
		Args: usageMinOneArg(),
	}
	labelAddCommand.cmd.Flags().StringVar(&labelAddCommand.to, "to", "", "Label ID (required)")
	return labelAddCommand
}

func (c *labelAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.to) == "" {
		return output.ErrUsage("label is required (use --to <label-id>)")
	}

	folderID, err := parsePositiveID(c.to, "label")
	if err != nil {
		return err
	}
	postingIDs, err := parsePositivePostingIDs(args)
	if err != nil {
		return err
	}
	if err := sdk.Postings().File(cmd.Context(), folderID, postingIDs...); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("Label %d added to %d %s", folderID, len(postingIDs), threadNoun(len(postingIDs)))
	return writeLabelMutation(cmd, summary)
}

type labelCreateCommand struct {
	cmd *cobra.Command
}

func newLabelCreateCommand() *labelCreateCommand {
	labelCreateCommand := &labelCreateCommand{}
	labelCreateCommand.cmd = &cobra.Command{
		Use:   "create <name> <id>...",
		Short: "Create a label and add it to email threads",
		Example: `  hey label create "Travel receipts" 12345
  hey label create "Project Apollo" 12345 67890`,
		RunE: labelCreateCommand.run,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return usageErrorf("%s <name> <id>...", cmd.CommandPath())
			}
			return nil
		},
	}
	return labelCreateCommand
}

func (c *labelCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return output.ErrUsage("label name is required")
	}
	postingIDs, err := parsePositivePostingIDs(args[1:])
	if err != nil {
		return err
	}
	if err := sdk.Postings().CreateFolder(cmd.Context(), name, postingIDs...); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("Label %q created and added to %d %s", name, len(postingIDs), threadNoun(len(postingIDs)))
	return writeLabelMutation(cmd, summary)
}

type labelRemoveCommand struct {
	cmd  *cobra.Command
	from string
}

func newLabelRemoveCommand() *labelRemoveCommand {
	labelRemoveCommand := &labelRemoveCommand{}
	labelRemoveCommand.cmd = &cobra.Command{
		Use:   "remove <id>...",
		Short: "Remove labels from email threads",
		Example: `  hey label remove 12345 --from 789
  hey label remove 12345 67890 --from all`,
		RunE: labelRemoveCommand.run,
		Args: usageMinOneArg(),
	}
	labelRemoveCommand.cmd.Flags().StringVar(&labelRemoveCommand.from, "from", "", "Label ID, or all to remove every label (required)")
	return labelRemoveCommand
}

func (c *labelRemoveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	from := strings.TrimSpace(c.from)
	if from == "" {
		return output.ErrUsage("label is required (use --from <label-id|all>)")
	}
	folderID := int64(0)
	if !strings.EqualFold(from, "all") {
		var err error
		folderID, err = parsePositiveID(from, "label")
		if err != nil {
			return err
		}
	}
	postingIDs, err := parsePositivePostingIDs(args)
	if err != nil {
		return err
	}
	if err := sdk.Postings().Unfile(cmd.Context(), folderID, postingIDs...); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("Label %d removed from %d %s", folderID, len(postingIDs), threadNoun(len(postingIDs)))
	if folderID == 0 {
		summary = fmt.Sprintf("All labels removed from %d %s", len(postingIDs), threadNoun(len(postingIDs)))
	}
	return writeLabelMutation(cmd, summary)
}

func paginateFolder(ctx context.Context, folderID int64, first *hey.FolderPage, limit int, all bool) (*generated.FolderWithPostings, string, int, error) {
	folder := *first.Folder
	folder.Postings = append([]generated.Posting(nil), first.Folder.Postings...)
	nextPage := first.NextPage
	total := max(first.TotalCount, len(folder.Postings))

	needMore := all || (limit > 0 && len(folder.Postings) < limit)
	for page := 1; page <= maxAdditionalPages && needMore && nextPage != ""; page++ {
		cursor := nextPage
		result, err := sdk.Folders().GetPage(ctx, folderID, &generated.GetFolderParams{Page: &cursor})
		if err != nil {
			return nil, "", 0, convertSDKError(err)
		}
		if result == nil || result.Folder == nil {
			return nil, "", 0, fmt.Errorf("label %d page %q returned no data", folderID, cursor)
		}
		folder.Postings = append(folder.Postings, result.Folder.Postings...)
		nextPage = result.NextPage
		total = max(total, result.TotalCount, len(folder.Postings))
		needMore = all || (limit > 0 && len(folder.Postings) < limit)
	}

	if limit > 0 && !all && len(folder.Postings) > limit {
		folder.Postings = folder.Postings[:limit]
		nextPage = ""
	}
	return &folder, nextPage, total, nil
}

func folderTruncationNotice(shown, total int, hasMore, all, fromCursor bool) string {
	if all {
		if hasMore {
			return fmt.Sprintf("Showing %d results. Pagination limit reached; continue with --page using next_page.", shown)
		}
		if shown < total {
			if fromCursor {
				return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads with the label).", shown, total)
			}
			return fmt.Sprintf("Showing %d of %d results; HEY returned no additional page cursor.", shown, total)
		}
		return ""
	}
	if shown < total {
		return fmt.Sprintf("Showing %d of %d results. Use --all to see everything.", shown, total)
	}
	if hasMore {
		return fmt.Sprintf("Showing %d results. More available; use --all to fetch all.", shown)
	}
	return ""
}

func makeFolderOutput(folder *generated.FolderWithPostings, nextPage string, total int) folderOutput {
	postings := make([]folderPostingOutput, len(folder.Postings))
	for i, posting := range folder.Postings {
		postings[i] = folderPostingOutput{Posting: posting, TopicID: resolvePostingTopicID(posting)}
	}
	return folderOutput{
		ID:         folder.Id,
		Name:       folder.Name,
		AppURL:     folder.AppUrl,
		CreatedAt:  nonZeroTime(folder.CreatedAt),
		UpdatedAt:  nonZeroTime(folder.UpdatedAt),
		Postings:   postings,
		NextPage:   nextPage,
		TotalCount: total,
	}
}

func nonZeroTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func writeLabelMutation(cmd *cobra.Command, summary string) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), terminalSafeText(summary)+".")
		return nil
	}
	return writeOK(nil, output.WithSummary(summary))
}

func parsePositiveID(value, kind string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, output.ErrUsage(fmt.Sprintf("invalid %s ID: %s", kind, value))
	}
	return id, nil
}

func parsePositivePostingIDs(values []string) ([]int64, error) {
	ids := make([]int64, len(values))
	seen := make(map[int64]bool, len(values))
	for i, value := range values {
		id, err := parsePositiveID(value, "thread")
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, output.ErrUsage(fmt.Sprintf("duplicate thread ID: %d", id))
		}
		seen[id] = true
		ids[i] = id
	}
	return ids, nil
}

func labelNoun(count int) string {
	if count == 1 {
		return "label"
	}
	return "labels"
}
