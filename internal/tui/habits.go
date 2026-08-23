package tui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// heyColors stands HEY's colors up as ANSI slots, for the reason styles.go and covers.go
// give: the reader's terminal theme defines those sixteen, so a habit or a calendar wears
// its own color in the reader's palette rather than HEY's hex. Gold takes the bright
// yellow and brown the plain one, which is what a dark yellow looks like in every theme.
//
// One vocabulary covers both, because HEY has one: `Calendar::Preference::Colored` is
// where a calendar's color comes from and a habit's is the same enum. Black is a calendar
// color only, and it takes the foreground slot rather than lipgloss.Black — the reader's
// ink, which is dark on a light theme and light on a dark one, where a literal black
// would vanish into half of them.
var heyColors = map[string]color.Color{
	"blue":   lipgloss.Blue,
	"red":    lipgloss.Red,
	"gold":   lipgloss.BrightYellow,
	"green":  lipgloss.Green,
	"teal":   lipgloss.Cyan,
	"purple": lipgloss.Magenta,
	"pink":   lipgloss.BrightMagenta,
	"brown":  lipgloss.Yellow,
	"black":  lipgloss.White,
}

// habitMarkerStyle is the style for a habit's ring: its own color where HEY gave it
// one, and the alert red every other waiting thing wears where it did not.
func habitMarkerStyle(habitColor string) lipgloss.Style {
	if slot, ok := heyColors[habitColor]; ok {
		return lipgloss.NewStyle().Foreground(slot).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colorAlert).Bold(true)
}

// habitMarker is the ring HEY fills in when a habit is done for the day.
func habitMarker(done bool) string {
	if done {
		return "●"
	}
	return "○"
}

// habitLabel is a habit as it reads in a list: its icon's emoji, then its name.
func habitLabel(habit Recording) string {
	name := terminal.SanitizeLine(habit.Title)
	if emoji := habitvalues.EmojiFor(habit.Icon); emoji != "" {
		return emoji + " " + name
	}
	return name
}

// habitPicker is the habits of the day, opened over the calendar with h. Habits are
// managed from here rather than from the calendar's own keys: a habit is picked by
// looking at the list, and the calendar keeps its keys for the calendar.
type habitPicker struct {
	habits []Recording
	cursor int
	// completable is whether keeping a habit is on offer here as well as managing one. It is
	// off on the year, where the picker is a list of habits rather than a day's: a year read
	// carries no recordings, so nothing on that screen knows what was kept on the day the
	// cursor is on. Adding, renaming and deleting go by a habit's id and are fine anywhere.
	completable bool
	confirmed   int64 // the habit whose deletion has been asked for once
	status      string
}

func newHabitPicker(habits []Recording, completable bool) *habitPicker {
	return &habitPicker{habits: habits, completable: completable}
}

// setHabits keeps the cursor on the habit it was on, which is what a save or a delete
// leaves behind: the list is read again and comes back a habit shorter or renamed.
func (p *habitPicker) setHabits(habits []Recording) {
	var onID int64
	if selected := p.selected(); selected != nil {
		onID = selected.ID
	}
	p.habits = habits
	p.cursor = min(p.cursor, max(len(habits)-1, 0))
	for i, habit := range habits {
		if habit.ID == onID {
			p.cursor = i
			break
		}
	}
	p.confirmed = 0
}

func (p *habitPicker) selected() *Recording {
	if p.cursor < 0 || p.cursor >= len(p.habits) {
		return nil
	}
	return &p.habits[p.cursor]
}

func (p *habitPicker) moveCursor(msg tea.KeyPressMsg) {
	p.cursor = stepListCursor(p.cursor, len(p.habits), msg)
	p.confirmed = 0
}

// draw puts the picker over the calendar it was opened from. Its rows carry each
// habit's own color, so it lays them out itself rather than handing plain names to
// framedList.
func (p *habitPicker) draw(base string, width, height int) string {
	contentWidth := modalContentWidth(width)
	visible := modalContentRows(height)
	if p.status != "" {
		visible = max(visible-2, 1)
	}

	var rows []string
	start, end := modalListWindow(len(p.habits), p.cursor, visible)
	for i := start; i < end; i++ {
		habit := p.habits[i]
		// Whether a habit was kept is only shown where it can be answered for: a ring drawn
		// empty on the year would be saying "not done today" off a read that never looked.
		done := p.completable && habit.Done()
		marker := habitMarkerStyle(habit.Color).Render(habitMarker(done))
		if !p.completable {
			marker = habitMarkerStyle(habit.Color).Render("·")
		}

		label := truncateToWidth(habitLabel(habit), max(contentWidth-4, 1))
		labelStyle := lipgloss.NewStyle().Foreground(colorBright)
		prefix := "  "
		if done {
			labelStyle = styleMuted
		}
		if i == p.cursor {
			labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
			prefix = "› "
		}
		rows = append(rows, prefix+marker+" "+labelStyle.Render(label))
	}

	body := strings.Join(rows, "\n")
	if len(p.habits) == 0 {
		body = styleMuted.Render("No habits yet")
	}
	if p.status != "" {
		body += "\n\n" + styleMuted.Render(truncateToWidth(terminal.SanitizeLine(p.status), contentWidth))
	}
	return overlayModal(base, modalFrame("Habits", body, width), width, height)
}

func (p *habitPicker) helpBindings() []helpBinding {
	bindings := []helpBinding{{"↑↓", "choose"}}
	if selected := p.selected(); selected != nil {
		if p.completable {
			doneLabel := "mark done"
			if selected.Done() {
				doneLabel = "clear"
			}
			bindings = append(bindings, helpBinding{"enter", doneLabel})
		}
		deleteLabel := "delete"
		if p.confirmed == selected.ID {
			deleteLabel = "press x again to delete"
		}
		bindings = append(bindings, helpBinding{"e", "edit"}, helpBinding{"x", deleteLabel})
	}
	bindings = append(bindings, helpBinding{"a", "new habit"})
	return append(bindings, helpBinding{"esc", "close"})
}
