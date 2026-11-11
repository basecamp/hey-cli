package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/output"
)

type localToolsCommand struct {
	cmd *cobra.Command
}

// localTool is a command the app offers, as hey local tools lists it.
type localTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Effect      string `json:"effect,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

// localEffects is how a person reads each effect the app names.
var localEffects = map[string]string{
	"read":           "reads, changes nothing",
	"reversible":     "changes something you can undo",
	"reaches_people": "waits for you in HEY",
}

func newLocalToolsCommand() *localToolsCommand {
	toolsCommand := &localToolsCommand{}
	toolsCommand.cmd = &cobra.Command{
		Use:   "tools",
		Short: "List what the HEY app offers",
		Long:  "List the commands the HEY app offers agents now, with what each does and the arguments it takes.",
		Annotations: map[string]string{
			"agent_notes": "Each tool has a name for `hey local call`, its description in the app's language, its effect (read, reversible, or reaches_people, which waits for the person in the HEY window) and its JSON Schema. An empty list means nobody is signed in to the app.",
		},
		Example: `  hey local tools
  hey local tools --json`,
		RunE: toolsCommand.run,
		Args: cobra.NoArgs,
	}
	return toolsCommand
}

func (c *localToolsCommand) run(cmd *cobra.Command, _ []string) error {
	var offered []localapp.Tool
	err := withLocalApp(cmd, func(ctx context.Context, client *localapp.Client) error {
		var err error
		offered, err = client.Tools(ctx)
		return err
	})
	if err != nil {
		return err
	}

	tools := make([]localTool, 0, len(offered))
	for _, tool := range offered {
		listed := localTool{Name: tool.Name, Description: tool.Description, Effect: tool.Effect()}
		if len(tool.InputSchema) > 0 {
			listed.InputSchema = tool.InputSchema
		}
		tools = append(tools, listed)
	}

	if writer.IsStyled() {
		if len(tools) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "HEY offers nothing now: sign in to HEY in the app")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Command", "Effect", "Description"})
		for _, tool := range tools {
			effect := localEffects[tool.Effect]
			if effect == "" {
				effect = tool.Effect
			}
			table.addRow([]string{tool.Name, effect, truncate(tool.Description, 60)})
		}
		table.print()
		return nil
	}
	return writeOK(tools,
		output.WithSummary(fmt.Sprintf("HEY offers %d %s", len(tools), localCommandNoun(len(tools)))),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "call", Command: "hey local call <command> --arg name=value", Description: "Run one"}),
	)
}

func localCommandNoun(count int) string {
	if count == 1 {
		return "command"
	}
	return "commands"
}
