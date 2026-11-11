package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type localCallCommand struct {
	cmd  *cobra.Command
	args []string
}

func newLocalCallCommand() *localCallCommand {
	callCommand := &localCallCommand{}
	callCommand.cmd = &cobra.Command{
		Use:   "call <command> [arguments-json]",
		Short: "Run one of the HEY app's commands",
		Long: `Run one of the commands hey local tools lists, with its arguments as a JSON object,
as --arg name=value pairs, or both, the pairs over the object.

A value is read as JSON where it is JSON, so sender=42 is the number 42 and
decision=out the word out. Quote a word that would read as something else:
--arg 'subject="2026"'.

A command that reaches people waits in the HEY window for you to press Send:
it exits 9, pending, and nothing has been sent.`,
		Annotations: map[string]string{
			"agent_notes": "Prefer the sugar (hey local imbox, hey local screener, hey local send) where it covers the command. Exit 9 is pending: the command waits for the person in the HEY window and nothing has happened yet. A refusal carries the app's reason in the JSON error's meta.refusal, with the ids it would take under available.",
		},
		Example: `  hey local call imbox.list
  hey local call screener.decide --arg sender=42 --arg decision=out
  hey local call screener.decide '{"sender": 42, "decision": "in"}'
  hey local call message.send --arg draft=1731280000123 --json`,
		RunE: callCommand.run,
		Args: cobra.MatchAll(usageMinOneArg(), cobra.MaximumNArgs(2)),
	}
	callCommand.cmd.Flags().StringArrayVar(&callCommand.args, "arg", nil, "An argument as name=value (repeatable)")
	return callCommand
}

func (c *localCallCommand) run(cmd *cobra.Command, args []string) error {
	arguments, err := localArguments(args[1:], c.args)
	if err != nil {
		return err
	}
	result, err := callLocal(cmd, args[0], arguments)
	if err != nil {
		return err
	}
	return writeLocalAnswer(cmd, args[0], result)
}

// localArguments builds a call's arguments: the JSON object, if one was given, then each
// name=value pair over it.
func localArguments(object []string, pairs []string) (map[string]any, error) {
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
			return nil, apierr.ErrUsage(fmt.Sprintf("--arg %s is not name=value", pair))
		}
		if given[name] {
			return nil, apierr.ErrUsage(fmt.Sprintf("--arg %s is given twice", name))
		}
		given[name] = true
		arguments[name] = localArgumentValue(value)
	}
	return arguments, nil
}

// localArgumentValue reads value as JSON where it is JSON, and as a word otherwise.
func localArgumentValue(value string) any {
	var parsed any
	if err := decodeJSONExactly(value, &parsed); err != nil {
		return value
	}
	return parsed
}

// decodeJSONExactly reads one JSON value and nothing after it, numbers kept as written, so
// an id past what a float holds reaches the app unrounded.
func decodeJSONExactly(text string, into any) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	// Token reads past whatever follows, a stray bracket included, which More would not.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("more after the JSON value")
	}
	return nil
}
