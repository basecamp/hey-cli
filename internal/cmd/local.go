package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// localTimeout bounds one hey local run, from the dial to the last answer. A read waits
// for the app's own load of what it lists, which is quick when the app is open.
const localTimeout = 30 * time.Second

const localDoorHint = `Turn on "Let agents use HEY" in the HEY app's Settings (General), and keep the app running`

// localSocketFlag is --socket, shared by every hey local subcommand.
var localSocketFlag string

type localCommand struct {
	cmd *cobra.Command
}

func newLocalCommand() *localCommand {
	localCommand := &localCommand{}
	localCommand.cmd = &cobra.Command{
		Use:   "local",
		Short: "Ask the HEY app on this computer to act for you",
		Long: `Talk to the HEY desktop app running on this computer, rather than to HEY's servers.

The app opens a door for agents only while "Let agents use HEY" is on in its
Settings. Through it, hey local runs the app's own commands over the mail the app
holds, as you would in its window: it needs no sign-in of its own, and reading
marks nothing seen.

What a command may do depends on its effect, which hey local tools lists:
  read            runs, and changes nothing
  reversible      runs, and can be undone (a sender screened out can be screened in)
  reaches_people  waits in the HEY window until you press Send there

A command that waits for you exits 9 and says "Waiting in HEY": nothing has been
sent, and nothing hey local does can send it. Nothing that cannot be undone, like
emptying the trash, is offered at all.

The door is a Unix socket at $XDG_RUNTIME_DIR/hey/agent.sock. --socket or
HEY_LOCAL_SOCKET points hey local at another one. The HEY app opens the door on
Linux; on other systems there is nothing for hey local to talk to yet.`,
		Annotations: map[string]string{
			"agent_notes": "hey local talks to the person's own HEY app, not to HEY's servers, and works only while the app is running with \"Let agents use HEY\" on. `hey local tools` lists what the app offers now, with each command's effect. Reads change nothing and mark nothing seen. A screening decision runs at once and is undone with the opposite decision. A send never goes from here: it exits 9, pending, and waits for the person to press Send in the HEY window, so say it is waiting for them, never that it was sent. Exit 6 with a hint means the door is off or the app is closed: ask the person to turn on \"Let agents use HEY\" in the app's Settings.",
		},
	}

	localCommand.cmd.PersistentFlags().StringVar(&localSocketFlag, "socket", "", "The HEY app's socket (default $XDG_RUNTIME_DIR/hey/agent.sock, or HEY_LOCAL_SOCKET)")

	localCommand.cmd.AddCommand(newLocalToolsCommand().cmd)
	localCommand.cmd.AddCommand(newLocalCallCommand().cmd)
	localCommand.cmd.AddCommand(newLocalImboxCommand().cmd)
	localCommand.cmd.AddCommand(newLocalScreenerCommand().cmd)
	localCommand.cmd.AddCommand(newLocalSendCommand().cmd)
	return localCommand
}

// withLocalApp dials the app's door, runs talk over the connection and closes it, turning
// whatever went wrong on the way into what a person or a script can act on.
func withLocalApp(cmd *cobra.Command, talk func(context.Context, *localapp.Client) error) error {
	path, err := localapp.SocketPath(localSocketFlag)
	if err != nil {
		return apierr.ErrUsageHint(
			"XDG_RUNTIME_DIR is not set, so there is no HEY socket to look for",
			"Run hey local from your desktop session, or pass --socket <path>",
		)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), localTimeout)
	defer cancel()
	client, err := localapp.Dial(ctx, path)
	if err != nil {
		return localFailure(err)
	}
	defer client.Close()
	return localFailure(talk(ctx, client))
}

// callLocal runs one of the app's commands and hands back what it did. A refusal is an
// error. So is pending, once it is written: whatever the command, an answer that waits
// for the person is never read on as done.
func callLocal(cmd *cobra.Command, name string, arguments map[string]any) (*localapp.Result, error) {
	var result *localapp.Result
	err := withLocalApp(cmd, func(ctx context.Context, client *localapp.Client) error {
		var err error
		result, err = client.Call(ctx, name, arguments)
		return err
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, localRefusal(name, result)
	}
	if pending, ok := result.Pending(); ok {
		return nil, writeLocalPending(cmd, pending)
	}
	return result, nil
}

// localFailure says what the app's door did, in the terms of the CLI's errors: a door
// that is closed is a connection that failed (6), with the way to open it; anything the
// app answered wrongly or refused is an API failure (7).
func localFailure(err error) error {
	var (
		closed   *localapp.DoorClosedError
		connect  *localapp.ConnectError
		timeout  *localapp.TimeoutError
		server   *localapp.ServerError
		protocol *localapp.ProtocolError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &closed):
		return &apierr.Error{
			Code:    apierr.CodeNetwork,
			Message: fmt.Sprintf("HEY isn't letting agents in: nothing answers at %s", closed.Path),
			Hint:    localDoorHint,
			Cause:   err,
		}
	case errors.As(err, &connect), errors.As(err, &timeout):
		return &apierr.Error{Code: apierr.CodeNetwork, Message: terminal.SanitizeLine(err.Error()), Cause: err}
	case errors.As(err, &protocol) && protocol.Closed && protocol.Method == "initialize":
		return &apierr.Error{
			Code:    apierr.CodeNetwork,
			Message: "HEY closed the connection without answering",
			Hint:    "The HEY app answers only the user account it runs as",
			Cause:   err,
		}
	case errors.As(err, &server) && server.Method == "tools/call" && server.Code == -32602:
		// The app's answer to a tool it has not got, or arguments that are not an object.
		return &apierr.Error{
			Code:    apierr.CodeUsage,
			Message: terminal.SanitizeLine(err.Error()),
			Hint:    "Run: hey local tools, for what the app offers now and what each takes",
			Cause:   err,
		}
	case errors.As(err, &server), errors.As(err, &protocol):
		return &apierr.Error{Code: apierr.CodeAPI, Message: terminal.SanitizeLine(err.Error()), Cause: err}
	default:
		return err
	}
}

// localRefusal is a command the app refused, with its reason in the error's meta. A
// refusal that names an argument is a usage error, with what the argument would take; the
// app signed out of HEY is an auth one, though not the CLI's sign-in; anything else, a list
// the app could not load included, is the app's to explain.
func localRefusal(name string, result *localapp.Result) error {
	// Numbers kept as written, so an id past a million is hinted as an id, not 1.7e+12.
	var reason map[string]any
	_ = decodeJSONExactly(string(result.Answer()), &reason)
	why, _ := reason["error"].(string)
	if why == "" {
		why = result.Text()
	}

	refusal := &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: terminal.SanitizeLine(fmt.Sprintf("HEY refused %s: %s", name, why)),
		Meta:    map[string]any{"refusal": reason},
	}
	if detail, _ := reason["detail"].(string); detail != "" {
		refusal.Message += terminal.SanitizeLine(" (" + detail + ")")
	}
	arg, _ := reason["arg"].(string)
	available, listsAvailable := reason["available"].([]any)
	switch {
	case arg != "" && listsAvailable && len(available) == 0:
		// Nothing would do, so nothing the caller sent was wrong: the app holds none now,
		// or could not load what it would choose from.
		refusal.Hint = "The app holds nothing this takes now: hey local tools lists what it offers"
	case arg != "":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = terminal.SanitizeLine(localArgumentHint(arg, reason))
	case why == "bad_request" || why == "not_text":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "Run: hey local tools, for what each command takes"
	case why == "not_offered":
		refusal.Hint = "Run: hey local tools, for what the app offers now"
	case why == "signed_out":
		refusal.Code = apierr.CodeAuth
		refusal.Hint = "Sign in to HEY in the app"
	}
	return refusal
}

// localArgumentHint says what a refused argument would take: the ids the app offers it
// for now, or its cases.
func localArgumentHint(arg string, reason map[string]any) string {
	for _, key := range []string{"available", "cases"} {
		if values, ok := reason[key].([]any); ok {
			taken := make([]string, 0, len(values))
			for _, value := range values {
				taken = append(taken, fmt.Sprint(value))
			}
			return fmt.Sprintf("%s takes one of: %s", arg, strings.Join(taken, ", "))
		}
	}
	return "Run: hey local tools, for what each command takes"
}

// writeLocalAnswer writes what a command answered: pretty JSON for a person, the envelope
// for everyone else.
func writeLocalAnswer(cmd *cobra.Command, name string, result *localapp.Result) error {
	answer, err := decodeLocalAnswer(name, result)
	if err != nil {
		return err
	}
	if writer.IsStyled() {
		indented, err := json.MarshalIndent(answer, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), terminal.Sanitize(string(indented)))
		return nil
	}
	return writeOK(answer, output.WithSummary("HEY ran "+name))
}

// writeLocalPending says a command waits in HEY's window, and returns ErrPending so the
// run exits as neither done nor failed, whatever the output format makes of the answer.
func writeLocalPending(cmd *cobra.Command, pending *localapp.Pending) error {
	const summary = "Waiting in HEY: nothing is sent until the person presses Send in the HEY window"
	var snapshot localSendSnapshot
	_ = json.Unmarshal(pending.Snapshot, &snapshot)
	confirmation := localConfirmationID(pending.Confirmation)

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Waiting in HEY: press Send in the HEY window to send it. Nothing has been sent.")
		if len(snapshot.To) > 0 {
			fmt.Fprintln(out, "  To:      "+terminal.SanitizeLine(strings.Join(snapshot.To, ", ")))
		}
		if snapshot.Subject != "" {
			fmt.Fprintln(out, "  Subject: "+terminal.SanitizeLine(snapshot.Subject))
		}
	case output.FormatIDs, output.FormatCount:
		// Neither has a list to show: the confirmation's id is the one id there is, and
		// the words go to stderr so stdout stays what the flag asked for.
		if writer.EffectiveFormat() == output.FormatIDs && confirmation != "" {
			fmt.Fprintln(cmd.OutOrStdout(), confirmation)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), summary)
	default:
		// Decoded to values, since --markdown prints a map's values as they are and would
		// show raw JSON as bytes.
		data := map[string]any{"status": "pending"}
		for key, raw := range map[string]json.RawMessage{"confirmation": pending.Confirmation, "snapshot": pending.Snapshot} {
			var value any
			if decodeJSONExactly(string(raw), &value) == nil {
				data[key] = value
			}
		}
		if err := writeOK(data, output.WithSummary(summary), output.WithMeta("status", "pending")); err != nil {
			// A filter that failed on the answer does not make the send any less pending.
			fmt.Fprintln(cmd.ErrOrStderr(), terminal.SanitizeLine(err.Error()))
			fmt.Fprintln(cmd.ErrOrStderr(), summary)
		}
	}
	return output.ErrPending
}

// localConfirmationID is a confirmation's id as a line: a number as written, a word
// without its quotes.
func localConfirmationID(raw json.RawMessage) string {
	var word string
	if json.Unmarshal(raw, &word) == nil {
		return terminal.SanitizeLine(word)
	}
	return terminal.SanitizeLine(string(raw))
}

// decodeLocalAnswer reads a command's answer as JSON values, numbers kept exact, so an id
// the app gives reaches the output as the app wrote it.
func decodeLocalAnswer(name string, result *localapp.Result) (any, error) {
	raw := result.Answer()
	if raw == nil {
		return map[string]any{"text": result.Text()}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var answer any
	if err := decoder.Decode(&answer); err != nil {
		return nil, localUnreadable(name, err)
	}
	return answer, nil
}

// decodeLocalAnswerAs reads a command's answer into the shape the command's sugar knows,
// and refuses one that is not that shape, or that complete says lacks what the command
// must have, rather than show it as empty or as done.
func decodeLocalAnswerAs[T any](name string, result *localapp.Result, complete func(T) error) (T, error) {
	var answer T
	raw := result.Answer()
	if raw == nil {
		return answer, localUnreadable(name, errors.New("it carries no structured answer"))
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return answer, localUnreadable(name, err)
	}
	if err := complete(answer); err != nil {
		return answer, localUnreadable(name, err)
	}
	return answer, nil
}

func localUnreadable(name string, cause error) error {
	return &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: terminal.SanitizeLine(fmt.Sprintf("HEY's answer to %s could not be read: %v", name, cause)),
		Hint:    "This hey may be older than the HEY app: run hey upgrade",
		Cause:   cause,
	}
}
