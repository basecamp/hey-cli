package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	internalfolders "github.com/basecamp/hey-cli/internal/folders"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type labelsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newLabelsCommand() *labelsCommand {
	command := newLabelsListingCommand("labels", `  hey labels
  hey labels --limit 10
  hey labels --json`)
	command.cmd.Annotations[compatibilityForAnnotation] = "label list"
	return command
}

func newLabelListCommand() *labelsCommand {
	command := newLabelsListingCommand("list", `  hey label list
  hey label list --limit 10
  hey label list --json`)
	command.cmd.Args = cobra.NoArgs
	return command
}

func newLabelsListingCommand(use, example string) *labelsCommand {
	command := &labelsCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: "List your email labels",
		Annotations: map[string]string{
			"agent_notes": "Returns label IDs and names. Use an ID with hey label view and hey label add/remove.",
		},
		Example: example,
		RunE:    command.run,
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of labels to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")

	return command
}

func (c *labelsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	folders, err := internalfolders.List(cmd.Context(), sdk)
	if err != nil {
		return apierr.FromSDK(err)
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
			table.addRow([]string{fmt.Sprintf("%d", label.ID), terminal.SanitizeLine(label.Name)})
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
			Command:     "hey label view <id>",
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

var labelListing = postingsListing{
	heading: "Label",
	summary: func(count int, name string) string {
		return fmt.Sprintf("%d %s labeled %s", count, threadNoun(count), name)
	},
	cursorNotice: func(shown, total int) string {
		return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads with the label).", shown, total)
	},
	breadcrumbs: []output.Breadcrumb{
		{Action: "read", Command: "hey threads <id>", Description: "Read an email thread"},
		{Action: "add_label", Command: "hey label add <id> --to <label-id>", Description: "Add another label to a thread"},
		{Action: "remove_label", Command: "hey label remove <id> --from <label-id|all>", Description: "Remove labels from a thread"},
	},
}

func newLabelCommand() *labelCommand {
	command := newLabelReaderCommand(
		"label",
		"List and manage email labels",
		`  hey label list
  hey label view 123
  hey label view 123 --all
  hey label view 123 --json`,
	)
	command.cmd.Annotations[compatibilityUsageAnnotation] = "label <id>"
	command.cmd.Args = cobra.MaximumNArgs(1)
	command.cmd.AddCommand(newLabelListCommand().cmd)
	command.cmd.AddCommand(newLabelViewCommand().cmd)
	command.cmd.AddCommand(newLabelAddCommand().cmd)
	command.cmd.AddCommand(newLabelCreateCommand().cmd)
	command.cmd.AddCommand(newLabelRemoveCommand().cmd)
	return command
}

func newLabelViewCommand() *labelCommand {
	return newLabelReaderCommand(
		"view <id>",
		"List email threads with a label",
		`  hey label view 123
  hey label view 123 --page next-cursor
  hey label view 123 --all
  hey label view 123 --json`,
	)
}

func newLabelReaderCommand(use, short, example string) *labelCommand {
	command := &labelCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: short,
		Annotations: map[string]string{
			"agent_notes": "The ID comes from hey label list. Returns labeled email threads with topic_id for reading them, and answers --json, --styled, --markdown, --ids-only and --count.",
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

func (c *labelCommand) run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
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
	first, err := sdk.Folders().GetPage(cmd.Context(), folderID, params)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if first == nil || first.Folder == nil {
		return apierr.ErrNotFound("label", args[0])
	}

	seed := pageResult[generated.Posting]{Items: first.Folder.Postings, Cursor: first.NextPage, Total: first.TotalCount}
	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}
	return labelListing.write(cmd, mail.FolderSource(first.Folder), seed, request, c.page != "")
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
		return apierr.ErrUsage("label is required (use --to <label-id>)")
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
		return apierr.FromSDK(err)
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
		Args: usageMinArgs(2),
	}
	return labelCreateCommand
}

func (c *labelCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return apierr.ErrUsage("label name is required")
	}
	postingIDs, err := parsePositivePostingIDs(args[1:])
	if err != nil {
		return err
	}
	if err := sdk.Postings().CreateFolder(cmd.Context(), name, postingIDs...); err != nil {
		return apierr.FromSDK(err)
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
		return apierr.ErrUsage("label is required (use --from <label-id|all>)")
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
		return apierr.FromSDK(err)
	}

	summary := fmt.Sprintf("Label %d removed from %d %s", folderID, len(postingIDs), threadNoun(len(postingIDs)))
	if folderID == 0 {
		summary = fmt.Sprintf("All labels removed from %d %s", len(postingIDs), threadNoun(len(postingIDs)))
	}
	return writeLabelMutation(cmd, summary)
}

func writeLabelMutation(cmd *cobra.Command, summary string) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), terminal.SanitizeLine(summary)+".")
		return nil
	}
	return writeOK(nil, output.WithSummary(summary))
}

func parsePositiveID(value, kind string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, apierr.ErrUsage(fmt.Sprintf("invalid %s ID: %s", kind, value))
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
			return nil, apierr.ErrUsage(fmt.Sprintf("duplicate thread ID: %d", id))
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
