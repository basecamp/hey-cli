package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// hey app is raw access to the HEY app's door, for a tool the ordinary commands have not
// learned yet. It is hidden, and registered only with HEY_EXPERIMENTAL=app. It talks to the
// app whatever the profile, and runs only the tools whose effect is read: what changes
// something comes with the commands that do it.

type appCommand struct {
	cmd *cobra.Command
}

func newAppCommand() *appCommand {
	command := &appCommand{}
	command.cmd = &cobra.Command{
		Use:    "app",
		Short:  "Talk to the HEY app on this computer directly",
		Hidden: true,
		Long: `Talk to the door the HEY app on this computer opens while "Let Agents Use HEY"
is on in its Settings, as a profile whose endpoint is the app does.

The door is a Unix socket at $XDG_RUNTIME_DIR/hey/agent.sock, or HEY_LOCAL_SOCKET.`,
	}
	command.cmd.AddCommand(newAppToolsCommand())
	command.cmd.AddCommand(newAppCallCommand())
	return command
}

func isAppCommand(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	return len(parts) >= 2 && parts[1] == "app"
}

// appTool is a tool the app offers, as hey app tools lists it.
type appTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Effect      string `json:"effect,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

func newAppToolsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List what the HEY app offers",
		Long:  "List the tools the HEY app offers agents now, with each one's effect (read, reversible or reaches_people) and the arguments it takes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			offered, err := listAppTools(cmd)
			if err != nil {
				return err
			}
			tools := make([]appTool, 0, len(offered))
			for _, tool := range offered {
				listed := appTool{Name: tool.Name, Description: tool.Description, Effect: tool.Effect()}
				if len(tool.InputSchema) > 0 {
					listed.InputSchema = tool.InputSchema
				}
				tools = append(tools, listed)
			}
			if writer.IsStyled() {
				if len(tools) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "The HEY app offers nothing now: sign in to HEY in the app")
					return nil
				}
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"Tool", "Effect", "Arguments", "Description"})
				for _, tool := range tools {
					table.addRow([]string{tool.Name, tool.Effect, appArgumentNames(tool.InputSchema), truncate(tool.Description, 60)})
				}
				table.print()
				return nil
			}
			return writeOK(tools, output.WithSummary(fmt.Sprintf("The HEY app offers %d tools", len(tools))))
		},
	}
}

func listAppTools(cmd *cobra.Command) ([]localapp.Tool, error) {
	var offered []localapp.Tool
	err := withApp(cmd, func(ctx context.Context, client *localapp.Client) error {
		var err error
		offered, err = client.Tools(ctx)
		return err
	})
	return offered, err
}

// appArgumentNames names the arguments a tool's schema declares, sorted, for a person;
// --json carries the whole schema.
func appArgumentNames(schema any) string {
	raw, ok := schema.(json.RawMessage)
	if !ok {
		return ""
	}
	var declared struct {
		Properties map[string]any `json:"properties"`
	}
	if json.Unmarshal(raw, &declared) != nil {
		return ""
	}
	names := make([]string, 0, len(declared.Properties))
	for name := range declared.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func newAppCallCommand() *cobra.Command {
	var pairs []string
	command := &cobra.Command{
		Use:   "call <tool> [arguments-json]",
		Short: "Run one of the HEY app's read tools",
		Long: `Run a tool hey app tools lists as read, with its arguments as a JSON object, as
--arg name=value pairs, or both, the pairs over the object. A value is read as
JSON where it is JSON, so sender=42 is the number 42.

A tool whose effect is anything but read is refused before it is called.`,
		Args: cobra.MatchAll(usageMinOneArg(), cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := appArguments(args[1:], pairs)
			if err != nil {
				return err
			}
			name := args[0]
			offered, err := listAppTools(cmd)
			if err != nil {
				return err
			}
			if len(offered) == 0 {
				// The door lists nothing while nobody is signed in to the app.
				return &apierr.Error{Code: apierr.CodeAuth, Message: "The HEY app offers nothing now: nobody is signed in", Hint: "Sign in to HEY in the app"}
			}
			effect, found := "", false
			for _, tool := range offered {
				if tool.Name == name {
					effect, found = tool.Effect(), true
				}
			}
			switch {
			case !found:
				return apierr.ErrUsageHint(terminal.SanitizeLine(fmt.Sprintf("the HEY app does not offer %s now", name)), "Run: hey app tools")
			case effect != "read":
				return apierr.ErrUsageHint(
					terminal.SanitizeLine(fmt.Sprintf("%s is %s, and hey app runs only what reads", name, effect)),
					"Do it in the HEY app")
			}
			result, err := callApp(cmd, name, arguments)
			if err != nil {
				return err
			}
			return writeAppAnswer(cmd, name, result)
		},
	}
	command.Flags().StringArrayVar(&pairs, "arg", nil, "An argument as name=value (repeatable)")
	return command
}

// appArguments builds a call's arguments: the JSON object, if one was given, then each
// name=value pair over it.
func appArguments(object []string, pairs []string) (map[string]any, error) {
	arguments := map[string]any{}
	if len(object) == 1 {
		if err := decodeJSONExactly(object[0], &arguments); err != nil || arguments == nil {
			return nil, apierr.ErrUsageHint("the arguments are not a JSON object",
				`Pass an object, like '{"sender": 42}', or --arg sender=42`)
		}
	}
	given := map[string]bool{}
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, apierr.ErrUsage(terminal.SanitizeLine(fmt.Sprintf("--arg %s is not name=value", pair)))
		}
		if given[name] {
			return nil, apierr.ErrUsage(terminal.SanitizeLine(fmt.Sprintf("--arg %s is given twice", name)))
		}
		given[name] = true
		var parsed any
		if decodeJSONExactly(value, &parsed) != nil {
			parsed = value
		}
		arguments[name] = parsed
	}
	return arguments, nil
}

// writeAppAnswer writes what a tool answered, numbers kept as the app wrote them: pretty
// JSON for a person, the envelope for everyone else.
func writeAppAnswer(cmd *cobra.Command, name string, result *localapp.Result) error {
	var answer any = map[string]any{"text": result.Text()}
	if raw := result.Answer(); raw != nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&answer); err != nil {
			return appUnreadable(name, err)
		}
	}
	if writer.IsStyled() {
		indented, err := json.MarshalIndent(answer, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), terminal.Sanitize(string(indented)))
		return nil
	}
	return writeOK(answer, output.WithSummary("The HEY app ran "+name))
}
