package tui

import (
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// A toast says what just happened — a habit ticked off, a contact saved, an attachment
// written — in the top right corner, over whatever is on screen, and takes itself away.
// It is the model's rather than each section's because it outlives the thing that raised
// it: a save that switches views should still say it saved.
//
// What stays an inline notice instead is anything describing the state of the screen —
// a list that has stopped following the server, a load that failed and wants a key, a
// confirmation waiting to be answered. Those have to be readable after two seconds.
const toastDuration = 2 * time.Second

type toastKind int

const (
	toastInfo toastKind = iota
	toastError
)

// notifyMsg raises a toast. A section asks for one by returning notify(...) as a
// command, so the request travels the same path as everything else it answers with.
type notifyMsg struct {
	text string
	kind toastKind
}

// toastExpiredMsg carries the id of the toast whose time is up, so a second toast
// raised while the first is on screen is not cleared by the first one's timer.
type toastExpiredMsg struct {
	id uint64
}

func notify(text string) tea.Cmd {
	return func() tea.Msg { return notifyMsg{text: text} }
}

func notifyError(what string, err error) tea.Cmd {
	return func() tea.Msg { return notifyMsg{text: errorNotice(what, err), kind: toastError} }
}

// showToast puts one on screen and starts its clock.
func (m *model) showToast(msg notifyMsg) tea.Cmd {
	m.toastID++
	m.toast = msg
	id := m.toastID
	return tea.Tick(toastDuration, func(time.Time) tea.Msg { return toastExpiredMsg{id: id} })
}

// toastView is the toast itself, or nothing when none is up.
func (m model) toastView() string {
	if m.toast.text == "" {
		return ""
	}
	var border color.Color = colorChrome
	text := lipgloss.NewStyle().Foreground(colorBright)
	if m.toast.kind == toastError {
		border, text = colorError, lipgloss.NewStyle().Foreground(colorError)
	}

	// A toast is over the content, so it can never be wider than half the content: the
	// reader is looking at what they were doing, not at this.
	body := truncateToWidth(terminal.SanitizeLine(m.toast.text), max(m.contentWidth()/2-4, 10))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Render(text.Render(body))
}
