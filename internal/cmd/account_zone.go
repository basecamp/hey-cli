package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// clockNow is the clock a default date is read from, a seam for tests. The location it
// answers in is the machine's, which only a read falls back on, and only when the account
// has no zone to tell today in.
var clockNow = time.Now

// todayUnknown is what a refusal to name today says it could not do.
const todayUnknown = "cannot tell which day today is"

// accountZone is the HEY account's time zone as the identity serves it, read at most once a
// command and only when something needs it: a clock time to place, or a today to name.
type accountZone struct {
	read bool
	name string
	err  error
}

// timeZone is the zone HEY's web app reads typed times and today in: the one the identity
// serves, which HEY keeps in step with the browser the account last signed in from.
func (a *accountZone) timeZone(ctx context.Context) (string, error) {
	if !a.read {
		identity, err := rootSDK.Identity().GetIdentity(ctx)
		*a = accountZone{read: true, err: err}
		if err == nil && identity != nil {
			a.name = identity.TimeZone
		}
	}
	return a.name, a.err
}

// location is the account's zone, loaded. An account with no zone, or with one this build
// cannot load, answers an *unknownAccountZoneError; a failed read answers the read's own error.
func (a *accountZone) location(ctx context.Context) (string, *time.Location, error) {
	name, err := a.timeZone(ctx)
	if err != nil {
		return "", nil, err
	}
	if name == "" {
		return "", nil, &unknownAccountZoneError{reason: "your HEY account has no time zone set"}
	}
	loc, err := loadEventZone(name)
	if err != nil {
		return "", nil, &unknownAccountZoneError{
			reason: fmt.Sprintf("your HEY account's time zone %s is not one this build of hey knows", terminal.SanitizeLine(name)),
			cause:  err,
		}
	}
	return name, loc, nil
}

// todayToWrite is today in the account's zone, for a command that writes to a day it was
// not given. HEY answers a JSON request in UTC, and the machine's clock is UTC on a server
// and in most sandboxes, so in New York after 20:00 either would write to tomorrow. Without
// the account's zone the write is refused rather than guessed at, and hint names the date
// argument that would do instead.
func (a *accountZone) todayToWrite(ctx context.Context, hint string) (time.Time, error) {
	_, loc, err := a.location(ctx)
	if err != nil {
		return time.Time{}, accountZoneRefusal(err, todayUnknown, hint)
	}
	return calendarDay(clockNow().In(loc)), nil
}

// todayToRead is today in the account's zone, for a command that reads a day it was not
// given. An account with no zone is a lasting state rather than a failure, and refusing
// would leave the command unusable without a date, so the read takes the machine's today —
// as HEY's web app takes the browser's — and says so on stderr. A read of the identity that
// fails is refused as a write's is: it is worth a retry, and a guessed day is not.
func (a *accountZone) todayToRead(ctx context.Context, stderr io.Writer, hint string) (time.Time, error) {
	_, loc, err := a.location(ctx)
	if unknown, ok := errors.AsType[*unknownAccountZoneError](err); ok {
		today := calendarDay(clockNow())
		fmt.Fprintf(stderr, "Notice: %s, so today is %s by this machine's clock\n", unknown.reason, today.Format(dateLayout))
		return today, nil
	}
	if err != nil {
		return time.Time{}, accountZoneRefusal(err, todayUnknown, hint)
	}
	return calendarDay(clockNow().In(loc)), nil
}

// calendarDay is the day an instant falls on where it was read, as a date with no zone left
// in it, so that adding days to it never meets a clock change.
func calendarDay(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// unknownAccountZoneError is an account whose zone cannot be used: none is set, or this build
// does not know the one that is.
type unknownAccountZoneError struct {
	reason string
	cause  error
}

func (e *unknownAccountZoneError) Error() string { return e.reason }
func (e *unknownAccountZoneError) Unwrap() error { return e.cause }

// accountZoneRefusal says why there is no zone to work in, what it was needed for, and what
// to pass instead. An account without a usable zone is a usage error. A failed read keeps
// what it failed with — network, rate limit, a server error — so a script can tell a retry
// from a mistake. A login HEY no longer accepts is an auth failure and nothing else: the
// hint would only move the refusal to the next request.
func accountZoneRefusal(err error, purpose, hint string) error {
	if unknown, ok := errors.AsType[*unknownAccountZoneError](err); ok {
		return &apierr.Error{
			Code:    apierr.CodeUsage,
			Message: purpose + ": " + unknown.reason,
			Hint:    hint,
			Cause:   unknown.cause,
		}
	}

	readErr := *apierr.AsError(apierr.FromSDK(err))
	if readErr.Code == apierr.CodeAuth {
		return &readErr
	}
	readErr.Message = purpose + ": your HEY account's time zone could not be read: " + readErr.Message
	readErr.Hint = hint
	if readErr.Cause == nil {
		readErr.Cause = err
	}
	return &readErr
}
