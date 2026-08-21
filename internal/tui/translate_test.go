package tui

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// These translators are the seam between the SDK's shapes and the TUI's own, and what is
// worth pinning in one is the decision it makes rather than the fields it copies. A
// posting and an entry are described by internal/mail now, and tested there.

func TestSDKRecordingToModelFormatsItsTimes(t *testing.T) {
	got := sdkRecordingToModel(generated.Recording{
		Id: 99, Title: "Standup", Type: "Calendar::Habit",
		StartsAt:    time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC),
		EndsAt:      time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC),
	})

	if got.StartsAt != "2026-08-18T14:00:00Z" || got.EndsAt != "2026-08-18T15:00:00Z" {
		t.Errorf("times = %q / %q", got.StartsAt, got.EndsAt)
	}
	if got.CompletedAt != "2026-08-18T16:00:00Z" {
		t.Errorf("completed = %q", got.CompletedAt)
	}
}

// An incomplete recording is the common case — a todo has no end, an open one no
// completion — and those have to read as empty rather than as a zero date.
func TestSDKRecordingToModelLeavesMissingTimesEmpty(t *testing.T) {
	got := sdkRecordingToModel(generated.Recording{Id: 5, Title: "Buy milk", Type: "Calendar::Todo"})

	if got.StartsAt != "" || got.EndsAt != "" || got.CompletedAt != "" {
		t.Errorf("zero times should read as empty: %+v", got)
	}
}
