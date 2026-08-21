package tui

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// These translators are the seam between the SDK's shapes and the TUI's own, and what is
// worth pinning in one is the decision it makes: which of two sources a field comes from,
// what a missing value reads as. A posting is described by internal/mail now, and tested
// there.

func TestSDKMessageToEntry(t *testing.T) {
	created := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)
	updated := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)

	got := sdkMessageToEntry(generated.Entry{
		Id: 501, Kind: "message", Summary: "Thread summary", CreatedAt: created,
		AlternativeSenderName: "Support", AppUrl: "https://app.hey.com/messages/501",
		Creator: generated.Contact{Id: 42, Name: "Jane Dawson", EmailAddress: "jane@example.com"},
	}, generated.Message{
		Id: 501, Subject: "Thread subject", Content: `<p>Message body with a <a href="https://example.com/plan">plan</a></p>`, UpdatedAt: updated,
	})

	if got.ID != 501 || got.Kind != "message" || got.Summary != "Thread summary" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.CreatedAt != "2026-08-18T09:30:00Z" || got.UpdatedAt != "2026-08-18T11:00:00Z" {
		t.Errorf("timestamps = %q / %q", got.CreatedAt, got.UpdatedAt)
	}
	if got.Body != "Message body with a [plan](https://example.com/plan)" {
		t.Errorf("body = %q, want Markdown", got.Body)
	}
	if got.BodyHTML != `<p>Message body with a <a href="https://example.com/plan">plan</a></p>` {
		t.Errorf("bodyHTML = %q, want the original HTML", got.BodyHTML)
	}
	if got.Creator.ID != 42 || got.Creator.EmailAddress != "jane@example.com" {
		t.Errorf("creator = %+v", got.Creator)
	}
	if got.AlternativeSenderName != "Support" || got.AppURL != "https://app.hey.com/messages/501" {
		t.Errorf("sender or URL wrong: %+v", got)
	}
}

func TestSDKCalendarToModel(t *testing.T) {
	got := sdkCalendarToModel(generated.Calendar{
		Id: 4732, Name: "Rob Zolkos", Kind: "personal",
		Owned: true, Personal: true, External: false,
	})

	if got.ID != 4732 || got.Name != "Rob Zolkos" || got.Kind != "personal" {
		t.Errorf("calendar = %+v", got)
	}
	if !got.Owned || !got.Personal || got.External {
		t.Errorf("flags = %+v", got)
	}
}

func TestSDKRecordingToModel(t *testing.T) {
	starts := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	done := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)

	got := sdkRecordingToModel(generated.Recording{
		Id: 99, Title: "Standup", AllDay: false, Recurring: true,
		StartsAt: starts, EndsAt: ends,
		StartsAtTimeZone: "UTC", EndsAtTimeZone: "UTC",
		Type: "Calendar::Habit", Content: "notes", RemindersLabel: "10 minutes before",
		CompletedAt: done, Label: "work", Icon: "read", Color: "blue", Days: []int32{1, 3, 5},
	})

	if got.ID != 99 || got.Title != "Standup" || got.Type != "Calendar::Habit" {
		t.Errorf("recording = %+v", got)
	}
	if got.StartsAt != "2026-08-18T14:00:00Z" || got.EndsAt != "2026-08-18T15:00:00Z" {
		t.Errorf("times = %q / %q", got.StartsAt, got.EndsAt)
	}
	if got.CompletedAt != "2026-08-18T16:00:00Z" {
		t.Errorf("completed = %q", got.CompletedAt)
	}
	if !got.Recurring || got.AllDay {
		t.Errorf("flags = %+v", got)
	}
	if got.RemindersLabel != "10 minutes before" || got.Label != "work" {
		t.Errorf("detail = %+v", got)
	}
	if got.Icon != "read" || got.Color != "blue" || len(got.Days) != 3 || got.Days[1] != 3 {
		t.Errorf("habit fields = %+v", got)
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
