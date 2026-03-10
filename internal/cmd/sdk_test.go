package cmd

import (
	"testing"
	"time"
)

func TestFormatTimestampUTC(t *testing.T) {
	// 2024-01-15 00:00:00 UTC
	// In a non-UTC local timezone (e.g. America/Los_Angeles, UTC-8) this
	// would render as 2024-01-14 if formatted in local time.
	ts := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	want := "2024-01-15T00:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

func TestFormatTimestampMidDay(t *testing.T) {
	ts := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	want := "2024-01-15T14:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

func TestFormatTimestampZero(t *testing.T) {
	var ts time.Time
	got := formatTimestamp(ts)
	if got != "" {
		t.Errorf("formatTimestamp(zero) = %q, want empty", got)
	}
}

func TestFormatDateUTC(t *testing.T) {
	// 2024-01-15 00:00:00 UTC — must stay 2024-01-15 regardless of local timezone
	ts := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	got := formatDate(ts)
	want := "2024-01-15"
	if got != want {
		t.Errorf("formatDate = %q, want %q", got, want)
	}
}

func TestFormatDateZero(t *testing.T) {
	var ts time.Time
	got := formatDate(ts)
	if got != "" {
		t.Errorf("formatDate(zero) = %q, want empty", got)
	}
}
