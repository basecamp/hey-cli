package tui

import (
	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// errorNotice is the line the reader gets when a request fails: what was being
// done, then the same sentence the CLI prints for that error rather than the
// SDK's own text. The auth hint is the one thing left out — it tells somebody at
// a shell prompt what to run, and this is read inside a full-screen app. The
// message may be the server's, so the line is sanitized before it is shown.
func errorNotice(what string, err error) string {
	e := apierr.AsError(apierr.FromSDK(err))
	if e.Hint != "" && e.Code != apierr.CodeAuth {
		return terminal.SanitizeLine(what + ": " + e.Message + " — " + e.Hint)
	}
	return terminal.SanitizeLine(what + ": " + e.Message)
}
