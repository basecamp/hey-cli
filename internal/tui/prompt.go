package tui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// ErrCanceled reports that the user dismissed a prompt (esc or ctrl+c).
var ErrCanceled = errors.New("canceled")

// ErrNotInteractive reports that a prompt was requested without a terminal.
var ErrNotInteractive = errors.New("interactive prompt requires a terminal")

// Confirm shows a yes/no prompt on the terminal (rendered to stderr so stdout
// stays data) and reports the choice. Enter accepts the highlighted answer,
// y/n answer directly, arrows or h/l move, esc and ctrl+c cancel.
func Confirm(message string, defaultYes bool) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // G115: fd fits in int
		return false, ErrNotInteractive
	}

	p := tea.NewProgram(newConfirmModel(message, defaultYes), tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return false, err
	}
	m, ok := final.(confirmModel)
	if !ok {
		return false, ErrCanceled
	}
	if m.canceled {
		return false, ErrCanceled
	}
	return m.value, nil
}

type confirmModel struct {
	message  string
	value    bool
	canceled bool
	styles   confirmStyles
}

type confirmStyles struct {
	message  lipgloss.Style
	selected lipgloss.Style
	option   lipgloss.Style
	help     lipgloss.Style
}

func newConfirmModel(message string, defaultYes bool) confirmModel {
	return confirmModel{
		message: message,
		value:   defaultYes,
		styles: confirmStyles{
			message:  lipgloss.NewStyle().Bold(true),
			selected: lipgloss.NewStyle().Foreground(colorPrimary).Bold(true),
			option:   lipgloss.NewStyle().Foreground(colorMuted),
			help:     lipgloss.NewStyle().Foreground(colorMuted),
		},
	}
}

func (m confirmModel) Init() tea.Cmd {
	return nil
}

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y":
		m.value = true
		return m, tea.Quit
	case "n", "N":
		m.value = false
		return m, tea.Quit
	case "enter":
		return m, tea.Quit
	case "left", "right", "h", "l", "tab":
		m.value = !m.value
		return m, nil
	case "esc", "ctrl+c":
		m.canceled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() tea.View {
	yes, no := m.styles.option.Render("Yes"), m.styles.option.Render("No")
	if m.value {
		yes = m.styles.selected.Render("▸ Yes")
	} else {
		no = m.styles.selected.Render("▸ No")
	}

	line := m.styles.message.Render(m.message) + "  " + yes + "   " + no + "\n" +
		m.styles.help.Render("  y/n choose · enter accept · esc cancel") + "\n"
	return tea.NewView(line)
}
