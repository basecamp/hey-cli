package tui

import (
	tea "charm.land/bubbletea/v2"
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
