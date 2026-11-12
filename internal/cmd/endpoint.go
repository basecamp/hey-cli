package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// A profile whose endpoint is the app is answered by the HEY app on this computer, through
// the door it opens while "Let Agents Use HEY" is on, rather than by HEY's server. The
// commands keep their names and their answers keep the API's field names; only what the
// app offers is answered, and nothing ever falls through to the server: a profile is the
// one way the endpoint is chosen. It is experimental: without HEY_EXPERIMENTAL=app the
// profile is refused (config.ApplyProfile), and nothing here shows in help or .surface.

// appTimeout bounds one command's talk with the app, from the dial to the last answer.
const appTimeout = 30 * time.Second

const appDoorHint = `Open HEY on this computer, and turn on "Let Agents Use HEY" in its Settings (General)`

// answeringEndpoint is the endpoint this run answers from, as meta.endpoint names it: ""
// outside an experimental build, where the envelope stays as it was.
var answeringEndpoint string

// appEndpoint reports whether this run is answered by the HEY app.
func appEndpoint() bool {
	return answeringEndpoint == config.EndpointApp
}

// chooseEndpoint settles the endpoint once the profile is applied, and refuses what the
// app endpoint cannot answer before anything reads a credential.
func chooseEndpoint(cmd *cobra.Command) error {
	answeringEndpoint = ""
	if !config.ExperimentalApp() {
		return nil
	}
	answeringEndpoint = config.EndpointServer
	if isAppCommand(cmd) || cfg.ActiveEndpoint() == config.EndpointApp {
		answeringEndpoint = config.EndpointApp
	}
	if !appEndpoint() {
		return nil
	}
	if accountFlag != "" {
		return apierr.ErrUsageHint(
			"the HEY app on this computer takes no --account",
			"The app answers for the account its window shows: drop --account, or use a server profile")
	}
	if isAppCommand(cmd) {
		return nil
	}
	return appOffers(cmd)
}

// appRoutes are the commands the app answers: the reads its door serves. Each one's RunE
// asks appEndpoint() before anything else.
var appRoutes = map[string]bool{
	"hey box":              true,
	"hey box view":         true,
	"hey screener list":    true,
	"hey screener history": true,
	"hey draft list":       true,
	// The writes its door serves: a screening decision, a draft written, and a send that
	// waits for the person. A verb that would send now is refused (appSendsNow).
	"hey screener approve": true,
	"hey screener deny":    true,
	"hey compose":          true,
	"hey reply":            true,
	"hey draft send":       true,
}

// appSendsNow reports a command that would send mail now. The app sends nothing without
// the person: it saves a draft, and hey draft send asks HEY to send it, which waits for
// them to press Send. So these are refused before anything happens, never turned into a
// draft quietly, which would make the word send mean two things.
func appSendsNow(cmd *cobra.Command) bool {
	switch cmd.CommandPath() {
	case "hey compose":
		draft, _ := cmd.Flags().GetBool("draft")
		return !draft
	case "hey reply":
		// A dry run sends nothing; the reply refuses it itself, as not offered.
		draft, _ := cmd.Flags().GetBool("draft")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return !draft && !dryRun
	case "hey bulk-reply send", "hey forward":
		return true
	}
	return false
}

// appSendRefusal points a command that would send now at the draft it can write instead,
// and at hey draft send, which asks HEY to send it.
func appSendRefusal(cmd *cobra.Command) error {
	path := cmd.CommandPath()
	instead := "add --draft to save it in HEY instead"
	if path == "hey bulk-reply send" || path == "hey forward" {
		instead = "write a draft with " + appCommandLine("compose --draft") + " or " + appCommandLine("reply <thread-id> --draft") + " instead"
	}
	return apierr.ErrUsageHint(
		fmt.Sprintf("%s would send now, and the HEY app on this computer sends nothing without you: %s", path, instead),
		"Then ask HEY to send it with "+appCommandLine("draft send <id>")+": it waits in HEY until you press Send")
}

// appOffers refuses a command the app endpoint does not answer. Commands that read no
// server (config, profile, version and the like) run as they always do.
func appOffers(cmd *cobra.Command) error {
	path := cmd.CommandPath()
	parts := strings.Fields(path)
	switch {
	case appSendsNow(cmd):
		return appSendRefusal(cmd)
	case appRoutes[path], len(parts) < 2, isProfileCommand(cmd), !commandUsesRuntimeConfig(cmd),
		// Completion lists commands and profile names, and reads no server.
		cmd.Name() == cobra.ShellCompRequestCmd, cmd.Name() == cobra.ShellCompNoDescRequestCmd:
		return nil
	case parts[1] == "mcp":
		return apierr.ErrUsageHint(
			fmt.Sprintf("hey mcp speaks to HEY's server, and profile %q is answered by the HEY app on this computer", terminal.SanitizeLine(cfg.ActiveProfile)),
			"Run hey mcp with a server profile: hey mcp -P <profile>")
	case parts[1] == "auth" || parts[1] == "login" || parts[1] == "logout":
		return apierr.ErrUsageHint(
			fmt.Sprintf("profile %q is answered by the HEY app on this computer, which needs no sign-in", terminal.SanitizeLine(cfg.ActiveProfile)),
			"Sign in to HEY in the app; hey auth is for a server profile: hey auth login -P <profile>")
	}
	return apierr.ErrUsageHint(
		fmt.Sprintf("%s is not offered by the HEY app on this computer (profile %q)", path, terminal.SanitizeLine(cfg.ActiveProfile)),
		"Use a server profile for it: hey profile list")
}

// appNotOffered refuses what a routed command was asked that the app does not serve, such
// as a box other than the Imbox.
func appNotOffered(what string) error {
	return apierr.ErrUsageHint(
		terminal.SanitizeLine(what)+" is not offered by the HEY app on this computer",
		"Use a server profile for it: hey profile list")
}

// endpointOption names the endpoint in the response meta, in an experimental build only.
func endpointOption() output.ResponseOption {
	if answeringEndpoint == "" {
		return func(*output.Response) {}
	}
	return output.WithMeta("endpoint", answeringEndpoint)
}

// withApp dials the app's door, runs talk over the connection and closes it, turning
// whatever went wrong on the way into what a person or a script can act on.
func withApp(cmd *cobra.Command, talk func(context.Context, *localapp.Client) error) error {
	path, err := localapp.SocketPath("")
	if err != nil {
		return apierr.ErrUsageHint(
			"XDG_RUNTIME_DIR is not set to an absolute path, so there is no HEY app to look for",
			"Run hey from your desktop session, or set "+localapp.SocketEnv+" to the app's socket")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), appTimeout)
	defer cancel()
	client, err := localapp.Dial(ctx, path)
	if err != nil {
		return appFailure(err)
	}
	defer client.Close()
	return appFailure(talk(ctx, client))
}

// callApp runs one of the app's commands and hands back its answer. A refusal is an
// error, and so is an answer that waits for the person: only a send does that.
func callApp(cmd *cobra.Command, name string, arguments map[string]any) (*localapp.Result, error) {
	result, err := callAppTool(cmd, name, arguments)
	if err != nil {
		return nil, err
	}
	if _, pending := result.Pending(); pending {
		return nil, appUnreadable(name, errors.New("it waits for the person, which only a send does"))
	}
	return result, nil
}

// callAppTool runs one of the app's commands and hands back its answer, pending or done;
// a refusal is an error.
func callAppTool(cmd *cobra.Command, name string, arguments map[string]any) (*localapp.Result, error) {
	var result *localapp.Result
	err := withApp(cmd, func(ctx context.Context, client *localapp.Client) error {
		var err error
		result, err = client.Call(ctx, name, arguments)
		return err
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, appRefusal(name, arguments, result)
	}
	return result, nil
}

// appFailure says what the door did in the CLI's terms: a door that is closed is a
// connection that failed (6), with the way to open it; anything the app answered wrongly
// is an API failure (7).
func appFailure(err error) error {
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
			Message: terminal.SanitizeLine(fmt.Sprintf("The HEY app isn't letting agents in: nothing answers at %s", closed.Path)),
			Hint:    appDoorHint,
			Cause:   err,
		}
	case errors.As(err, &connect), errors.As(err, &timeout):
		return &apierr.Error{Code: apierr.CodeNetwork, Message: terminal.SanitizeLine(err.Error()), Cause: err}
	case errors.As(err, &protocol) && protocol.Closed && protocol.Method == "initialize":
		return &apierr.Error{
			Code:    apierr.CodeNetwork,
			Message: "The HEY app closed the connection without answering",
			Hint:    "The HEY app answers only the user account it runs as",
			Cause:   err,
		}
	case errors.As(err, &protocol) && protocol.Closed:
		// The app quit, or turned the door off, while it answered.
		return &apierr.Error{
			Code:    apierr.CodeNetwork,
			Message: "The HEY app closed the connection before it answered",
			Hint:    appDoorHint,
			Cause:   err,
		}
	case errors.As(err, &server), errors.As(err, &protocol):
		return &apierr.Error{Code: apierr.CodeAPI, Message: terminal.SanitizeLine(err.Error()), Cause: err}
	default:
		return err
	}
}

// appRefusal is a command the app refused, with its reason in the error's meta. The app
// signed out is an auth failure (3), though not the CLI's sign-in; a command it does not
// offer now, arguments it would not take, or a draft it would not send now is a usage
// error (1); anything else is the app's to explain (7). Two answers are no refusal, since
// something may have been done: session_ended and saved_not_listed.
func appRefusal(name string, arguments map[string]any, result *localapp.Result) error {
	var reason map[string]any
	_ = decodeJSONExactly(string(result.Answer()), &reason)
	why, _ := reason["error"].(string)
	if why == "" {
		why = result.Text()
	}
	refusal := &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: terminal.SanitizeLine(fmt.Sprintf("The HEY app refused %s: %s", name, why)),
		Meta:    map[string]any{"refusal": reason},
	}
	arg, _ := reason["arg"].(string)
	_, namesIDs := reason["available"]
	switch {
	case why == "signed_out":
		refusal.Code = apierr.CodeAuth
		refusal.Hint = "Sign in to HEY in the app"
	case why == "session_ended":
		// A sign-out or account switch cut the call off while it ran: what it asked may
		// already be done, so it is never worded as refused.
		refusal.Message = terminal.SanitizeLine(fmt.Sprintf("The HEY app's session ended while it ran %s: it may have been done", name))
		refusal.Hint = "Check in HEY whether it was done before running it again"
		if name == "message.send" {
			// A send never goes without the person, so "done" is at most a wait.
			refusal.Message = "The HEY app's session ended while it took the send: it may be waiting in HEY for you to press Send, and nothing has been sent without you"
			refusal.Hint = "Look in HEY before asking again"
		}
	case why == "saved_not_listed":
		refusal.Message = terminal.SanitizeLine(fmt.Sprintf("The HEY app saved draft %v but couldn't list it back: it may be gone or changed", reason["draft"]))
		refusal.Hint = terminal.SanitizeLine(fmt.Sprintf("Look for a draft whose id or made_as is %v in %s before writing it again", reason["draft"], appCommandLine("draft list")))
	case why == "not_an_address":
		refusal.Code = apierr.CodeUsage
		refusal.Message = terminal.SanitizeLine("The HEY app can't read these as addresses: " + appJoined(reason["given"]))
		refusal.Hint = "Give --to as addresses, or Name <address>, separated by commas"
	case why == "not_saved":
		appNotSaved(refusal, name, reason)
	case why == "not_a_sender":
		// No confirmation waits and no sheet shows: HEY would refuse it at Send.
		refusal.Message = terminal.SanitizeLine(fmt.Sprintf("The HEY app won't send draft %v: it goes from %v, which this account no longer sends from", reason["draft"], reason["from"]))
		refusal.Hint = "Change From in HEY, or write a new draft with " + appCommandLine("compose --draft")
	case why == "disabled" && name == "message.send":
		// The draft is there, but not one HEY would ask to send now; available lists those it would.
		refusal.Code = apierr.CodeUsage
		refusal.Message = terminal.SanitizeLine(fmt.Sprintf("The HEY app won't send draft %v now: it is scheduled, goes to nobody, has a file still uploading, or is a forward", arguments["draft"]))
		// The draft is the one argument a send takes, whether or not the app names it.
		refusal.Hint = terminal.SanitizeLine(appArgumentHint("draft", reason))
		if appOffersNothing(reason) {
			refusal.Code = apierr.CodeAPI
			refusal.Hint = "No draft can be sent from here now: " + appCommandLine("draft list") + " shows which are sendable"
		}
	case arg != "" && appOffersNothing(reason):
		// Nothing would do, so nothing the caller sent was wrong: the app holds none now.
		refusal.Hint = "The HEY app holds nothing this takes now"
	case arg != "" && namesIDs:
		// An id the app does not hold, as the server answers one it does not: not found.
		refusal.Code = apierr.CodeNotFound
		refusal.Hint = terminal.SanitizeLine(appArgumentHint(arg, reason))
	case arg != "":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = terminal.SanitizeLine(appArgumentHint(arg, reason))
	case why == "not_offered":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "This HEY app does not offer it now: update HEY, or use a server profile"
	case why == "bad_request" || why == "no_such_case" || why == "not_text":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "This hey may be newer or older than the HEY app: update both"
	case why == "unavailable":
		refusal.Hint = "The HEY app could not load it: try again once the app has synced"
	}
	if detail, _ := reason["detail"].(string); detail != "" {
		refusal.Message += terminal.SanitizeLine(" (" + detail + ")")
	}
	return refusal
}

// appNotSaved says why the app saved no draft: past what one message takes. A new
// message's words and recipients are the caller's to change (1); a reply's recipients,
// files and quote are the thread's, so a reply past a limit is the person's to write in
// HEY (7).
func appNotSaved(refusal *apierr.Error, name string, reason map[string]any) {
	why, _ := reason["refusal"].(string)
	past := map[string]string{
		"too_many_recipients": "it goes to more people than HEY sends one message to",
		"too_many_files":      "it holds more files than HEY sends in one message",
		"too_large":           "it is larger than HEY sends in one message",
	}[why]
	if past == "" {
		past = "HEY says " + why
	}
	refusal.Message = terminal.SanitizeLine("The HEY app didn't save the draft: " + past)
	switch {
	case why == "too_large" && name == "draft.write":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "Shorten the message"
	case why == "too_many_recipients" && name == "draft.write":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "Write it to fewer people"
	case why == "too_large" && name == "draft.reply":
		refusal.Hint = "Shorten the message, or reply in HEY: a reply's quote counts too"
	case name == "draft.reply":
		refusal.Hint = "Reply to this thread in HEY"
	}
}

// appJoined is a refusal's list as words.
func appJoined(values any) string {
	list, _ := values.([]any)
	words := make([]string, 0, len(list))
	for _, value := range list {
		words = append(words, fmt.Sprint(value))
	}
	return strings.Join(words, ", ")
}

// appOffersNothing reports a refusal whose list of what the argument would take, its ids
// or its cases, is there and empty.
func appOffersNothing(reason map[string]any) bool {
	for _, key := range []string{"available", "cases"} {
		if values, ok := reason[key].([]any); ok && len(values) == 0 {
			return true
		}
	}
	return false
}

// appArgumentHint says what a refused argument would take: the ids the app offers it for
// now, or its cases.
func appArgumentHint(arg string, reason map[string]any) string {
	for _, key := range []string{"available", "cases"} {
		if values, ok := reason[key].([]any); ok {
			return fmt.Sprintf("%s takes one of: %s", arg, appJoined(values))
		}
	}
	return "This hey may be newer or older than the HEY app: update both"
}

// appCommandLine is a hey command on this run's profile, for a breadcrumb: an app's draft
// id run on a server profile names another draft, or one HEY would send at once.
func appCommandLine(rest string) string {
	profile := cfg.ActiveProfile
	if !config.ValidProfileName(profile) {
		profile = shellQuoted(profile)
	}
	return terminal.SanitizeLine(fmt.Sprintf("hey -P %s %s", profile, rest))
}

// decodeAppAnswer reads a command's answer into the shape this hey knows, and refuses one
// that is not that shape, or that complete says lacks what the command must have, rather
// than show it as empty.
func decodeAppAnswer[T any](name string, result *localapp.Result, complete func(T) error) (T, error) {
	var answer T
	raw := result.Answer()
	if raw == nil {
		return answer, appUnreadable(name, errors.New("it carries no structured answer"))
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return answer, appUnreadable(name, err)
	}
	if err := complete(answer); err != nil {
		var otherwise *appOtherwiseError
		if errors.As(err, &otherwise) {
			return answer, appAnsweredOtherwise(name, err)
		}
		return answer, appUnreadable(name, err)
	}
	return answer, nil
}

// appOtherwiseError is what a complete func returns for an answer whose shape is right but
// which says the app did something other than what was asked.
type appOtherwiseError struct{ said string }

func (e *appOtherwiseError) Error() string { return e.said }

// appAnsweredOtherwise is an answer that says the app did something other than what was
// asked: something may have changed, so the person is sent to look, not to upgrade.
func appAnsweredOtherwise(name string, cause error) error {
	return &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: terminal.SanitizeLine(fmt.Sprintf("The HEY app's answer to %s is not what was asked: %v", name, cause)),
		Hint:    "Check in HEY what it did before running it again",
		Cause:   cause,
	}
}

func appUnreadable(name string, cause error) error {
	return &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: terminal.SanitizeLine(fmt.Sprintf("The HEY app's answer to %s could not be read: %v", name, cause)),
		Hint:    "This hey may be older than the HEY app: run hey upgrade",
		Cause:   cause,
	}
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
