package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/timezone"
)

// timeZoneHint is how every refusal about an event's zone says what to do instead.
const timeZoneHint = "pass --time-zone with an IANA zone name, for example --time-zone America/New_York"

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

	name, loc, err := f.account.location(ctx)
	if err != nil {
		return "", nil, accountZoneRefusal(err, "no time zone to place the event in", timeZoneHint)
	}
	return name, loc, nil
}

func errInvalidTimeZone(name string) error {
	return apierr.ErrUsageHint(fmt.Sprintf("invalid time-zone: %s", terminal.SanitizeLine(name)),
		"an IANA time zone name, spelled exactly, for example America/New_York")
}
