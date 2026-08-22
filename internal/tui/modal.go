package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// modal is what is open over the mail list: a form or a picker that holds every key
// while it is up. Only one is ever open, so mailView keeps one of these rather than a
// pointer per kind, and the questions the app asks — which keys go where, what is on
// screen, what the help bar says — have one answer each instead of one per ladder.
type modal interface {
	// handleKey routes one key press, and answers whether the modal stays open. The
	// closing is mailView's to do, so escape and a committed choice are the same
	// thing here: a modal that is finished says so and lets go.
	handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool)

	// handleMsg routes what is not a key press — cursor blinks and the other component
	// messages — and answers whether the modal took it. A picker takes nothing, so
	// whatever is on screen behind it goes on receiving them.
	handleMsg(msg tea.Msg) (tea.Cmd, bool)

	// draw renders the modal. A modal that composites itself over the list asks the
	// view for what is beneath it; one that replaces the screen ignores it.
	draw(view *mailView) string

	helpBindings() []helpBinding
	resize(width, height int)
	restyle(styles styles)
}

// plainModal is embedded by a modal that draws from what it is handed at render time
// and only wants key presses: there is nothing to keep on a resize or a restyle, and
// a cursor blink belongs to whatever is on screen behind it.
type plainModal struct{}

func (plainModal) handleMsg(tea.Msg) (tea.Cmd, bool) { return nil, false }

func (plainModal) resize(_, _ int) {}

func (plainModal) restyle(_ styles) {}

// modalOf answers the open modal when it is the one asked for, so the few places that
// care which modal is up can ask for it by name.
func modalOf[T modal](view *mailView) T {
	open, _ := view.modal.(T)
	return open
}

// A modal's border takes a column on each side and its padding two more, so a frame
// spends six columns on chrome; the border and the title's blank line spend six rows.
// A frame is never given a width to fill, so it hugs its widest line and these are the
// ceiling rather than the size.
const (
	modalChromeWidth = 6
	modalChromeRows  = 6
)

func modalContentWidth(width int) int { return max(width-modalChromeWidth, 1) }

func modalContentRows(height int) int { return max(height-modalChromeRows, 1) }

// overlayModal draws a modal over the view that opened it, centered, so the list or
// the calendar behind it stays on screen around the border.
func overlayModal(base, modal string, width, height int) string {
	x := max((width-lipgloss.Width(modal))/2, 0)
	y := max((height-lipgloss.Height(modal))/2, 0)
	return overlayAt(base, modal, x, y, width, height)
}

// overlayAt composites one layer over another at a given cell. It is where every
// overlay in the TUI ends up, so there is one answer to how layers are drawn.
func overlayAt(base, layer string, x, y, width, height int) string {
	compositor := lipgloss.NewCompositor(
		lipgloss.NewLayer(base).Z(0),
		lipgloss.NewLayer(layer).X(x).Y(y).Z(1),
	)
	canvas := lipgloss.NewCanvas(width, height)
	compositor.Draw(canvas, canvas.Bounds())
	return canvas.Render()
}

// modalFrame is the box every modal wears: a rounded border in the chrome color with a
// title above the body. What the body is — a column of names, a form — is the modal's
// own business.
func modalFrame(title, body string, width int) string {
	heading := lipgloss.NewStyle().
		Foreground(colorChrome).
		Bold(true).
		Render(truncateToWidth(title, modalContentWidth(width)))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorChrome).
		Padding(0, 2).
		Render(heading + "\n\n" + body)
}

// framedList is a modal that chooses one of a list: the names in a frame, the cursor
// marked, scrolled to whatever the screen has room for.
func framedList(title string, names []string, cursor, width, height int) string {
	rows := modalListRows(names, cursor, modalContentWidth(width), modalContentRows(height))
	return modalFrame(title, strings.Join(rows, "\n"), width)
}

// modalListRows draws the names a modal is choosing between, marking the cursor and
// scrolling to keep it on screen. It is separate from framedList because a picker with
// something to say underneath its list — a switch in flight, an error — builds its own
// body out of these rows and gets the same list either way.
func modalListRows(names []string, cursor, contentWidth, visible int) []string {
	selected := lipgloss.NewStyle().Foreground(colorActive).Bold(true)

	start, end := modalListWindow(len(names), cursor, visible)
	rows := make([]string, 0, visible)
	for i := start; i < end; i++ {
		name := truncateToWidth(names[i], max(contentWidth-2, 1))
		if i == cursor {
			rows = append(rows, selected.Render("› "+name))
		} else {
			rows = append(rows, "  "+name)
		}
	}
	return rows
}

// modalListWindow is the slice of a list a modal has room for, scrolled to keep the
// cursor on screen. A modal whose rows carry their own colors builds them itself — a
// row cannot be truncated once it holds escape sequences — and shares the scrolling.
func modalListWindow(count, cursor, visible int) (start, end int) {
	if cursor >= visible {
		start = cursor - visible + 1
	}
	return start, min(start+visible, count)
}

// stepListCursor moves a modal's cursor within a list of count items, and is why a
// picker does not carry its own arrow-key ladder.
func stepListCursor(cursor, count int, msg tea.KeyPressMsg) int {
	switch msg.Key().Code {
	case tea.KeyUp:
		if cursor > 0 {
			return cursor - 1
		}
	case tea.KeyDown:
		if cursor < count-1 {
			return cursor + 1
		}
	}
	return cursor
}
