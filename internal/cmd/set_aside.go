package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
)

type setAsideCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

var setAsideListing = postingsListing{
	heading:      "Box",
	summary:      boxSummary,
	cursorNotice: boxListing.cursorNotice,
	groupColumn:  true,
	breadcrumbs: []output.Breadcrumb{
		{Action: "read", Command: "hey thread read <thread-id>", Description: "Read an email thread"},
		{Action: "group", Command: "hey set-aside group create <box-item-id>...", Description: "Gather threads into a group"},
		{Action: "move", Command: "hey move <box-item-id> --to <box>", Description: "Move an email thread to another box"},
	},
}

func newSetAsideCommand() *setAsideCommand {
	command := &setAsideCommand{}
	command.cmd = &cobra.Command{
		Use:   "set-aside",
		Short: "List and group email threads in Set Aside",
		Long:  "List the email threads in Set Aside and organize them into groups.",
		Example: `  hey set-aside view
  hey set-aside group list
  hey set-aside group create 12345 67890`,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Args: cobra.NoArgs,
	}
	command.cmd.AddCommand(newSetAsideViewCommand().cmd)
	command.cmd.AddCommand(newSetAsideGroupCommand())
	return command
}

func newSetAsideViewCommand() *setAsideCommand {
	command := &setAsideCommand{}
	command.cmd = &cobra.Command{
		Use:   "view",
		Short: "List email threads in Set Aside",
		Long:  "List the email threads in Set Aside, with the group each one belongs to.",
		Annotations: map[string]string{
			"agent_notes": "Returns the same threads as hey box view set-aside, with each thread's box_group_id. Use id with hey set-aside group create/add/remove and hey move; use topic_id with hey thread read.",
		},
		Example: `  hey set-aside view
  hey set-aside view --all
  hey set-aside view --json`,
		RunE: command.run,
		Args: cobra.NoArgs,
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of threads to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")
	command.cmd.Flags().StringVar(&command.page, "page", "", "Continue from a next_page cursor")
	return command
}

func (c *setAsideCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	box, err := readSetAside(cmd.Context(), boxPageArgument(c.page))
	if err != nil {
		return err
	}

	seed := pageResult[generated.Posting]{Items: box.Postings, Cursor: box.NextHistoryUrl}
	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}

	listing := setAsideListing
	listing.payload = boxPayload(box)
	return listing.write(cmd, mail.BoxSource(box), seed, request, c.page != "")
}

func newSetAsideGroupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "List and manage groups in Set Aside",
		Long:  "Gather Set Aside threads into groups, list the groups, and break them up.",
		Example: `  hey set-aside group list
  hey set-aside group view 42
  hey set-aside group create 12345 67890
  hey set-aside group add 12345 --to 42
  hey set-aside group remove 12345
  hey set-aside group delete 42`,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newSetAsideGroupListCommand().cmd)
	cmd.AddCommand(newSetAsideGroupViewCommand().cmd)
	cmd.AddCommand(newSetAsideGroupCreateCommand())
	cmd.AddCommand(newSetAsideGroupAddCommand().cmd)
	cmd.AddCommand(newSetAsideGroupRemoveCommand())
	cmd.AddCommand(newSetAsideGroupDeleteCommand())
	return cmd
}

type setAsideGroupListCommand struct {
	cmd *cobra.Command
}

type setAsideGroupOutput struct {
	ID          int64 `json:"id"`
	ThreadCount int   `json:"thread_count"`
}

func newSetAsideGroupListCommand() *setAsideGroupListCommand {
	command := &setAsideGroupListCommand{}
	command.cmd = &cobra.Command{
		Use:   "list",
		Short: "List the groups in Set Aside",
		Long:  "List the groups in Set Aside with how many threads each one holds. Groups have no name in HEY; a group is identified by its ID and its threads.",
		Annotations: map[string]string{
			"agent_notes": "Returns group IDs with their thread counts. Use an ID with hey set-aside group view, add and delete.",
		},
		Example: `  hey set-aside group list
  hey set-aside group list --json`,
		RunE: command.run,
		Args: cobra.NoArgs,
	}
	return command
}

func (c *setAsideGroupListCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	boxID, err := setAsideID(cmd.Context())
	if err != nil {
		return err
	}
	groups, err := readSetAsideGroups(cmd.Context(), boxID)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Threads"})
		for _, group := range groups {
			table.addRow([]string{fmt.Sprintf("%d", group.ID), fmt.Sprintf("%d", group.ThreadCount)})
		}
		table.print()
		return nil
	}

	return writeOK(groups,
		output.WithSummary(fmt.Sprintf("%d %s in Set Aside", len(groups), groupNoun(len(groups)))),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "view", Command: "hey set-aside group view <group-id>", Description: "List the threads in a group"},
			output.Breadcrumb{Action: "delete", Command: "hey set-aside group delete <group-id>", Description: "Break a group up"},
		),
	)
}

// readSetAsideGroups reads the group index and then each group's first page for its
// total. The index carries ids alone, so the count costs one request per group.
func readSetAsideGroups(ctx context.Context, boxID int64) ([]setAsideGroupOutput, error) {
	result, err := sdk.Boxes().ListGroups(ctx, boxID)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}

	groups := make([]setAsideGroupOutput, 0)
	if result == nil {
		return groups, nil
	}
	for _, group := range result.BoxGroups {
		page, err := sdk.Boxes().GetGroupPage(ctx, boxID, group.Id, nil)
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		groups = append(groups, setAsideGroupOutput{ID: group.Id, ThreadCount: groupPageTotal(page)})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups, nil
}

type setAsideGroupViewCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

type setAsideGroupViewOutput struct {
	ID         int64                 `json:"id"`
	BoxID      int64                 `json:"box_id"`
	Postings   []sourcePostingOutput `json:"postings"`
	NextPage   string                `json:"next_page,omitempty"`
	TotalCount int                   `json:"total_count"`
}

var setAsideGroupListing = postingsListing{
	heading: "Group",
	summary: func(count int, name string) string {
		return fmt.Sprintf("%d %s in %s", count, threadNoun(count), name)
	},
	cursorNotice: func(shown, total int) string {
		return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads in the group).", shown, total)
	},
	breadcrumbs: []output.Breadcrumb{
		{Action: "read", Command: "hey thread read <thread-id>", Description: "Read an email thread"},
		{Action: "remove", Command: "hey set-aside group remove <box-item-id>", Description: "Take a thread out of its group"},
		{Action: "delete", Command: "hey set-aside group delete <group-id>", Description: "Break the group up"},
	},
}

func newSetAsideGroupViewCommand() *setAsideGroupViewCommand {
	command := &setAsideGroupViewCommand{}
	command.cmd = &cobra.Command{
		Use:   "view <group-id>",
		Short: "List email threads in a Set Aside group",
		Annotations: map[string]string{
			"agent_notes": "The ID comes from hey set-aside group list. Returns the group's threads; use id with hey set-aside group remove and hey move, topic_id with hey thread read. --page continues from the next_page cursor of an earlier listing of the same group.",
		},
		Example: `  hey set-aside group view 42
  hey set-aside group view 42 --all
  hey set-aside group view 42 --json`,
		RunE: command.run,
		Args: usageExactOneArg(),
	}
	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of threads to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")
	command.cmd.Flags().StringVar(&command.page, "page", "", "Continue from a next_page cursor")
	return command
}

func (c *setAsideGroupViewCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	groupID, err := parsePositiveID(args[0], "group")
	if err != nil {
		return err
	}
	boxID, err := setAsideID(cmd.Context())
	if err != nil {
		return err
	}

	read := readSetAsideGroupPage(boxID, groupID)
	first, err := read(cmd.Context(), c.page)
	if err != nil {
		return err
	}

	source := mail.Source{ID: groupID, Name: fmt.Sprintf("Set Aside group %d", groupID)}
	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}
	listing := setAsideGroupListing
	listing.payload = func(_ mail.Source, rows []sourcePostingOutput, nextPage string, total int) any {
		return setAsideGroupViewOutput{ID: groupID, BoxID: boxID, Postings: rows, NextPage: nextPage, TotalCount: total}
	}
	return listing.writePages(cmd, source, first, request, c.page != "", read)
}

func readSetAsideGroupPage(boxID, groupID int64) pageReader[generated.Posting] {
	return func(ctx context.Context, cursor string) (pageResult[generated.Posting], error) {
		var params *generated.GetBoxGroupParams
		if cursor != "" {
			params = &generated.GetBoxGroupParams{Page: &cursor}
		}
		page, err := sdk.Boxes().GetGroupPage(ctx, boxID, groupID, params)
		if err != nil {
			return pageResult[generated.Posting]{}, apierr.FromSDK(err)
		}
		if page == nil || page.Group == nil {
			return pageResult[generated.Posting]{}, apierr.ErrNotFound("group", fmt.Sprintf("%d", groupID))
		}
		return pageResult[generated.Posting]{Items: page.Group.Postings, Cursor: page.NextPage, Total: groupPageTotal(page)}, nil
	}
}

// groupPageTotal is what X-Total-Count said, or the page itself when the header is missing.
func groupPageTotal(page *hey.BoxGroupPage) int {
	if page == nil || page.Group == nil {
		return 0
	}
	return max(page.TotalCount, len(page.Group.Postings))
}

func newSetAsideGroupCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create <box-item-id>...",
		Short: "Gather email threads into a new Set Aside group",
		Annotations: map[string]string{
			"agent_notes": "Accepts box item IDs from hey set-aside view. Threads not yet in Set Aside are moved there. Returns the new group's ID.",
		},
		Example: `  hey set-aside group create 12345
  hey set-aside group create 12345 67890`,
		Args: usageMinOneArg(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			ids, err := parseIntArgs(args)
			if err != nil {
				return err
			}
			boxID, err := setAsideID(cmd.Context())
			if err != nil {
				return err
			}

			group, err := sdk.Boxes().CreateGroup(cmd.Context(), boxID, ids)
			if err != nil {
				return apierr.FromSDK(err)
			}
			if group == nil {
				return apierr.ErrAPI(200, "HEY did not answer with the new group")
			}

			summary := fmt.Sprintf("Group %d created with %d %s", group.Id, len(ids), threadNoun(len(ids)))
			return writeMutation(cmd, summary, map[string]int64{"id": group.Id})
		},
	}
}

type setAsideGroupAddCommand struct {
	cmd *cobra.Command
	to  string
}

func newSetAsideGroupAddCommand() *setAsideGroupAddCommand {
	command := &setAsideGroupAddCommand{}
	command.cmd = &cobra.Command{
		Use:   "add <box-item-id>...",
		Short: "Add email threads to a Set Aside group",
		Annotations: map[string]string{
			"agent_notes": "Accepts box item IDs from hey set-aside view and a group ID from hey set-aside group list. A thread already in another group is moved to this one.",
		},
		Example: `  hey set-aside group add 12345 --to 42
  hey set-aside group add 12345 67890 --to 42`,
		RunE: command.run,
		Args: usageMinOneArg(),
	}
	command.cmd.Flags().StringVar(&command.to, "to", "", "Group ID (required)")
	return command
}

func (c *setAsideGroupAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.to) == "" {
		return apierr.ErrUsage("group is required (use --to <group-id>)")
	}
	groupID, err := parsePositiveID(c.to, "group")
	if err != nil {
		return err
	}
	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}
	boxID, err := setAsideID(cmd.Context())
	if err != nil {
		return err
	}

	if err := sdk.Postings().AddToBoxGroup(cmd.Context(), boxID, groupID, ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s added to group %d", len(ids), threadNoun(len(ids)), groupID), nil)
}

func newSetAsideGroupRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <box-item-id>...",
		Short: "Take email threads out of their Set Aside group",
		Annotations: map[string]string{
			"agent_notes": "Accepts box item IDs from hey set-aside view. The threads stay in Set Aside, outside any group.",
		},
		Example: `  hey set-aside group remove 12345
  hey set-aside group remove 12345 67890`,
		Args: usageMinOneArg(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			ids, err := parseIntArgs(args)
			if err != nil {
				return err
			}

			if err := sdk.Postings().RemoveFromBoxGroup(cmd.Context(), ids...); err != nil {
				return apierr.FromSDK(err)
			}

			return writeMutation(cmd, fmt.Sprintf("%d %s removed from their group", len(ids), threadNoun(len(ids))), nil)
		},
	}
}

func newSetAsideGroupDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <group-id>",
		Short: "Break up a Set Aside group",
		Long:  "Delete a Set Aside group. HEY moves its threads out of Set Aside into Previously Seen; to keep them in Set Aside, use hey set-aside group remove instead.",
		Annotations: map[string]string{
			"agent_notes": "Deleting a group moves its threads to Previously Seen in the Imbox, not back into Set Aside. Use hey set-aside group remove to ungroup threads that should stay set aside.",
		},
		Example: `  hey set-aside group delete 42`,
		Args:    usageExactOneArg(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			groupID, err := parsePositiveID(args[0], "group")
			if err != nil {
				return err
			}
			boxID, err := setAsideID(cmd.Context())
			if err != nil {
				return err
			}

			if err := sdk.Boxes().DeleteGroup(cmd.Context(), boxID, groupID); err != nil {
				return apierr.FromSDK(err)
			}

			return writeMutation(cmd, fmt.Sprintf("Group %d deleted; its threads moved to Previously Seen", groupID), nil)
		},
	}
}

func readSetAside(ctx context.Context, page string) (*generated.BoxShowResponse, error) {
	var cursor *string
	if page != "" {
		cursor = &page
	}
	box, err := sdk.Boxes().GetAsidebox(ctx, &generated.GetAsideboxParams{Page: cursor})
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	return box, nil
}

func setAsideID(ctx context.Context) (int64, error) {
	boxes, err := sdk.Boxes().List(ctx)
	if err != nil {
		return 0, apierr.FromSDK(err)
	}
	if boxes != nil {
		for _, box := range *boxes {
			if strings.EqualFold(box.Kind, hey.BoxKindSetAside) {
				return box.Id, nil
			}
		}
	}
	return 0, apierr.ErrNotFound("box", "set aside")
}

func groupNoun(count int) string {
	if count == 1 {
		return "group"
	}
	return "groups"
}
