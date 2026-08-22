package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// toastAndRest unpacks the batch a section answers a mutation with, without running
// anything but the toast: a toast first, then whatever it wants run next, which is the
// order notify(...) is batched in everywhere. Use it where the rest of the answer needs
// handling a test's own way — a list that pages, say — and deliverToView otherwise.
func toastAndRest(t *testing.T, cmd tea.Cmd) (string, tea.Cmd) {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("expected a toast batched with one command, got %T", msg)
	}
	notice, ok := batch[0]().(notifyMsg)
	if !ok {
		t.Fatal("the first command in the batch is not a toast")
	}
	return notice.text, batch[1]
}

// deliverToView runs a command, following a batch into the commands it holds, giving
// the view every message but a toast and answering the toast's text.
func deliverToView(view sectionView, cmd tea.Cmd) string {
	if cmd == nil {
		return ""
	}
	switch msg := cmd().(type) {
	case notifyMsg:
		return msg.text
	case tea.BatchMsg:
		toast := ""
		for _, sub := range msg {
			if text := deliverToView(view, sub); text != "" {
				toast = text
			}
		}
		return toast
	default:
		view.Update(msg)
		return ""
	}
}

func TestToastStandsInTheTopRightAndTakesItselfAway(t *testing.T) {
	m := testModel()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = sized.(model)

	updated, cmd := m.Update(notifyMsg{text: "Habit cleared for today"})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("a toast should start its own clock")
	}

	rows := strings.Split(stripANSI(m.View().Content), "\n")
	row := -1
	for i, line := range rows {
		if strings.Contains(line, "Habit cleared for today") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("the toast is not on screen: %q", rows)
	}

	// It stands over the content rather than pushing it down, in the corner furthest
	// from the list a reader is working through.
	if row != headerHeight-1 {
		t.Errorf("toast is on row %d, want the first content row (%d)", row, headerHeight-1)
	}
	line := rows[row]
	if trailing := lipgloss.Width(line) - lipgloss.Width(strings.TrimRight(line, " ")); trailing > 1 {
		t.Errorf("toast is not against the right edge: %q", line)
	}

	// The clock names the toast it belongs to, so a stale timer cannot clear a newer one.
	stale, _ := m.Update(toastExpiredMsg{id: m.toastID - 1})
	if stale.(model).toast.text == "" {
		t.Error("an older toast's timer cleared the one on screen")
	}
	cleared, _ := m.Update(toastExpiredMsg{id: m.toastID})
	if cleared.(model).toast.text != "" {
		t.Error("the toast outlived its own timer")
	}
}

func TestToastSurvivesTheViewThatRaisedItGoingAway(t *testing.T) {
	m := testModel()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = sized.(model)

	updated, _ := m.Update(notifyMsg{text: "Contact hidden"})
	m = updated.(model)

	switched, _ := m.switchSection(sectionCalendar)
	if view := stripANSI(switched.(model).View().Content); !strings.Contains(view, "Contact hidden") {
		t.Errorf("the toast did not outlive the section that raised it: %q", view)
	}
}
