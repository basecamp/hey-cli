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
	if !appEndpoint() || isAppCommand(cmd) {
		return nil
	}
	if accountFlag != "" {
		return apierr.ErrUsageHint(
			fmt.Sprintf("profile %q is answered by the HEY app on this computer, which takes no --account", terminal.SanitizeLine(cfg.ActiveProfile)),
			"The app answers for the account its window shows: drop --account, or use a server profile")
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
}

// appOffers refuses a command the app endpoint does not answer. Commands that read no
// server (config, profile, version and the like) run as they always do.
func appOffers(cmd *cobra.Command) error {
	path := cmd.CommandPath()
	parts := strings.Fields(path)
	switch {
	case appRoutes[path], len(parts) < 2, isProfileCommand(cmd), !commandUsesRuntimeConfig(cmd):
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
// error, and so is an answer that waits for the person: a read never should.
func callApp(cmd *cobra.Command, name string, arguments map[string]any) (*localapp.Result, error) {
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
		return nil, appRefusal(name, result)
	}
	if _, pending := result.Pending(); pending {
		return nil, appUnreadable(name, errors.New("it waits for the person, which a read never does"))
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
	case errors.As(err, &server), errors.As(err, &protocol):
		return &apierr.Error{Code: apierr.CodeAPI, Message: terminal.SanitizeLine(err.Error()), Cause: err}
	default:
		return err
	}
}

// appRefusal is a command the app refused, with its reason in the error's meta. The app
// signed out is an auth failure (3), though not the CLI's sign-in; a command it does not
// offer now, or arguments it would not take, is a usage error (1); anything else is the
// app's to explain (7).
func appRefusal(name string, result *localapp.Result) error {
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
	switch why {
	case "signed_out":
		refusal.Code = apierr.CodeAuth
		refusal.Hint = "Sign in to HEY in the app"
	case "not_offered":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "This HEY app does not offer it now: update HEY, or use a server profile"
	case "bad_request", "no_such_case", "not_text":
		refusal.Code = apierr.CodeUsage
		refusal.Hint = "This hey may be newer or older than the HEY app: update both"
	case "unavailable":
		refusal.Hint = "The HEY app could not load it: try again once the app has synced"
	}
	if detail, _ := reason["detail"].(string); detail != "" {
		refusal.Message += terminal.SanitizeLine(" (" + detail + ")")
	}
	return refusal
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
		return answer, appUnreadable(name, err)
	}
	return answer, nil
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
