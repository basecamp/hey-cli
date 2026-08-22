package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// A to-do is named, so this modal has an input as well as a list, and one of three
// things is going on: reading the list, naming a new to-do, or renaming the one under
// the cursor.
type todoMode int

const (
	todoBrowsing todoMode = iota
	todoAdding
	todoRenaming
)

// todoPicker is the week's to-dos, opened over the calendar with s. Like the habits
// modal it is where they are managed, so the calendar keeps its keys for the calendar.
type todoPicker struct {
	todos     []Recording
	cursor    int
	mode      todoMode
	input     textinput.Model
	confirmed int64 // the to-do whose deletion has been asked for once
	status    string
}

func newTodoPicker(todos []Recording) *todoPicker {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Renew passport"
	return &todoPicker{todos: todos, input: input}
}

// setTodos keeps the cursor on the to-do it was on, which is what a write leaves
// behind: the week is read again and comes back a to-do longer or shorter.
func (p *todoPicker) setTodos(todos []Recording) {
	var onID int64
	if selected := p.selected(); selected != nil {
		onID = selected.ID
	}
	p.todos = todos
	p.cursor = min(p.cursor, max(len(todos)-1, 0))
	for i, todo := range todos {
		if todo.ID == onID {
			p.cursor = i
			break
		}
	}
	p.confirmed = 0
}

func (p *todoPicker) selected() *Recording {
	if p.cursor < 0 || p.cursor >= len(p.todos) {
		return nil
	}
	return &p.todos[p.cursor]
}

// todoInputWidth is what the input asks for. A frame hugs its widest line, so an input
// sized off the whole screen draws a box as wide as the terminal around a short list.
const todoInputWidth = 34

// resize sizes the input for the frame it is drawn in: the frame's chrome and the
// "New to-do: " label stand to its left.
func (p *todoPicker) resize(width int) {
	p.input.SetWidth(min(max(modalContentWidth(width)-12, 10), todoInputWidth))
}

func (p *todoPicker) startAdding() tea.Cmd {
	p.mode = todoAdding
	p.confirmed = 0
	p.status = ""
	p.input.SetValue("")
	return p.input.Focus()
}

// startRenaming fills the input with the title as it is shown rather than as HEY served
// it, so what the reader edits is what they were reading.
func (p *todoPicker) startRenaming() tea.Cmd {
	selected := p.selected()
	if selected == nil {
		return nil
	}
	p.mode = todoRenaming
	p.confirmed = 0
	p.status = ""
	p.input.SetValue(terminal.SanitizeLine(selected.Title))
	return p.input.Focus()
}

func (p *todoPicker) stopEditing() {
	p.mode = todoBrowsing
	p.status = ""
	p.input.Blur()
}

// title is what the input holds, and whether there is anything to make a to-do out of.
func (p *todoPicker) title() (string, bool) {
	title := strings.TrimSpace(p.input.Value())
	if title == "" {
		p.status = "Give the to-do a name"
		return "", false
	}
	return title, true
}

// renamed answers the new title for the selected to-do, and false when there is nothing
// to save. An unedited input is no rename: the input was filled with the title as the
// screen shows it, so saving it back would rewrite whatever the sanitizer took out.
func (p *todoPicker) renamed() (Recording, string, bool) {
	selected := p.selected()
	title, ok := p.title()
	if selected == nil || !ok {
		return Recording{}, "", false
	}
	if title == selected.Title || title == terminal.SanitizeLine(selected.Title) {
		return Recording{}, "", false
	}
	return *selected, title, true
}

func (p *todoPicker) editing() bool {
	return p.mode != todoBrowsing
}

func (p *todoPicker) update(msg tea.Msg) tea.Cmd {
	if !p.editing() {
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

func (p *todoPicker) moveCursor(msg tea.KeyPressMsg) {
	p.cursor = stepListCursor(p.cursor, len(p.todos), msg)
	p.confirmed = 0
}

// draw puts the picker over the calendar it was opened from.
func (p *todoPicker) draw(base string, width, height int) string {
	contentWidth := modalContentWidth(width)
	visible := modalContentRows(height)
	for _, line := range []bool{p.editing(), p.status != ""} {
		if line {
			visible = max(visible-2, 1)
		}
	}

	var rows []string
	start, end := modalListWindow(len(p.todos), p.cursor, visible)
	for i := start; i < end; i++ {
		todo := p.todos[i]
		done := todo.Done()

		marker, markerStyle := "□", lipgloss.NewStyle().Foreground(colorAlert).Bold(true)
		labelStyle := lipgloss.NewStyle().Foreground(colorBright)
		if done {
			marker, markerStyle, labelStyle = "■", styleMuted, styleMuted
		}
		prefix := "  "
		if i == p.cursor {
			prefix = "› "
			// While the input is open the cursor keeps its place but gives up the
			// highlight: the reader is typing, not choosing.
			if !p.editing() {
				labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
			}
		}
		title := truncateToWidth(terminal.SanitizeLine(todo.Title), max(contentWidth-4, 1))
		rows = append(rows, prefix+markerStyle.Render(marker)+" "+labelStyle.Render(title))
	}

	body := strings.Join(rows, "\n")
	if len(p.todos) == 0 {
		body = styleMuted.Render("Nothing to do this week")
	}
	if p.editing() {
		label := "New to-do: "
		if p.mode == todoRenaming {
			label = "Rename: "
		}
		body += "\n\n" + styleMuted.Render(label) + p.input.View()
	}
	if p.status != "" {
		body += "\n\n" + styleMuted.Render(truncateToWidth(terminal.SanitizeLine(p.status), contentWidth))
	}
	return overlayModal(base, modalFrame(todosSectionLabel, body, width), width, height)
}

func (p *todoPicker) helpBindings() []helpBinding {
	if p.editing() {
		save := "add"
		if p.mode == todoRenaming {
			save = "rename"
		}
		return []helpBinding{{"enter", save}, {"esc", "cancel"}}
	}

	bindings := []helpBinding{{"↑↓", "choose"}}
	if selected := p.selected(); selected != nil {
		doneLabel := "mark done"
		if selected.Done() {
			doneLabel = "clear"
		}
		deleteLabel := "delete"
		if p.confirmed == selected.ID {
			deleteLabel = "press x again to delete"
		}
		bindings = append(bindings,
			helpBinding{"enter", doneLabel},
			helpBinding{"e", "rename"},
			helpBinding{"x", deleteLabel})
	}
	return append(bindings, helpBinding{"a", "new to-do"}, helpBinding{"esc", "close"})
}
