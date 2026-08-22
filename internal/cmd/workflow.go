package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type workflowListItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name,omitempty"`
}

type workflowStageView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type workflowDetailView struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	AppURL    string              `json:"app_url,omitempty"`
	Stages    []workflowStageView `json:"stages"`
	CreatedAt time.Time           `json:"created_at,omitempty,omitzero"`
	UpdatedAt time.Time           `json:"updated_at,omitempty,omitzero"`
}

type workflowsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newWorkflowsCommand() *workflowsCommand {
	workflowsCommand := &workflowsCommand{}
	workflowsCommand.cmd = &cobra.Command{
		Use:   "workflows",
		Short: "List your email workflows",
		Annotations: map[string]string{
			"agent_notes": "Returns workflow IDs, names, and linked account IDs. Use an ID with hey workflow; use --account to limit the list to one linked mail account.",
		},
		Example: `  hey workflows
  hey workflows --account 12345
  hey workflows --limit 10
  hey workflows --json`,
		RunE: workflowsCommand.run,
		Args: cobra.NoArgs,
	}
	workflowsCommand.cmd.Flags().IntVar(&workflowsCommand.limit, "limit", 0, "Maximum number of workflows to show")
	workflowsCommand.cmd.Flags().BoolVar(&workflowsCommand.all, "all", false, "Show every workflow (override --limit)")
	return workflowsCommand
}

func (c *workflowsCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	workflows, err := listWorkflows(cmd.Context())
	if err != nil {
		return err
	}
	total := len(workflows)
	if c.limit > 0 && !c.all && len(workflows) > c.limit {
		workflows = workflows[:c.limit]
	}
	notice := output.TruncationNotice(len(workflows), total)

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Name", "Account ID", "Account"})
		for _, workflow := range workflows {
			table.addRow([]string{
				fmt.Sprintf("%d", workflow.ID),
				terminal.SanitizeLine(workflow.Name),
				fmt.Sprintf("%d", workflow.AccountID),
				terminal.SanitizeLine(workflow.AccountName),
			})
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

	return writeOK(workflows,
		output.WithSummary(fmt.Sprintf("%d %s", len(workflows), workflowNoun(len(workflows)))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey workflow <id>",
			Description: "View a workflow and its stages",
		}),
	)
}

func listWorkflows(ctx context.Context) ([]workflowListItem, error) {
	if accountID, ok := sdk.AccountID(); ok {
		return listAccountWorkflows(ctx, accountID)
	}

	identity, err := rootSDK.Identity().GetIdentity(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	if identity == nil {
		return nil, apierr.ErrAPI(0, "HEY returned no identity data")
	}

	items := make([]workflowListItem, 0)
	for _, account := range identity.Accounts {
		if !linkedAccountAccessible(account) {
			continue
		}
		accountItems, err := listAccountWorkflows(ctx, account.Id)
		if err != nil {
			return nil, err
		}
		items = append(items, accountItems...)
	}
	return items, nil
}

func listAccountWorkflows(ctx context.Context, accountID int64) ([]workflowListItem, error) {
	workflows, err := rootSDK.Workflows().List(ctx, accountID)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	items := make([]workflowListItem, 0, len(workflows))
	for _, workflow := range workflows {
		items = append(items, workflowListItem{
			ID:          workflow.ID,
			Name:        workflow.Name,
			AccountID:   accountID,
			AccountName: workflow.AccountName,
		})
	}
	return items, nil
}

type workflowCommand struct {
	cmd *cobra.Command
}

func newWorkflowCommand() *workflowCommand {
	workflowCommand := &workflowCommand{}
	workflowCommand.cmd = &cobra.Command{
		Use:   "workflow <id>",
		Short: "View and manage an email workflow",
		Annotations: map[string]string{
			"agent_notes": "The workflow ID comes from hey workflows. Detail returns stage IDs in position order. Subcommands create, update, delete, stage, add, move, and remove workflows and their threads.",
		},
		Example: `  hey workflow 123
  hey workflow 123 --json
  hey workflow 123 --ids-only`,
		RunE: workflowCommand.run,
		Args: usageExactOneArg(),
	}
	workflowCommand.cmd.AddCommand(newWorkflowCreateCommand().cmd)
	workflowCommand.cmd.AddCommand(newWorkflowUpdateCommand().cmd)
	workflowCommand.cmd.AddCommand(newWorkflowDeleteCommand().cmd)
	workflowCommand.cmd.AddCommand(newWorkflowStageCommand().cmd)
	workflowCommand.cmd.AddCommand(newWorkflowAddCommand().cmd)
	workflowCommand.cmd.AddCommand(newWorkflowMoveCommand().cmd)
	workflowCommand.cmd.AddCommand(newWorkflowRemoveCommand().cmd)
	return workflowCommand
}

func (c *workflowCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parsePositiveID(args[0], "workflow")
	if err != nil {
		return err
	}

	workflow, err := sdk.Workflows().Get(cmd.Context(), workflowID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if workflow == nil {
		return apierr.ErrNotFound("workflow", args[0])
	}
	detail := workflowDetailFromSDK(workflow)

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		fmt.Fprintf(cmd.OutOrStdout(), "Workflow: %s (%d)\n\n", terminal.SanitizeLine(detail.Name), detail.ID)
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Stage ID", "Name"})
		for _, stage := range detail.Stages {
			table.addRow([]string{fmt.Sprintf("%d", stage.ID), terminal.SanitizeLine(stage.Name)})
		}
		table.print()
		return nil
	case output.FormatIDs, output.FormatCount:
		return writeOK(detail.Stages,
			output.WithSummary(fmt.Sprintf("%d %s", len(detail.Stages), stageNoun(len(detail.Stages)))),
		)
	case output.FormatMarkdown:
		return writeWorkflowMarkdown(cmd, detail)
	default:
		return writeOK(detail,
			output.WithSummary(fmt.Sprintf("Workflow %d with %d %s", detail.ID, len(detail.Stages), stageNoun(len(detail.Stages)))),
			output.WithBreadcrumbs(
				output.Breadcrumb{Action: "add", Command: "hey workflow add <topic-id> --to <workflow-id> --stage <stage-id>", Description: "Add a thread to a stage"},
				output.Breadcrumb{Action: "move", Command: "hey workflow move <topic-id> --workflow <workflow-id> --to <stage-id>", Description: "Move a thread to another stage"},
			),
		)
	}
}

func workflowDetailFromSDK(workflow *generated.Workflow) workflowDetailView {
	stages := make([]workflowStageView, 0, len(workflow.Stages))
	for _, stage := range workflow.Stages {
		stages = append(stages, workflowStageView{ID: stage.Id, Name: stage.Name})
	}
	return workflowDetailView{
		ID:        workflow.Id,
		Name:      workflow.Name,
		AppURL:    workflow.AppUrl,
		Stages:    stages,
		CreatedAt: workflow.CreatedAt,
		UpdatedAt: workflow.UpdatedAt,
	}
}

func writeWorkflowMarkdown(cmd *cobra.Command, workflow workflowDetailView) error {
	var document strings.Builder
	fmt.Fprintf(&document, "# %s\n\n", markdownSafeText(workflow.Name))
	fmt.Fprintf(&document, "**ID:** %d\n\n", workflow.ID)
	if len(workflow.Stages) == 0 {
		document.WriteString("(no stages)\n")
	} else {
		document.WriteString("| stage_id | name |\n")
		document.WriteString("| --- | --- |\n")
		for _, stage := range workflow.Stages {
			fmt.Fprintf(&document, "| %d | %s |\n", stage.ID, markdownSafeText(stage.Name))
		}
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), document.String())
	return err
}

type workflowCreateCommand struct {
	cmd *cobra.Command
}

func newWorkflowCreateCommand() *workflowCreateCommand {
	workflowCreateCommand := &workflowCreateCommand{}
	workflowCreateCommand.cmd = &cobra.Command{
		Use:   "create <name>",
		Short: "Create an email workflow",
		Example: `  hey workflow create "Hiring"
  hey workflow create "Sales pipeline" --account 12345`,
		RunE: workflowCreateCommand.run,
		Args: usageExactOneArg(),
	}
	return workflowCreateCommand
}

func (c *workflowCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	name := strings.TrimSpace(args[0])
	if name == "" {
		return apierr.ErrUsage("workflow name is required")
	}
	accountID, err := workflowCreationAccountID(cmd.Context())
	if err != nil {
		return err
	}
	if err := sdk.Workflows().Create(cmd.Context(), name, accountID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Workflow %q created", name), nil,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "list", Command: "hey workflows", Description: "Find the new workflow ID"}),
	)
}

func workflowCreationAccountID(ctx context.Context) (int64, error) {
	if accountID, ok := sdk.AccountID(); ok {
		return accountID, nil
	}
	identity, err := rootSDK.Identity().GetIdentity(ctx)
	if err != nil {
		return 0, apierr.FromSDK(err)
	}
	if identity == nil {
		return 0, apierr.ErrAPI(0, "HEY returned no identity data")
	}
	var accountID int64
	for _, account := range identity.Accounts {
		if !linkedAccountAccessible(account) {
			continue
		}
		if accountID != 0 {
			return 0, apierr.ErrUsageHint("workflow creation requires one linked mail account", "select one with --account <id> or `hey accounts use <id>`")
		}
		accountID = account.Id
	}
	if accountID == 0 {
		return 0, apierr.ErrAPI(0, "HEY returned no available linked mail account")
	}
	return accountID, nil
}

type workflowUpdateCommand struct {
	cmd  *cobra.Command
	name string
}

func newWorkflowUpdateCommand() *workflowUpdateCommand {
	workflowUpdateCommand := &workflowUpdateCommand{}
	workflowUpdateCommand.cmd = &cobra.Command{
		Use:     "update <id>",
		Aliases: []string{"edit", "rename"},
		Short:   "Rename an email workflow",
		Example: `  hey workflow update 123 --name "Recruiting"`,
		RunE:    workflowUpdateCommand.run,
		Args:    usageExactOneArg(),
	}
	workflowUpdateCommand.cmd.Flags().StringVar(&workflowUpdateCommand.name, "name", "", "New workflow name (required)")
	return workflowUpdateCommand
}

func (c *workflowUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parsePositiveID(args[0], "workflow")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(c.name)
	if name == "" {
		return apierr.ErrUsage("workflow name is required (use --name <name>)")
	}
	if err := sdk.Workflows().Update(cmd.Context(), workflowID, name); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Workflow %d renamed to %q", workflowID, name), nil)
}

type workflowDeleteCommand struct {
	cmd *cobra.Command
}

func newWorkflowDeleteCommand() *workflowDeleteCommand {
	workflowDeleteCommand := &workflowDeleteCommand{}
	workflowDeleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete an email workflow",
		Example: `  hey workflow delete 123`,
		RunE:    workflowDeleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return workflowDeleteCommand
}

func (c *workflowDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parsePositiveID(args[0], "workflow")
	if err != nil {
		return err
	}
	if err := sdk.Workflows().Delete(cmd.Context(), workflowID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Workflow %d deleted", workflowID), nil)
}

type workflowStageCommand struct {
	cmd *cobra.Command
}

func newWorkflowStageCommand() *workflowStageCommand {
	workflowStageCommand := &workflowStageCommand{}
	workflowStageCommand.cmd = &cobra.Command{
		Use:   "stage",
		Short: "Manage workflow stages",
		Annotations: map[string]string{
			"agent_notes": "Create adds an Untitled stage; use hey workflow <id> to find its stage ID, then update it with --name.",
		},
	}
	workflowStageCommand.cmd.AddCommand(newWorkflowStageCreateCommand().cmd)
	workflowStageCommand.cmd.AddCommand(newWorkflowStageUpdateCommand().cmd)
	workflowStageCommand.cmd.AddCommand(newWorkflowStageDeleteCommand().cmd)
	return workflowStageCommand
}

type workflowStageCreateCommand struct {
	cmd *cobra.Command
}

func newWorkflowStageCreateCommand() *workflowStageCreateCommand {
	workflowStageCreateCommand := &workflowStageCreateCommand{}
	workflowStageCreateCommand.cmd = &cobra.Command{
		Use:     "create <workflow-id>",
		Short:   "Add an Untitled stage to a workflow",
		Example: `  hey workflow stage create 123`,
		RunE:    workflowStageCreateCommand.run,
		Args:    usageExactOneArg(),
	}
	return workflowStageCreateCommand
}

func (c *workflowStageCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parsePositiveID(args[0], "workflow")
	if err != nil {
		return err
	}
	if err := sdk.Workflows().CreateStage(cmd.Context(), workflowID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Untitled stage added to workflow %d", workflowID), nil,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "view", Command: fmt.Sprintf("hey workflow %d", workflowID), Description: "Find the new stage ID"}),
	)
}

type workflowStageUpdateCommand struct {
	cmd  *cobra.Command
	name string
}

func newWorkflowStageUpdateCommand() *workflowStageUpdateCommand {
	workflowStageUpdateCommand := &workflowStageUpdateCommand{}
	workflowStageUpdateCommand.cmd = &cobra.Command{
		Use:     "update <workflow-id> <stage-id>",
		Aliases: []string{"edit", "rename"},
		Short:   "Rename a workflow stage",
		Example: `  hey workflow stage update 123 456 --name "Interviewing"`,
		RunE:    workflowStageUpdateCommand.run,
		Args:    usageExactArgs(2),
	}
	workflowStageUpdateCommand.cmd.Flags().StringVar(&workflowStageUpdateCommand.name, "name", "", "New stage name (required)")
	return workflowStageUpdateCommand
}

func (c *workflowStageUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parsePositiveID(args[0], "workflow")
	if err != nil {
		return err
	}
	stageID, err := parsePositiveID(args[1], "stage")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(c.name)
	if name == "" {
		return apierr.ErrUsage("stage name is required (use --name <name>)")
	}
	if err := sdk.Workflows().UpdateStage(cmd.Context(), workflowID, stageID, name); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Stage %d renamed to %q", stageID, name), nil)
}

type workflowStageDeleteCommand struct {
	cmd *cobra.Command
}

func newWorkflowStageDeleteCommand() *workflowStageDeleteCommand {
	workflowStageDeleteCommand := &workflowStageDeleteCommand{}
	workflowStageDeleteCommand.cmd = &cobra.Command{
		Use:     "delete <workflow-id> <stage-id>",
		Short:   "Delete a workflow stage",
		Example: `  hey workflow stage delete 123 456`,
		RunE:    workflowStageDeleteCommand.run,
		Args:    usageExactArgs(2),
	}
	return workflowStageDeleteCommand
}

func (c *workflowStageDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parsePositiveID(args[0], "workflow")
	if err != nil {
		return err
	}
	stageID, err := parsePositiveID(args[1], "stage")
	if err != nil {
		return err
	}
	if err := sdk.Workflows().DeleteStage(cmd.Context(), workflowID, stageID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Stage %d deleted from workflow %d", stageID, workflowID), nil)
}

type workflowAddCommand struct {
	cmd   *cobra.Command
	to    string
	stage string
}

func newWorkflowAddCommand() *workflowAddCommand {
	workflowAddCommand := &workflowAddCommand{}
	workflowAddCommand.cmd = &cobra.Command{
		Use:   "add <topic-id>...",
		Short: "Add email threads to a workflow stage",
		Example: `  hey workflow add 501 --to 123 --stage 456
  hey workflow add 501 502 --to 123 --stage 456`,
		RunE: workflowAddCommand.run,
		Args: usageMinOneArg(),
	}
	workflowAddCommand.cmd.Flags().StringVar(&workflowAddCommand.to, "to", "", "Workflow ID (required)")
	workflowAddCommand.cmd.Flags().StringVar(&workflowAddCommand.stage, "stage", "", "Stage ID (required)")
	return workflowAddCommand
}

func (c *workflowAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parseRequiredWorkflowFlag(c.to, "--to <workflow-id>")
	if err != nil {
		return err
	}
	stageID, err := parseRequiredStageFlag(c.stage, "--stage <stage-id>")
	if err != nil {
		return err
	}
	topicIDs, err := parsePositiveTopicIDs(args)
	if err != nil {
		return err
	}
	for _, topicID := range topicIDs {
		if err := sdk.Workflows().StageTopic(cmd.Context(), topicID, workflowID, stageID); err != nil {
			return apierr.FromSDK(err)
		}
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s added to workflow %d stage %d", len(topicIDs), threadNoun(len(topicIDs)), workflowID, stageID), nil)
}

type workflowMoveCommand struct {
	cmd      *cobra.Command
	workflow string
	to       string
}

func newWorkflowMoveCommand() *workflowMoveCommand {
	workflowMoveCommand := &workflowMoveCommand{}
	workflowMoveCommand.cmd = &cobra.Command{
		Use:   "move <topic-id>...",
		Short: "Move email threads to another workflow stage",
		Example: `  hey workflow move 501 --workflow 123 --to 789
  hey workflow move 501 502 --workflow 123 --to 789`,
		RunE: workflowMoveCommand.run,
		Args: usageMinOneArg(),
	}
	workflowMoveCommand.cmd.Flags().StringVar(&workflowMoveCommand.workflow, "workflow", "", "Workflow ID (required)")
	workflowMoveCommand.cmd.Flags().StringVar(&workflowMoveCommand.to, "to", "", "Destination stage ID (required)")
	return workflowMoveCommand
}

func (c *workflowMoveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parseRequiredWorkflowFlag(c.workflow, "--workflow <workflow-id>")
	if err != nil {
		return err
	}
	stageID, err := parseRequiredStageFlag(c.to, "--to <stage-id>")
	if err != nil {
		return err
	}
	topicIDs, err := parsePositiveTopicIDs(args)
	if err != nil {
		return err
	}
	for _, topicID := range topicIDs {
		if err := sdk.Workflows().MoveTopic(cmd.Context(), topicID, workflowID, stageID); err != nil {
			return apierr.FromSDK(err)
		}
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s moved to workflow %d stage %d", len(topicIDs), threadNoun(len(topicIDs)), workflowID, stageID), nil)
}

type workflowRemoveCommand struct {
	cmd  *cobra.Command
	from string
}

func newWorkflowRemoveCommand() *workflowRemoveCommand {
	workflowRemoveCommand := &workflowRemoveCommand{}
	workflowRemoveCommand.cmd = &cobra.Command{
		Use:   "remove <topic-id>...",
		Short: "Remove email threads from a workflow",
		Example: `  hey workflow remove 501 --from 123
  hey workflow remove 501 502 --from 123`,
		RunE: workflowRemoveCommand.run,
		Args: usageMinOneArg(),
	}
	workflowRemoveCommand.cmd.Flags().StringVar(&workflowRemoveCommand.from, "from", "", "Workflow ID (required)")
	return workflowRemoveCommand
}

func (c *workflowRemoveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	workflowID, err := parseRequiredWorkflowFlag(c.from, "--from <workflow-id>")
	if err != nil {
		return err
	}
	topicIDs, err := parsePositiveTopicIDs(args)
	if err != nil {
		return err
	}
	for _, topicID := range topicIDs {
		if err := sdk.Workflows().UnstageTopic(cmd.Context(), topicID, workflowID); err != nil {
			return apierr.FromSDK(err)
		}
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s removed from workflow %d", len(topicIDs), threadNoun(len(topicIDs)), workflowID), nil)
}

func parseRequiredWorkflowFlag(value, usage string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, apierr.ErrUsage("workflow is required (use " + usage + ")")
	}
	return parsePositiveID(value, "workflow")
}

func parseRequiredStageFlag(value, usage string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, apierr.ErrUsage("stage is required (use " + usage + ")")
	}
	return parsePositiveID(value, "stage")
}

func workflowNoun(count int) string {
	if count == 1 {
		return "workflow"
	}
	return "workflows"
}

func stageNoun(count int) string {
	if count == 1 {
		return "stage"
	}
	return "stages"
}
