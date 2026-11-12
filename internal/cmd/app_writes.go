package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// The writes the HEY app answers on an app profile: hey screener approve and deny
// (screener.decide), hey compose --draft (draft.write), hey reply --draft (draft.reply)
// and hey draft send (message.send). The app sends nothing without the person: a send
// answers pending, waiting in HEY's window for them to press Send, and exits 9. A verb
// that would send now is refused before anything happens (appSendsNow).

// appRefusesFlags refuses the first of names that was given, which the app does not take.
func appRefusesFlags(cmd *cobra.Command, names ...string) error {
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			return appNotOffered("--" + name)
		}
	}
	return nil
}

// appDecision is what screener.decide answers.
type appDecision struct {
	Sender   int64  `json:"sender"`
	Screened string `json:"screened"`
}

// screenInApp screens one sender in or out in the app: by the app's sender id, as its
// hey screener list prints, or by address, looked up in the app's own Screener. It answers
// as the server's screening does, a list of the decided, with meta.id_kind sender.
func screenInApp(cmd *cobra.Command, args []string, decision string) error {
	if len(args) > 1 {
		return apierr.ErrUsageHint("the HEY app on this computer screens one sender at a time",
			"Run "+appCommandLine("screener "+appScreenVerb(decision))+" once for each sender")
	}
	if err := appRefusesFlags(cmd, "box", "seen", "spam"); err != nil {
		return err
	}
	sender, err := appSenderFor(cmd, args[0])
	if err != nil {
		return err
	}
	result, err := callApp(cmd, "screener.decide", map[string]any{"sender": sender.ID, "decision": decision})
	if err != nil {
		return err
	}
	// Said back by the app, so a decision it took any other way is never reported as this one.
	if _, err := decodeAppAnswer("screener.decide", result, func(decided appDecision) error {
		if decided.Sender != sender.ID || decided.Screened != decision {
			return &appOtherwiseError{fmt.Sprintf("it says sender %d was screened %q, not sender %d screened %s", decided.Sender, decided.Screened, sender.ID, decision)}
		}
		return nil
	}); err != nil {
		return err
	}

	status, verb, opposite, undo := "approved", "approved", "deny", "Turn this sender away instead"
	if decision == "out" {
		status, verb, opposite, undo = "denied", "denied", "approve", "Let this sender through after all"
	}
	decided := []screenedResult{{ID: sender.ID, Status: status, Name: sender.Name, Email: sender.Address}}
	if writer.IsStyled() {
		who := sender.Name
		if who == "" {
			who = sender.Address
		}
		if who == "" {
			who = fmt.Sprintf("Sender %d", sender.ID)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s.\n", terminal.SanitizeLine(who), verb)
		return nil
	}
	return writeOK(decided,
		output.WithSummary(fmt.Sprintf("1 sender %s", verb)),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      opposite,
			Command:     appCommandLine(fmt.Sprintf("screener %s %d", opposite, sender.ID)),
			Description: undo,
		}),
		appIDKind("sender"),
	)
}

func appScreenVerb(decision string) string {
	if decision == "out" {
		return "deny"
	}
	return "approve"
}

// appSenderFor is the sender a screening names: an address, found among the senders the
// app's Screener lists, waiting or decided, whatever its case; else the app's sender id.
func appSenderFor(cmd *cobra.Command, value string) (appSender, error) {
	if !isScreenerAddress(value) {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return appSender{}, apierr.ErrUsageHint(fmt.Sprintf("invalid sender ID or address: %s", value),
				"Sender IDs come from "+appCommandLine("screener list")+"; an email address works too")
		}
		return appSender{ID: id}, nil
	}
	screener, err := readAppScreener(cmd)
	if err != nil {
		return appSender{}, err
	}
	var found []appSender
	for _, sender := range append(append([]appSender{}, screener.Waiting...), screener.Decided...) {
		if strings.EqualFold(strings.TrimSpace(sender.Address), strings.TrimSpace(value)) && !appHasSender(found, sender.ID) {
			found = append(found, sender)
		}
	}
	switch {
	case len(found) == 0 && *screener.WaitingCount > len(screener.Waiting):
		// The app lists only part of who waits, so an address missing from the list was not
		// searched for. One found in it is taken, so a waiting sender stays
		// reachable by address while the app lists only the first of a long Screener.
		return appSender{}, &apierr.Error{
			Code:    apierr.CodeAPI,
			Message: fmt.Sprintf("The HEY app lists %d of the %d senders waiting, and none of them sends from %s", len(screener.Waiting), *screener.WaitingCount, terminal.SanitizeLine(value)),
			Hint:    "Screen them in HEY, or by sender ID once " + appCommandLine("screener list") + " lists them",
		}
	case len(found) == 0:
		return appSender{}, apierr.ErrNotFoundHint("sender", value,
			"Nobody the HEY app's Screener lists sends from that address: "+appCommandLine("screener list")+", "+appCommandLine("screener history"))
	case len(found) == 1:
		return found[0], nil
	}
	matches := make([]string, 0, len(found))
	for _, sender := range found {
		matches = append(matches, strconv.FormatInt(sender.ID, 10))
	}
	return appSender{}, apierr.ErrAmbiguous("sender", matches)
}

func appHasSender(senders []appSender, id int64) bool {
	for _, sender := range senders {
		if sender.ID == id {
			return true
		}
	}
	return false
}

// runApp writes a new message as a draft in the app, which takes plain text to To alone
// and signs it itself, as HEY signs a message it begins.
func (c *composeCommand) runApp(cmd *cobra.Command) error {
	if cmd.Flags().Changed("thread-id") {
		return apierr.ErrUsageHint("the HEY app on this computer takes no --thread-id",
			"Reply with "+appCommandLine("reply <thread-id> --draft"))
	}
	if err := appRefusesFlags(cmd, "cc", "bcc", "attach", "message-html", "from", "no-name-tag"); err != nil {
		return err
	}
	if c.subject == "" {
		return apierr.ErrUsageHint("--subject is required", appCommandLine("compose --draft --to <email> --subject <subject> -m <message>"))
	}
	body, err := appMessageBody(c.message)
	if err != nil {
		return err
	}
	to := strings.Join(parseAddresses(c.to), ", ")
	result, err := callApp(cmd, "draft.write", map[string]any{"to": to, "subject": c.subject, "body": body})
	if err != nil {
		return err
	}
	return writeAppDraft(cmd, "draft.write", result, 0)
}

// runApp writes a reply as a draft in the app, which addresses it as HEY's Reply does.
func (c *replyCommand) runApp(cmd *cobra.Command, args []string) error {
	if err := appRefusesFlags(cmd, "dry-run", "to", "cc", "bcc", "replace-recipients", "from", "attach", "message-html"); err != nil {
		return err
	}
	thread, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || thread <= 0 {
		return apierr.ErrUsageHint(fmt.Sprintf("invalid thread ID: %s", args[0]), "Thread IDs are topic_id in "+appCommandLine("box view imbox"))
	}
	body, err := appMessageBody(c.message)
	if err != nil {
		return err
	}
	result, err := callApp(cmd, "draft.reply", map[string]any{"thread": thread, "body": body})
	if err != nil {
		return err
	}
	return writeAppDraft(cmd, "draft.reply", result, thread)
}

// appMessageBody is -m's words, else stdin's when it is not a terminal, else $EDITOR's, as
// compose and reply read a message; the app takes them as plain text.
func appMessageBody(message string) (string, error) {
	switch {
	case message != "":
		return message, nil
	case !stdinIsTerminal():
		read, err := readStdin()
		if err != nil {
			return "", err
		}
		message = read
	default:
		written, err := editor.Open("")
		if err != nil {
			return "", apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
		}
		message = written
	}
	if strings.TrimSpace(message) == "" {
		return "", apierr.ErrUsage("no message provided (use -m or --message, or pipe it to stdin)")
	}
	return message, nil
}

// writeAppDraft answers a draft the app saved: nothing has been sent, and, where HEY would
// send it, hey draft send on this profile asks it to. thread is the thread a reply was
// asked for, 0 for a new message.
func writeAppDraft(cmd *cobra.Command, name string, result *localapp.Result, thread int64) error {
	kind := "new"
	if thread != 0 {
		kind = "reply"
	}
	saved, err := decodeAppAnswer(name, result, func(draft appDoorDraft) error {
		if draft.ID <= 0 {
			return errors.New("it gives a draft without an ID")
		}
		if draft.Sendable == nil {
			return fmt.Errorf("it does not say whether draft %d can be sent", draft.ID)
		}
		if draft.Kind != kind {
			return &appOtherwiseError{fmt.Sprintf("it saved draft %d as %q, not %s", draft.ID, draft.Kind, kind)}
		}
		if thread != 0 && (draft.Thread == nil || *draft.Thread != thread) {
			return &appOtherwiseError{fmt.Sprintf("it saved draft %d, not a reply on thread %d", draft.ID, thread)}
		}
		return nil
	})
	if err != nil {
		return err
	}
	draft := appDraftFrom(saved)
	// A draft is written by now, so --ids-only and --count get it as a list of one: their
	// refusal of a lone object would read as nothing having happened, and a retry writes
	// another draft.
	var data any = draft
	if format := writer.EffectiveFormat(); format == output.FormatIDs || format == output.FormatCount {
		data = []appDraft{draft}
	}

	to := "nobody"
	if len(draft.To) > 0 {
		to = strings.Join(draft.To, ", ")
	}
	line := fmt.Sprintf("Draft %d saved in HEY, to %s. Nothing has been sent.", draft.ID, to)
	if !draft.Sendable {
		summary := fmt.Sprintf("Draft %d saved; HEY would not send it from here now, and nothing has been sent", draft.ID)
		// --ids-only and --count carry no summary; this one goes where a waiting send's does.
		if format := writer.EffectiveFormat(); format == output.FormatIDs || format == output.FormatCount {
			fmt.Fprintln(cmd.ErrOrStderr(), summary)
		}
		return writeMutationLine(cmd, line+" HEY can't send it from here: finish it in HEY.", summary, data)
	}
	send := appCommandLine(fmt.Sprintf("draft send %d", draft.ID))
	return writeMutationLine(cmd,
		line+" Ask HEY to send it with: "+send,
		fmt.Sprintf("Draft %d saved; nothing has been sent", draft.ID),
		data,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "send", Command: send, Description: "Ask HEY to send it: it waits in HEY until the person presses Send"}),
	)
}

// runApp asks the app to send a draft it holds. The app never sends it on its own: it
// shows the person the draft and waits for them to press Send, so the answer is pending,
// exit 9, and anything else it answers is never read as a send.
func (c *draftSendCommand) runApp(cmd *cobra.Command, args []string) error {
	draftID, err := parseDraftID(args[0])
	if err != nil {
		return err
	}
	result, err := callAppTool(cmd, "message.send", map[string]any{"draft": draftID})
	if err != nil {
		return err
	}
	if pending, ok := result.Pending(); ok {
		return writeAppPending(cmd, pending)
	}
	answer := string(result.Answer())
	if answer == "" {
		answer = result.Text()
	}
	return &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: terminal.SanitizeLine("The HEY app's answer to message.send is not a send waiting for you, and it may have done something: " + answer),
		Hint:    "Check in HEY what it did before running it again",
	}
}

// appSendSnapshot is the part of a waiting send's snapshot shown to a person: what they
// will see in HEY's window before they press Send.
type appSendSnapshot struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
}

// writeAppPending says a send waits in HEY's window, and returns ErrPending so the run
// exits 9, neither done nor failed, whatever the output format makes of the answer.
func writeAppPending(cmd *cobra.Command, pending *localapp.Pending) error {
	const summary = "Waiting in HEY for the person to press Send: nothing has been sent"
	var snapshot appSendSnapshot
	_ = json.Unmarshal(pending.Snapshot, &snapshot)
	confirmation := appConfirmationID(pending.Confirmation)

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
		// Neither has a list to show: the confirmation's id is the one id there is, and the
		// words go to stderr so stdout stays what the flag asked for.
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
			if len(raw) > 0 && decodeJSONExactly(string(raw), &value) == nil {
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

// appConfirmationID is a confirmation's id as a line: a number as written, a word without
// its quotes.
func appConfirmationID(raw json.RawMessage) string {
	var word string
	if json.Unmarshal(raw, &word) == nil {
		return terminal.SanitizeLine(word)
	}
	return terminal.SanitizeLine(string(raw))
}
