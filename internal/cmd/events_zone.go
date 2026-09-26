package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/timezone"
)

// eventNow is the clock an event's default date is read from, a seam for tests.
var eventNow = time.Now

// timeZoneHint is how every refusal about a zone says what to do instead.
const timeZoneHint = "pass --time-zone with an IANA zone name, for example --time-zone America/New_York"

// accountZone is the HEY account's time zone as the identity serves it, read at most once a
// command and only when a clock time needs it.
type accountZone struct {
	read bool
	name string
	err  error
}

// accountTimeZone is the zone HEY's web app reads typed times in: the one the identity serves,
// which HEY keeps in step with the browser the account last signed in from.
func (f *eventFields) accountTimeZone(ctx context.Context) (string, error) {
	if !f.account.read {
		identity, err := rootSDK.Identity().GetIdentity(ctx)
		f.account = accountZone{read: true, err: err}
		if err == nil && identity != nil {
			f.account.name = identity.TimeZone
		}
	}
	return f.account.name, f.account.err
}

// writeZone is the zone a timed write's clock times are read in: --time-zone, or the HEY
// account's. Nothing else is guessed at — not the machine's, which is UTC on a server and
// in most sandboxes, and not UTC, which is what HEY would read a zoneless time as.
func (f *eventFields) writeZone(ctx context.Context) (string, *time.Location, error) {
	if f.timeZone != "" {
		loc, err := timezone.Load(f.timeZone)
		if err != nil {
			return "", nil, errInvalidTimeZone(f.timeZone)
		}
		return f.timeZone, loc, nil
	}

	name, err := f.accountTimeZone(ctx)
	if err != nil {
		// A failed read keeps what it failed with — network, rate limit, a server error — so
		// a script can tell a retry from a mistake, and says --time-zone would do without it.
		// A login HEY no longer accepts is an auth failure and nothing else: --time-zone would
		// only move the refusal to the write.
		readErr := *apierr.AsError(apierr.FromSDK(err))
		if readErr.Code == apierr.CodeAuth {
			return "", nil, &readErr
		}
		readErr.Message = "no time zone to place the event in: your HEY account's time zone could not be read: " + readErr.Message
		readErr.Hint = timeZoneHint
		if readErr.Cause == nil {
			readErr.Cause = err
		}
		return "", nil, &readErr
	}
	if name == "" {
		return "", nil, errNoAccountZone("your HEY account has no time zone set", nil)
	}
	loc, err := timezone.Load(name)
	if err != nil {
		return "", nil, errNoAccountZone(fmt.Sprintf("your HEY account's time zone %s is not one this build of hey knows", terminal.SanitizeLine(name)), err)
	}
	return name, loc, nil
}

func errInvalidTimeZone(name string) error {
	return apierr.ErrUsageHint(fmt.Sprintf("invalid time-zone: %s", terminal.SanitizeLine(name)),
		"an IANA time zone name, spelled exactly, for example America/New_York")
}

func errNoAccountZone(reason string, cause error) error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: "no time zone to place the event in: " + reason,
		Hint:    timeZoneHint,
		Cause:   cause,
	}
}
