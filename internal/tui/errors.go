package tui

import "github.com/basecamp/hey-cli/internal/apierr"

// errorNotice is the line the reader gets when a request fails: what was being
// done, then the same sentence the CLI prints for that error rather than the
// SDK's own text. The auth hint is the one thing left out — it tells somebody at
// a shell prompt what to run, and this is read inside a full-screen app.
func errorNotice(what string, err error) string {
	e := apierr.AsError(apierr.FromSDK(err))
	if e.Hint != "" && e.Code != apierr.CodeAuth {
		return what + ": " + e.Message + " — " + e.Hint
	}
	return what + ": " + e.Message
}
