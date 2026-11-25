package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/mail"
)

func TestSingleLineMailFitsOneRowWithSenderSubjectAndDate(t *testing.T) {
	p := mail.Posting{ID: 1, Name: "October workshop schedule", Summary: "Please bring the printed agenda.", Creator: mail.Contact{Name: "Morgan Shaw"}, CreatedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), IsBundle: true}
	l := contentList{}
	l.setSize(100, 10)
	l.setPostings([]mail.Posting{p})
	rows := strings.Split(strings.TrimSuffix(ansi.Strip(l.view()), "\n"), "\n")
	if len(rows) != 2 {
		t.Fatalf("want one header and one mail row, got %d", len(rows))
	}
	got := rows[1]
	for _, part := range []string{"│ ● ", "Morgan Shaw", "▪ October workshop schedule", formatDisplayDate(p.CreatedAt)} {
		if !strings.Contains(got, part) {
			t.Errorf("row missing %q: %q", part, got)
		}
	}
	if strings.Contains(got, "\n") || strings.Contains(got, p.Summary) {
		t.Fatal("single-line row still has a preview")
	}
	l.toggleSelected()
	if !strings.Contains(ansi.Strip(l.view()), "│ ✓ ") {
		t.Fatal("single-line row lost selection marker")
	}
	l.postings[0].Creator.Name = "Alexandra Montgomery-Williams"
	l.setSize(160, 10)
	row := strings.Split(strings.TrimSuffix(ansi.Strip(l.view()), "\n"), "\n")[1]
	if !strings.Contains(row, "Alexandra Montg...") || !strings.HasSuffix(row, formatDisplayDate(p.CreatedAt)) || displayWidth(row) != 160 {
		t.Fatalf("sender truncation or right-aligned date changed: %q", row)
	}
	for _, width := range []int{12, 20, 40, 80, 120} {
		for _, name := range []string{"Alexandra Montgomery-Williams", "山田 太郎 ✈️"} {
			l.postings[0].Creator.Name = name
			l.postings[0].Name = strings.Repeat("旅程の相談 ", 25)
			l.setSize(width, 10)
			for _, line := range strings.Split(ansi.Strip(l.view()), "\n") {
				if w := displayWidth(line); w > width {
					t.Errorf("row width=%d at %d columns", w, width)
				}
			}
		}
	}
}

func TestSingleLineMailPagingAndCover(t *testing.T) {
	postings := make([]mail.Posting, 0, 30)
	for i := range 30 {
		postings = append(postings, mail.Posting{ID: int64(i + 1), Name: fmt.Sprintf("Workshop invitation %d", i+1)})
	}
	l := contentList{hideSeenState: true}
	l.setSize(80, 10)
	l.setPostings(postings)
	if got := l.visibleItemsFrom(0); got != 10 {
		t.Fatalf("single-line fits %d, want 10", got)
	}
	l.cursor = 2
	l.pageDown()
	if l.scrollOff != 10 || l.cursor != 12 {
		t.Fatalf("page down: scroll=%d cursor=%d", l.scrollOff, l.cursor)
	}
	l.pageUp()
	if l.scrollOff != 0 || l.cursor != 2 {
		t.Fatalf("page up: scroll=%d cursor=%d", l.scrollOff, l.cursor)
	}
	l = contentList{}
	l.setSize(80, 18)
	for i := range postings {
		postings[i].Seen = i >= 2
	}
	l.setPostings(postings)
	l.setCover(coverTopo)
	if strings.Contains(ansi.Strip(l.view()), "Workshop invitation 3") {
		t.Fatal("single-line list exposed covered mail")
	}
	if got := l.itemCount(); got != 2 {
		t.Fatalf("cover exposes %d threads", got)
	}
	// An entirely covered list has no cursor or negative scroll offset.
	l.setPostings(postings[2:])
	if l.cursor != noSelection || l.scrollOff < 0 {
		t.Fatalf("covered cursor=%d scroll=%d", l.cursor, l.scrollOff)
	}
	_ = l.view()
}

func TestMailListsUseSingleLineLayoutWithoutASettingOrShortcut(t *testing.T) {
	m := modelWithBoxes()
	for _, list := range []*contentList{&m.mailView.postingList, &m.mailView.searchList, &m.mailView.bundleList, &m.mailView.seenList} {
		list.hideSeenState = true
		list.setSize(100, 10)
		list.setPostings(testPostings())
		if got := strings.Count(list.view(), "\n"); got != 2 {
			t.Fatalf("two threads occupy %d lines, want 2", got)
		}
	}
	original := m.mailView.postingList.view()
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModAlt}))
	m = updated.(model)
	if cmd != nil || m.mailView.postingList.view() != original {
		t.Fatal("removed density shortcut changed the mail list")
	}
	if strings.Contains(strings.Join(m.help.dialogLines(80), "\n"), "alt+v") {
		t.Fatal("help still advertises the removed shortcut")
	}
	updated, _ = m.applyMailAccount(mailAccountChoice{id: 2, label: "Work"}, nil)
	m = updated.(model)
	m.mailView.postingList.hideSeenState = true
	m.mailView.postingList.setSize(100, 10)
	m.mailView.postingList.setPostings(testPostings())
	if got := strings.Count(m.mailView.postingList.view(), "\n"); got != 2 {
		t.Fatal("account switch restored two-line rows")
	}
}
