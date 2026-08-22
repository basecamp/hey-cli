package tui

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// These translators are the seam between the SDK's shapes and the TUI's own, and what is
// worth pinning in one is the decision it makes rather than the fields it copies. A
// posting and an entry are described by internal/mail now, and tested there.

// A timestamp is carried across as it came, and turned into the reader's own clock only
// where it is read. It used to be rendered into a UTC string here and parsed back, which is
// how the whole calendar came to be drawn in UTC.
func TestSDKRecordingToModelKeepsItsTimes(t *testing.T) {
	starts := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	got := sdkRecordingToModel(generated.Recording{
		Id: 99, Title: "Standup", Type: "Calendar::Habit",
		StartsAt:    starts,
		EndsAt:      starts.Add(time.Hour),
		CompletedAt: starts.Add(2 * time.Hour),
	})

	if !got.StartsAt.Equal(starts) || !got.EndsAt.Equal(starts.Add(time.Hour)) {
		t.Errorf("times = %v / %v", got.StartsAt, got.EndsAt)
	}
	if !got.CompletedAt.Equal(starts.Add(2 * time.Hour)) {
		t.Errorf("completed = %v", got.CompletedAt)
	}

	// Read back, it is the same instant on the reader's clock rather than on UTC's.
	if !got.Starts().Equal(starts) {
		t.Errorf("Starts() moved the instant: %v against %v", got.Starts(), starts)
	}
	if got.Starts().Location() != time.Local {
		t.Errorf("Starts() answered in %v, want the local zone", got.Starts().Location())
	}
}

// An incomplete recording is the common case — a todo has no end, an open one no
// completion — and those have to read as unset rather than as some date.
func TestSDKRecordingToModelLeavesMissingTimesUnset(t *testing.T) {
	got := sdkRecordingToModel(generated.Recording{Id: 5, Title: "Buy milk", Type: "Calendar::Todo"})

	if !got.StartsAt.IsZero() || !got.EndsAt.IsZero() || got.Done() {
		t.Errorf("missing times should read as unset: %+v", got)
	}
	if !got.Starts().IsZero() || !got.Ends().IsZero() {
		t.Errorf("a missing time should stay missing when read: %+v", got)
	}
}

// An all-day event's timestamp is a calendar date, which haystack serves as UTC midnight on
// purpose. Converting it would move a birthday to the day before for anybody west of UTC.
func TestAllDayRecordingsKeepTheirCalendarDate(t *testing.T) {
	midnight := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	allDay := Recording{Title: "🎂 Karla", AllDay: true, StartsAt: midnight, EndsAt: midnight}

	if got := allDay.Starts(); !got.Equal(midnight) || got.Day() != 21 {
		t.Errorf("an all-day event moved off its date: %v", got)
	}
	if got := allDay.Starts().Location(); got != time.UTC {
		t.Errorf("an all-day date was converted to %v", got)
	}
}
