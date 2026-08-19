package tui

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// These translators are the seam between the SDK's shapes and the TUI's own. A field
// dropped or crossed here doesn't fail to compile — it renders blank, or renders someone
// else's data — so they're worth pinning.

func TestSDKPostingToModel(t *testing.T) {
	created := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)
	updated := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)

	got := sdkPostingToModel(generated.Posting{
		Id: 1233065884, CreatedAt: created, UpdatedAt: updated,
		Kind: "topic", Name: "Life insurance", Seen: true, Bundled: true, Muted: true,
		Summary: "the summary", EntryKind: "message", AppUrl: "https://app.hey.com/topics/21",
		AlternativeSenderName: "Copilot", VisibleEntryCount: 3,
		Extenzions: []generated.Extenzion{{Id: 7, Name: "receipts"}},
		Creator:    generated.Contact{Id: 42, Name: "Jane Dawson", EmailAddress: "jane@example.com"},
	})

	if got.ID != 1233065884 || got.Name != "Life insurance" || got.Kind != "topic" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.CreatedAt != "2026-08-18T09:30:00Z" || got.UpdatedAt != "2026-08-18T11:00:00Z" {
		t.Errorf("timestamps = %q / %q", got.CreatedAt, got.UpdatedAt)
	}
	if !got.Seen || !got.Bundled || !got.Muted {
		t.Errorf("flags should carry over: %+v", got)
	}
	if got.Summary != "the summary" || got.EntryKind != "message" || got.VisibleEntryCount != 3 {
		t.Errorf("detail fields wrong: %+v", got)
	}
	if got.AppURL != "https://app.hey.com/topics/21" || got.AlternativeSenderName != "Copilot" {
		t.Errorf("url or sender wrong: %+v", got)
	}
	if len(got.Extenzions) != 1 || got.Extenzions[0].Name != "receipts" {
		t.Errorf("extenzions = %+v", got.Extenzions)
	}
	if got.Creator.ID != 42 || got.Creator.Name != "Jane Dawson" || got.Creator.EmailAddress != "jane@example.com" {
		t.Errorf("creator = %+v", got.Creator)
	}
}

func TestSDKPostingToModelLeavesMissingTimestampsEmpty(t *testing.T) {
	got := sdkPostingToModel(generated.Posting{Id: 1})

	if got.CreatedAt != "" || got.UpdatedAt != "" {
		t.Errorf("a zero time should read as empty, got %q / %q", got.CreatedAt, got.UpdatedAt)
	}
	if got.Extenzions != nil {
		t.Errorf("no extenzions should stay nil, got %+v", got.Extenzions)
	}
}

func TestSDKBoxToModel(t *testing.T) {
	got := sdkBoxToModel(generated.Box{Id: 24088, Kind: "imbox", Name: "Imbox"})

	if got.ID != 24088 || got.Kind != "imbox" || got.Name != "Imbox" {
		t.Errorf("box = %+v", got)
	}
}

func TestSDKMessageToEntry(t *testing.T) {
	created := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)
	updated := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)

	got := sdkMessageToEntry(generated.Entry{
		Id: 501, Kind: "message", Summary: "Thread summary", CreatedAt: created,
		AlternativeSenderName: "Support", AppUrl: "https://app.hey.com/messages/501",
		Creator: generated.Contact{Id: 42, Name: "Jane Dawson", EmailAddress: "jane@example.com"},
	}, generated.Message{
		Id: 501, Subject: "Thread subject", Content: "<p>Message body</p>", UpdatedAt: updated,
	})

	if got.ID != 501 || got.Kind != "message" || got.Summary != "Thread summary" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.CreatedAt != "2026-08-18T09:30:00Z" || got.UpdatedAt != "2026-08-18T11:00:00Z" {
		t.Errorf("timestamps = %q / %q", got.CreatedAt, got.UpdatedAt)
	}
	if got.Body != "<p>Message body</p>" || got.BodyHTML != got.Body {
		t.Errorf("body = %q / %q", got.Body, got.BodyHTML)
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
		Type: "Calendar::Event", Content: "notes", RemindersLabel: "10 minutes before",
		CompletedAt: done, Label: "work",
	})

	if got.ID != 99 || got.Title != "Standup" || got.Type != "Calendar::Event" {
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
	if got.Content != "notes" || got.RemindersLabel != "10 minutes before" || got.Label != "work" {
		t.Errorf("detail = %+v", got)
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
