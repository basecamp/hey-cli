package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type snippetsCommand struct {
	cmd *cobra.Command
}

func newSnippetListCommand() *snippetsCommand {
	return newSnippetsListingCommand("list", `  hey snippet list
  hey snippet list --json
  hey snippet list --ids-only`)
}

func newSnippetsListingCommand(use, example string) *snippetsCommand {
	command := &snippetsCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: "List reusable email snippets",
		Annotations: map[string]string{
			"agent_notes": "Returns snippet IDs, names, plain text, and rich-text HTML. Use an ID with hey snippet update or delete.",
		},
		Example: example,
		RunE:    command.run,
		Args:    cobra.NoArgs,
	}
	return command
}

func (c *snippetsCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	snippets, err := sdk.Snippets().List(cmd.Context())
	if err != nil {
		return apierr.FromSDK(err)
	}

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		if len(snippets) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No snippets found")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Name", "Content", "Updated"})
		for _, snippet := range snippets {
			table.addRow([]string{
				fmt.Sprintf("%d", snippet.Id),
				terminal.SanitizeLine(snippet.Name),
				truncate(terminal.SanitizeLine(snippet.Content), 60),
				formatDate(snippet.UpdatedAt),
			})
		}
		table.print()
		return nil
	case output.FormatMarkdown:
		return writeSnippetsMarkdown(cmd, snippets)
	default:
		return writeOK(snippets,
			output.WithSummary(fmt.Sprintf("%d %s", len(snippets), snippetNoun(len(snippets)))),
			output.WithBreadcrumbs(
				output.Breadcrumb{Action: "create", Command: "hey snippet create --name <name> --content <content>", Description: "Create a snippet"},
				output.Breadcrumb{Action: "update", Command: "hey snippet update <id> --name <name> --content <content>", Description: "Update a snippet"},
			),
		)
	}
}

func writeSnippetsMarkdown(cmd *cobra.Command, snippets []generated.Snippet) error {
	if len(snippets) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "(no results)")
		return err
	}
	var document strings.Builder
	document.WriteString("| id | name | content | updated |\n")
	document.WriteString("| --- | --- | --- | --- |\n")
	for _, snippet := range snippets {
		fmt.Fprintf(&document, "| %d | %s | %s | %s |\n",
			snippet.Id,
			markdownSafeText(snippet.Name),
			markdownSafeText(snippet.Content),
			formatDate(snippet.UpdatedAt),
		)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), document.String())
	return err
}

func snippetNoun(count int) string {
	if count == 1 {
		return "snippet"
	}
	return "snippets"
}

type snippetCommand struct {
	cmd *cobra.Command
}

func newSnippetCommand() *snippetCommand {
	snippetCommand := &snippetCommand{}
	snippetCommand.cmd = &cobra.Command{
		Use:   "snippet",
		Short: "List and manage reusable email snippets",
		Annotations: map[string]string{
			"agent_notes": "List, create, update, or delete snippets. Find snippet IDs with hey snippet list.",
		},
	}
	snippetCommand.cmd.AddCommand(newSnippetListCommand().cmd)
	snippetCommand.cmd.AddCommand(newSnippetCreateCommand().cmd)
	snippetCommand.cmd.AddCommand(newSnippetUpdateCommand().cmd)
	snippetCommand.cmd.AddCommand(newSnippetDeleteCommand().cmd)
	return snippetCommand
}

type snippetCreateCommand struct {
	cmd         *cobra.Command
	name        string
	content     string
	contentHTML string
}

func newSnippetCreateCommand() *snippetCreateCommand {
	createCommand := &snippetCreateCommand{}
	createCommand.cmd = &cobra.Command{
		Use:     "create",
		Aliases: []string{"add"},
		Short:   "Create a reusable email snippet",
		Example: `  hey snippet create --name "Scheduling reply" --content "Tuesday works for me."
  hey snippet create --name "Office hours" --content "<p>Office hours are Monday through Thursday.</p>"`,
		RunE: createCommand.run,
		Args: cobra.NoArgs,
	}
	createCommand.cmd.Flags().StringVar(&createCommand.name, "name", "", "Snippet name (required)")
	createCommand.cmd.Flags().StringVar(&createCommand.content, "content", "", "Snippet content as Markdown")
	createCommand.cmd.Flags().StringVar(&createCommand.contentHTML, "content-html", "", "Snippet content as raw HTML instead of Markdown")
	createCommand.cmd.MarkFlagsMutuallyExclusive("content", "content-html")
	return createCommand
}

func (c *snippetCreateCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	name := strings.TrimSpace(c.name)
	if name == "" {
		return apierr.ErrUsage("--name is required")
	}
	content := c.contentHTML
	if content == "" {
		content = htmlutil.FromMarkdown(c.content)
	}
	if strings.TrimSpace(content) == "" {
		return apierr.ErrUsage("--content is required")
	}
	if err := sdk.Snippets().Create(cmd.Context(), name, content); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Snippet %q created", name), map[string]any{"name": name},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "list", Command: "hey snippet list", Description: "Find the new snippet ID"}),
	)
}

type snippetUpdateCommand struct {
	cmd         *cobra.Command
	name        string
	content     string
	contentHTML string
}

func newSnippetUpdateCommand() *snippetUpdateCommand {
	updateCommand := &snippetUpdateCommand{}
	updateCommand.cmd = &cobra.Command{
		Use:     "update <id>",
		Aliases: []string{"edit"},
		Short:   "Update a reusable email snippet",
		Example: `  hey snippet update 44 --name "Scheduling"
  hey snippet update 44 --content "Wednesday works for me."`,
		RunE: updateCommand.run,
		Args: usageExactOneArg(),
	}
	updateCommand.cmd.Flags().StringVar(&updateCommand.name, "name", "", "New snippet name")
	updateCommand.cmd.Flags().StringVar(&updateCommand.content, "content", "", "New snippet content as Markdown")
	updateCommand.cmd.Flags().StringVar(&updateCommand.contentHTML, "content-html", "", "New snippet content as raw HTML instead of Markdown")
	updateCommand.cmd.MarkFlagsMutuallyExclusive("content", "content-html")
	return updateCommand
}

func (c *snippetUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	snippetID, err := parsePositiveID(args[0], "snippet")
	if err != nil {
		return err
	}
	nameChanged := cmd.Flags().Changed("name")
	contentChanged := cmd.Flags().Changed("content") || cmd.Flags().Changed("content-html")
	if !nameChanged && !contentChanged {
		return apierr.ErrUsage("provide --name, --content or --content-html")
	}
	name := c.name
	if nameChanged {
		name = strings.TrimSpace(name)
		if name == "" {
			return apierr.ErrUsage("--name cannot be empty")
		}
	}
	content := c.contentHTML
	if content == "" {
		content = htmlutil.FromMarkdown(c.content)
	}
	if contentChanged && strings.TrimSpace(content) == "" {
		return apierr.ErrUsage("--content cannot be empty")
	}
	if err := sdk.Snippets().Update(cmd.Context(), snippetID, name, content); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Snippet %d updated", snippetID), map[string]any{"id": snippetID})
}

type snippetDeleteCommand struct {
	cmd *cobra.Command
}

func newSnippetDeleteCommand() *snippetDeleteCommand {
	deleteCommand := &snippetDeleteCommand{}
	deleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"remove"},
		Short:   "Delete a reusable email snippet",
		Example: `  hey snippet delete 44`,
		RunE:    deleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return deleteCommand
}

func (c *snippetDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	snippetID, err := parsePositiveID(args[0], "snippet")
	if err != nil {
		return err
	}
	if err := sdk.Snippets().Delete(cmd.Context(), snippetID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Snippet %d deleted", snippetID), map[string]any{"id": snippetID})
}
