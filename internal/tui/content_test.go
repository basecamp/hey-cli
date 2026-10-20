package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/mail"
)

func TestContentListKeepsBundleMarkerIndependent(t *testing.T) {
	for _, tt := range []struct {
		name          string
		seen          bool
		bubbledUp     bool
		selected      bool
		hideSeenState bool
		cursor        bool
		muted         bool
		wantMark      string
	}{
		{name: "unread", wantMark: "● "},
		{name: "read", seen: true, wantMark: "  "},
		{name: "bubbled up", seen: true, bubbledUp: true, wantMark: "  "},
		{name: "selected unread", selected: true, wantMark: "✓ "},
		{name: "selected read", seen: true, selected: true, wantMark: "✓ "},
		{name: "cursor", cursor: true, wantMark: "● "},
		{name: "hidden read state", hideSeenState: true, wantMark: "  "},
		{name: "hidden read state selected", hideSeenState: true, selected: true, wantMark: "✓ "},
		{name: "ignored", muted: true, wantMark: "● "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			posting := mail.Posting{
				ID: 100, Name: "October workshop schedule", IsBundle: true,
				Seen: tt.seen, BubbledUp: tt.bubbledUp, Muted: tt.muted,
				CreatedAt: time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC),
			}
			ordinary := posting
			ordinary.ID = 101
			ordinary.Name = "Lunch plans for Friday"
			ordinary.IsBundle = false
			list := contentList{hideSeenState: tt.hideSeenState}
			list.setSize(80, 20)
			list.setPostings([]mail.Posting{posting, ordinary})
			list.cursor = 1
			if tt.cursor {
				list.cursor = 0
			}
			if tt.selected {
				list.selected = map[int64]struct{}{posting.ID: {}}
			}

			view := ansi.Strip(list.view())
			var bundleLine, ordinaryLine string
			for _, line := range strings.Split(view, "\n") {
				if strings.Contains(line, posting.Name) {
					bundleLine = line
				}
				if strings.Contains(line, ordinary.Name) {
					ordinaryLine = line
				}
			}
			prefix := "  " + tt.wantMark + "▪ "
			if tt.cursor {
				prefix = "│ " + tt.wantMark + "▪ "
			}
			if tt.muted {
				prefix += "[Ignored] "
			}
			if !strings.HasPrefix(bundleLine, prefix+posting.Name) {
				t.Errorf("bundle row = %q, want prefix %q", bundleLine, prefix+posting.Name)
			}
			if ordinaryLine == "" || strings.Contains(ordinaryLine, "▪") {
				t.Errorf("ordinary row = %q, want a row without a bundle marker", ordinaryLine)
			}
			if list.postings[0].Name != posting.Name {
				t.Errorf("rendering changed the stored subject to %q", list.postings[0].Name)
			}
		})
	}
}

func TestContentListBudgetsBundleMarkerWithSubject(t *testing.T) {
	for _, width := range []int{40, 60, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			posting := mail.Posting{
				ID: 100, Name: strings.Repeat("October workshop schedule ", 5), IsBundle: true,
				CreatedAt: time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC),
			}
			ordinary := posting
			ordinary.ID, ordinary.IsBundle = 101, false
			list := contentList{hideSeenState: true}
			list.setSize(width, 20)
			list.setPostings([]mail.Posting{posting, ordinary})
			lines := strings.Split(ansi.Strip(list.view()), "\n")
			if !strings.HasPrefix(lines[0], "│   ▪ ") {
				t.Errorf("truncated bundle row loses its marker: %q", lines[0])
			}
			for _, row := range []int{0, 2} {
				if got := lipgloss.Width(lines[row]); got != width {
					t.Errorf("row width = %d, want %d: %q", got, width, lines[row])
				}
				if !strings.HasSuffix(lines[row], formatDisplayDate(posting.CreatedAt)) {
					t.Errorf("row loses the date: %q", lines[row])
				}
			}
		})
	}
}
