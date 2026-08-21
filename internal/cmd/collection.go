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

	"github.com/basecamp/hey-cli/internal/output"
)

type collectionsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newCollectionsCommand() *collectionsCommand {
	collectionsCommand := &collectionsCommand{}
	collectionsCommand.cmd = &cobra.Command{
		Use:   "collections",
		Short: "List your email collections",
		Annotations: map[string]string{
			"agent_notes": "Returns collection IDs and names. Use an ID with hey collection and hey collection add/remove/update.",
		},
		Example: `  hey collections
  hey collections --limit 10
  hey collections --json`,
		RunE: collectionsCommand.run,
	}

	collectionsCommand.cmd.Flags().IntVar(&collectionsCommand.limit, "limit", 0, "Maximum number of collections to show")
	collectionsCommand.cmd.Flags().BoolVar(&collectionsCommand.all, "all", false, "Show all results (override --limit)")

	return collectionsCommand
}

func (c *collectionsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	result, err := sdk.Collections().List(cmd.Context())
	if err != nil {
		return convertSDKError(err)
	}

	collections := make([]generated.Collection, 0)
	if result != nil {
		collections = append(collections, (*result)...)
	}
	total := len(collections)
	if c.limit > 0 && !c.all && len(collections) > c.limit {
		collections = collections[:c.limit]
	}
	notice := output.TruncationNotice(len(collections), total)

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Name"})
		for _, collection := range collections {
			table.addRow([]string{fmt.Sprintf("%d", collection.Id), terminalSafeText(collection.Name)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	return writeOK(collections,
		output.WithSummary(fmt.Sprintf("%d %s", len(collections), collectionNoun(len(collections)))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey collection <id>",
			Description: "View email threads in a collection",
		}),
	)
}

type collectionCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

type collectionOutput struct {
	ID         int64                     `json:"id"`
	Name       string                    `json:"name,omitempty"`
	AppURL     string                    `json:"app_url,omitempty"`
	CreatedAt  *time.Time                `json:"created_at,omitempty"`
	UpdatedAt  *time.Time                `json:"updated_at,omitempty"`
	Postings   []collectionPostingOutput `json:"postings"`
	NextPage   string                    `json:"next_page,omitempty"`
	TotalCount int                       `json:"total_count"`
}

type collectionPostingOutput struct {
	generated.Posting
	TopicID int64 `json:"topic_id,omitempty"`
}

type collectionPostingMarkdown struct {
	ID      int64  `json:"id"`
	TopicID int64  `json:"topic_id,omitempty"`
	From    string `json:"from,omitempty"`
	Summary string `json:"summary,omitempty"`
	Date    string `json:"date,omitempty"`
}

func newCollectionCommand() *collectionCommand {
	collectionCommand := &collectionCommand{}
	collectionCommand.cmd = &cobra.Command{
		Use:   "collection <id>",
		Short: "View and manage an email collection",
		Annotations: map[string]string{
			"agent_notes": "The ID comes from hey collections. Detail returns posting IDs for organization actions and topic_id for reading threads and collection membership changes.",
		},
		Example: `  hey collection 123
  hey collection 123 --page next-cursor
  hey collection 123 --all
  hey collection 123 --json`,
		RunE: collectionCommand.run,
		Args: usageExactOneArg(),
	}

	collectionCommand.cmd.Flags().IntVar(&collectionCommand.limit, "limit", 0, "Maximum number of threads to show")
	collectionCommand.cmd.Flags().BoolVar(&collectionCommand.all, "all", false, "Fetch all results (override --limit)")
	collectionCommand.cmd.Flags().StringVar(&collectionCommand.page, "page", "", "Continue from a next_page cursor")
	collectionCommand.cmd.AddCommand(newCollectionAddCommand().cmd)
	collectionCommand.cmd.AddCommand(newCollectionCreateCommand().cmd)
	collectionCommand.cmd.AddCommand(newCollectionRemoveCommand().cmd)
	collectionCommand.cmd.AddCommand(newCollectionUpdateCommand().cmd)

	return collectionCommand
}

func (c *collectionCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	collectionID, err := parsePositiveID(args[0], "collection")
	if err != nil {
		return err
	}

	var params *generated.GetCollectionParams
	if c.page != "" {
		params = &generated.GetCollectionParams{Page: &c.page}
	}
	page, err := sdk.Collections().GetPage(cmd.Context(), collectionID, params)
	if err != nil {
		return convertSDKError(err)
	}
	if page == nil || page.Collection == nil {
		return output.ErrNotFound("collection", args[0])
	}

	collection, nextPage, total, err := paginateCollection(cmd.Context(), collectionID, page, c.limit, c.all)
	if err != nil {
		return err
	}
	notice := collectionTruncationNotice(len(collection.Postings), total, nextPage != "", c.all, c.page != "")

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		return writeStyledCollection(cmd, collection, notice)
	case output.FormatIDs, output.FormatCount:
		if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
		}
		if nextPage != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "next_page: %s\n", terminalSafeText(nextPage))
		}
		return writeOK(collection.Postings)
	case output.FormatMarkdown:
		return writeMarkdownCollection(cmd, collection, nextPage, total, notice)
	default:
		return writeOK(makeCollectionOutput(collection, nextPage, total),
			output.WithSummary(fmt.Sprintf("%d %s in %s", len(collection.Postings), threadNoun(len(collection.Postings)), collection.Name)),
			output.WithNotice(notice),
			output.WithBreadcrumbs(
				output.Breadcrumb{Action: "read", Command: "hey threads <topic-id>", Description: "Read an email thread"},
				output.Breadcrumb{Action: "add_to_collection", Command: "hey collection add <topic-id> --to <collection-id>", Description: "Add a thread to a collection"},
				output.Breadcrumb{Action: "remove_from_collection", Command: "hey collection remove <topic-id> --from <collection-id>", Description: "Remove a thread from a collection"},
			),
		)
	}
}

func writeStyledCollection(cmd *cobra.Command, collection *generated.CollectionWithPostings, notice string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "Collection: %s\n\n", terminalSafeText(collection.Name))
	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"ID", "Thread", "From", "Summary", "Date"})
	for _, posting := range collection.Postings {
		topicID := ""
		if id := resolvePostingTopicID(posting); id != 0 {
			topicID = fmt.Sprintf("%d", id)
		}
		table.addRow([]string{
			fmt.Sprintf("%d", posting.Id),
			topicID,
			terminalSafeText(posting.Creator.Name),
			truncate(terminalSafeText(posting.Summary), 60),
			formatDate(posting.CreatedAt),
		})
	}
	table.print()
	if notice != "" {
		fmt.Fprintln(cmd.OutOrStdout(), notice)
	}
	return nil
}

func writeMarkdownCollection(cmd *cobra.Command, collection *generated.CollectionWithPostings, nextPage string, total int, notice string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n", markdownSafeText(collection.Name))
	rows := make([]collectionPostingMarkdown, len(collection.Postings))
	for i, posting := range collection.Postings {
		rows[i] = collectionPostingMarkdown{
			ID:      posting.Id,
			TopicID: resolvePostingTopicID(posting),
			From:    posting.Creator.Name,
			Summary: posting.Summary,
			Date:    formatDate(posting.CreatedAt),
		}
	}
	if err := writeOK(rows); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\n**Total threads:** %d\n", total)
	if nextPage != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "**Next page:** `%s`\n", terminalSafeText(nextPage))
	}
	if notice != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", markdownSafeText(notice))
	}
	return nil
}

type collectionCreateCommand struct {
	cmd     *cobra.Command
	summary string
}

func newCollectionCreateCommand() *collectionCreateCommand {
	collectionCreateCommand := &collectionCreateCommand{}
	collectionCreateCommand.cmd = &cobra.Command{
		Use:   "create <name>",
		Short: "Create a collection",
		Example: `  hey collection create "Kitchen remodel"
  hey collection create "Project Apollo" --summary "Messages and decisions for the project"`,
		RunE: collectionCreateCommand.run,
		Args: usageExactOneArg(),
	}
	collectionCreateCommand.cmd.Flags().StringVar(&collectionCreateCommand.summary, "summary", "", "Description shown with the collection")
	return collectionCreateCommand
}

func (c *collectionCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return output.ErrUsage("collection name is required")
	}
	accountID, _ := sdk.AccountID()
	if err := sdk.Collections().Create(cmd.Context(), hey.CreateCollectionParams{
		Name:      name,
		Summary:   strings.TrimSpace(c.summary),
		AccountID: accountID,
	}); err != nil {
		return convertSDKError(err)
	}

	return writeCollectionMutation(cmd, fmt.Sprintf("Collection %q created", name), output.Breadcrumb{
		Action:      "list",
		Command:     "hey collections",
		Description: "Find the new collection ID",
	})
}

type collectionUpdateCommand struct {
	cmd     *cobra.Command
	name    string
	summary string
}

func newCollectionUpdateCommand() *collectionUpdateCommand {
	collectionUpdateCommand := &collectionUpdateCommand{}
	collectionUpdateCommand.cmd = &cobra.Command{
		Use:     "update <id>",
		Aliases: []string{"edit"},
		Short:   "Update a collection",
		Example: `  hey collection update 123 --name "Kitchen renovation"
  hey collection update 123 --summary "Invoices and contractor decisions"`,
		RunE: collectionUpdateCommand.run,
		Args: usageExactOneArg(),
	}
	collectionUpdateCommand.cmd.Flags().StringVar(&collectionUpdateCommand.name, "name", "", "New collection name")
	collectionUpdateCommand.cmd.Flags().StringVar(&collectionUpdateCommand.summary, "summary", "", "New collection description")
	return collectionUpdateCommand
}

func (c *collectionUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	collectionID, err := parsePositiveID(args[0], "collection")
	if err != nil {
		return err
	}
	nameChanged := cmd.Flags().Changed("name")
	summaryChanged := cmd.Flags().Changed("summary")
	if !nameChanged && !summaryChanged {
		return output.ErrUsage("at least one of --name or --summary is required")
	}

	params := hey.UpdateCollectionParams{}
	if nameChanged {
		params.Name = strings.TrimSpace(c.name)
		if params.Name == "" {
			return output.ErrUsage("collection name is required")
		}
	}
	if summaryChanged {
		params.Summary = strings.TrimSpace(c.summary)
		if params.Summary == "" {
			return output.ErrUsage("collection summary is required")
		}
	}
	if err := sdk.Collections().Update(cmd.Context(), collectionID, params); err != nil {
		return convertSDKError(err)
	}

	return writeCollectionMutation(cmd, fmt.Sprintf("Collection %d updated", collectionID))
}

type collectionAddCommand struct {
	cmd *cobra.Command
	to  string
}

func newCollectionAddCommand() *collectionAddCommand {
	collectionAddCommand := &collectionAddCommand{}
	collectionAddCommand.cmd = &cobra.Command{
		Use:   "add <topic-id>...",
		Short: "Add email threads to a collection",
		Example: `  hey collection add 501 --to 123
  hey collection add 501 502 --to 123`,
		RunE: collectionAddCommand.run,
		Args: usageMinOneArg(),
	}
	collectionAddCommand.cmd.Flags().StringVar(&collectionAddCommand.to, "to", "", "Collection ID (required)")
	return collectionAddCommand
}

func (c *collectionAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.to) == "" {
		return output.ErrUsage("collection is required (use --to <collection-id>)")
	}
	collectionID, err := parsePositiveID(c.to, "collection")
	if err != nil {
		return err
	}
	topicIDs, err := parsePositiveTopicIDs(args)
	if err != nil {
		return err
	}
	for _, topicID := range topicIDs {
		if err := sdk.Collections().AddTopic(cmd.Context(), topicID, collectionID); err != nil {
			return convertSDKError(err)
		}
	}

	summary := fmt.Sprintf("%d %s added to collection %d", len(topicIDs), threadNoun(len(topicIDs)), collectionID)
	return writeCollectionMutation(cmd, summary)
}

type collectionRemoveCommand struct {
	cmd  *cobra.Command
	from string
}

func newCollectionRemoveCommand() *collectionRemoveCommand {
	collectionRemoveCommand := &collectionRemoveCommand{}
	collectionRemoveCommand.cmd = &cobra.Command{
		Use:   "remove <topic-id>...",
		Short: "Remove email threads from a collection",
		Example: `  hey collection remove 501 --from 123
  hey collection remove 501 502 --from 123`,
		RunE: collectionRemoveCommand.run,
		Args: usageMinOneArg(),
	}
	collectionRemoveCommand.cmd.Flags().StringVar(&collectionRemoveCommand.from, "from", "", "Collection ID (required)")
	return collectionRemoveCommand
}

func (c *collectionRemoveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.from) == "" {
		return output.ErrUsage("collection is required (use --from <collection-id>)")
	}
	collectionID, err := parsePositiveID(c.from, "collection")
	if err != nil {
		return err
	}
	topicIDs, err := parsePositiveTopicIDs(args)
	if err != nil {
		return err
	}
	for _, topicID := range topicIDs {
		if err := sdk.Collections().RemoveTopic(cmd.Context(), topicID, collectionID); err != nil {
			return convertSDKError(err)
		}
	}

	summary := fmt.Sprintf("%d %s removed from collection %d", len(topicIDs), threadNoun(len(topicIDs)), collectionID)
	return writeCollectionMutation(cmd, summary)
}

func paginateCollection(ctx context.Context, collectionID int64, first *hey.CollectionPage, limit int, all bool) (*generated.CollectionWithPostings, string, int, error) {
	collection := *first.Collection
	collection.Postings = append([]generated.Posting(nil), first.Collection.Postings...)
	nextPage := first.NextPage
	total := max(first.TotalCount, len(collection.Postings))

	needMore := all || (limit > 0 && len(collection.Postings) < limit)
	for page := 1; page <= maxAdditionalPages && needMore && nextPage != ""; page++ {
		cursor := nextPage
		result, err := sdk.Collections().GetPage(ctx, collectionID, &generated.GetCollectionParams{Page: &cursor})
		if err != nil {
			return nil, "", 0, convertSDKError(err)
		}
		if result == nil || result.Collection == nil {
			return nil, "", 0, fmt.Errorf("collection %d page %q returned no data", collectionID, cursor)
		}
		collection.Postings = append(collection.Postings, result.Collection.Postings...)
		nextPage = result.NextPage
		total = max(total, result.TotalCount, len(collection.Postings))
		needMore = all || (limit > 0 && len(collection.Postings) < limit)
	}

	if limit > 0 && !all && len(collection.Postings) > limit {
		collection.Postings = collection.Postings[:limit]
		nextPage = ""
	}
	return &collection, nextPage, total, nil
}

func collectionTruncationNotice(shown, total int, hasMore, all, fromCursor bool) string {
	if all {
		if hasMore {
			return fmt.Sprintf("Showing %d results. Pagination limit reached; continue with --page using next_page.", shown)
		}
		if shown < total {
			if fromCursor {
				return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads in the collection).", shown, total)
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

func makeCollectionOutput(collection *generated.CollectionWithPostings, nextPage string, total int) collectionOutput {
	postings := make([]collectionPostingOutput, len(collection.Postings))
	for i, posting := range collection.Postings {
		postings[i] = collectionPostingOutput{Posting: posting, TopicID: resolvePostingTopicID(posting)}
	}
	return collectionOutput{
		ID:         collection.Id,
		Name:       collection.Name,
		AppURL:     collection.AppUrl,
		CreatedAt:  nonZeroTime(collection.CreatedAt),
		UpdatedAt:  nonZeroTime(collection.UpdatedAt),
		Postings:   postings,
		NextPage:   nextPage,
		TotalCount: total,
	}
}

func writeCollectionMutation(cmd *cobra.Command, summary string, breadcrumbs ...output.Breadcrumb) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), terminalSafeText(summary)+".")
		return nil
	}
	options := []output.ResponseOption{output.WithSummary(summary)}
	if len(breadcrumbs) > 0 {
		options = append(options, output.WithBreadcrumbs(breadcrumbs...))
	}
	return writeOK(nil, options...)
}

func parsePositiveTopicIDs(values []string) ([]int64, error) {
	ids := make([]int64, len(values))
	seen := make(map[int64]bool, len(values))
	for i, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, output.ErrUsage(fmt.Sprintf("invalid topic ID: %s", value))
		}
		if seen[id] {
			return nil, output.ErrUsage(fmt.Sprintf("duplicate topic ID: %d", id))
		}
		seen[id] = true
		ids[i] = id
	}
	return ids, nil
}

func collectionNoun(count int) string {
	if count == 1 {
		return "collection"
	}
	return "collections"
}
