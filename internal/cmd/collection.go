package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type collectionsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newCollectionListCommand() *collectionsCommand {
	command := newCollectionsListingCommand("list", `  hey collection list
  hey collection list --limit 10
  hey collection list --json`)
	command.cmd.Args = cobra.NoArgs
	return command
}

func newCollectionsListingCommand(use, example string) *collectionsCommand {
	command := &collectionsCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: "List your email collections",
		Annotations: map[string]string{
			"agent_notes": "Returns collection IDs and names. Use an ID with hey collection view and hey collection add/remove/update.",
		},
		Example: example,
		RunE:    command.run,
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of collections to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")

	return command
}

func (c *collectionsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	result, err := sdk.Collections().List(cmd.Context())
	if err != nil {
		return apierr.FromSDK(err)
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
			table.addRow([]string{fmt.Sprintf("%d", collection.Id), terminal.SanitizeLine(collection.Name)})
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
			Command:     "hey collection view <id>",
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

var collectionListing = postingsListing{
	heading: "Collection",
	summary: func(count int, name string) string {
		return fmt.Sprintf("%d %s in %s", count, threadNoun(count), name)
	},
	cursorNotice: func(shown, total int) string {
		return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads in the collection).", shown, total)
	},
	breadcrumbs: []output.Breadcrumb{
		{Action: "read", Command: "hey thread read <thread-id>", Description: "Read an email thread"},
		{Action: "add_to_collection", Command: "hey collection add <thread-id> --to <collection-id>", Description: "Add a thread to a collection"},
		{Action: "remove_from_collection", Command: "hey collection remove <thread-id> --from <collection-id>", Description: "Remove a thread from a collection"},
	},
}

func newCollectionCommand() *collectionCommand {
	command := newCollectionReaderCommand(
		"collection",
		"List and manage email collections",
		`  hey collection list
  hey collection view 123
  hey collection view 123 --all
  hey collection view 123 --json`,
	)
	command.cmd.Annotations[compatibilityUsageAnnotation] = "collection <id>"
	command.cmd.Args = cobra.MaximumNArgs(1)
	command.cmd.AddCommand(newCollectionListCommand().cmd)
	command.cmd.AddCommand(newCollectionViewCommand().cmd)
	command.cmd.AddCommand(newCollectionAddCommand().cmd)
	command.cmd.AddCommand(newCollectionCreateCommand().cmd)
	command.cmd.AddCommand(newCollectionRemoveCommand().cmd)
	command.cmd.AddCommand(newCollectionUpdateCommand().cmd)
	return command
}

func newCollectionViewCommand() *collectionCommand {
	return newCollectionReaderCommand(
		"view <id>",
		"List email threads in a collection",
		`  hey collection view 123
  hey collection view 123 --page next-cursor
  hey collection view 123 --all
  hey collection view 123 --json`,
	)
}

func newCollectionReaderCommand(use, short, example string) *collectionCommand {
	command := &collectionCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: short,
		Annotations: map[string]string{
			"agent_notes": "The ID comes from hey collection list. Detail returns posting IDs for organization actions and topic_id for reading threads, and answers --json, --styled, --markdown, --ids-only and --count.",
		},
		Example: example,
		RunE:    command.run,
		Args:    usageExactOneArg(),
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of threads to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")
	command.cmd.Flags().StringVar(&command.page, "page", "", "Continue from a next_page cursor")
	return command
}

func (c *collectionCommand) run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
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
	first, err := sdk.Collections().GetPage(cmd.Context(), collectionID, params)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if first == nil || first.Collection == nil {
		return apierr.ErrNotFound("collection", args[0])
	}

	seed := pageResult[generated.Posting]{Items: first.Collection.Postings, Cursor: first.NextPage, Total: first.TotalCount}
	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}
	return collectionListing.write(cmd, mail.CollectionSource(first.Collection), seed, request, c.page != "")
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
		return apierr.ErrUsage("collection name is required")
	}
	accountID, _ := sdk.AccountID()
	if err := sdk.Collections().Create(cmd.Context(), hey.CreateCollectionParams{
		Name:      name,
		Summary:   strings.TrimSpace(c.summary),
		AccountID: accountID,
	}); err != nil {
		return apierr.FromSDK(err)
	}

	return writeCollectionMutation(cmd, fmt.Sprintf("Collection %q created", name), output.Breadcrumb{
		Action:      "list",
		Command:     "hey collection list",
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
		return apierr.ErrUsage("at least one of --name or --summary is required")
	}

	params := hey.UpdateCollectionParams{}
	if nameChanged {
		params.Name = strings.TrimSpace(c.name)
		if params.Name == "" {
			return apierr.ErrUsage("collection name is required")
		}
	}
	if summaryChanged {
		params.Summary = strings.TrimSpace(c.summary)
		if params.Summary == "" {
			return apierr.ErrUsage("collection summary is required")
		}
	}
	if err := sdk.Collections().Update(cmd.Context(), collectionID, params); err != nil {
		return apierr.FromSDK(err)
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
		Use:   "add <thread-id>...",
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
		return apierr.ErrUsage("collection is required (use --to <collection-id>)")
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
			return apierr.FromSDK(err)
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
		Use:   "remove <thread-id>...",
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
		return apierr.ErrUsage("collection is required (use --from <collection-id>)")
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
			return apierr.FromSDK(err)
		}
	}

	summary := fmt.Sprintf("%d %s removed from collection %d", len(topicIDs), threadNoun(len(topicIDs)), collectionID)
	return writeCollectionMutation(cmd, summary)
}

func writeCollectionMutation(cmd *cobra.Command, summary string, breadcrumbs ...output.Breadcrumb) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), terminal.SanitizeLine(summary)+".")
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
			return nil, apierr.ErrUsage(fmt.Sprintf("invalid topic ID: %s", value))
		}
		if seen[id] {
			return nil, apierr.ErrUsage(fmt.Sprintf("duplicate topic ID: %d", id))
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
