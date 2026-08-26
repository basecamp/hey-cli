package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/terminal"
)

const (
	trackFieldStarts = iota
	trackFieldEnds
	trackFieldCategory
	trackFieldNotes
	trackFieldCount
)

// timeTrackForm edits one finished track, and asks what HEY's own dialog asks: when it started,
// when it ended, what it is filed under and what was written on it.
//
// HEY's update is a genuine partial — a field left out is a field left alone — so the form
// remembers what arrived and sends only what the reader changed. Two of HEY's own rules are
// visible in here rather than hidden behind a field that quietly does nothing:
//
//   - A blank category does not un-file a track. There is no clearing one through this write,
//     only moving it, so a blank field means "leave it where it is" and the form says so.
//   - A category HEY has no such name for is created by the save. That is worth knowing before
//     a typo becomes a category.
//
// Nothing here is wired to a running track: every update completes the track it addresses, and
// the tracked time list holds none.
type timeTrackForm struct {
	trackID int64

	starts   *dateTimePicker
	ends     *dateTimePicker
	category textinput.Model
	notes    textinput.Model

	// What HEY served, to compare the form against. The moments are compared to the minute
	// because that is all the pickers ask for — a track timed to the second is not moved by
	// being opened and saved.
	startsArrived   time.Time
	endsArrived     time.Time
	categoryArrived string
	notesArrived    string

	// offered are the categories the tracked time page carried, put into the field with the
	// arrows. Typing a name of one's own still works — that is how a new category is made.
	offered []string
	offer   int

	focus   int
	status  string
	isError bool
	saving  bool
	width   int
}

func newTimeTrackForm(track trackedTime, categories []generated.TimeTrackCategory) *timeTrackForm {
	form := &timeTrackForm{
		trackID:         track.ID,
		starts:          newDateTimePicker(track.StartsAt, false),
		ends:            newDateTimePicker(track.EndsAt, false),
		category:        trackInput("Client work"),
		notes:           trackInput("What this time went on"),
		startsArrived:   track.StartsAt,
		endsArrived:     track.EndsAt,
		categoryArrived: track.Category,
		notesArrived:    track.Notes,
		offered:         categoryTitles(categories),
	}
	form.category.SetValue(track.Category)
	form.category.CursorEnd()
	form.notes.SetValue(track.Notes)
	form.notes.CursorEnd()
	form.offer = max(slices.Index(form.offered, track.Category), 0)
	return form
}

func trackInput(placeholder string) textinput.Model {
	input := newTextInput()
	input.Prompt = ""
	input.Placeholder = placeholder
	return input
}

func categoryTitles(categories []generated.TimeTrackCategory) []string {
	titles := make([]string, 0, len(categories))
	for _, category := range categories {
		if title := strings.TrimSpace(category.Title); title != "" {
			titles = append(titles, title)
		}
	}
	return titles
}

func (f *timeTrackForm) init() tea.Cmd { return f.takeFocus(1) }

// timeTrackFormWidth is what the form asks for, for the reason the habit form gives: a frame
// hugs its widest line, and left alone this one would draw a box the width of the terminal
// around four short fields.
const timeTrackFormWidth = 52

const trackFieldLabelWidth = 9

func (f *timeTrackForm) resize(width, _ int) {
	f.width = min(modalContentWidth(width), timeTrackFormWidth)
	value := max(f.width-trackFieldLabelWidth-1, 10)
	f.category.SetWidth(value)
	f.notes.SetWidth(value)
}

func (f *timeTrackForm) picker(field int) *dateTimePicker {
	switch field {
	case trackFieldStarts:
		return f.starts
	case trackFieldEnds:
		return f.ends
	}
	return nil
}

// step walks the form's fields, and a picker's own fields while it is on one: the picker says
// when tab has walked off its end, and that is when the form takes the focus back.
func (f *timeTrackForm) step(delta int) tea.Cmd {
	if picker := f.picker(f.focus); picker != nil {
		if cmd, inside := picker.step(delta); inside {
			return cmd
		}
	}
	f.focus = (f.focus + delta + trackFieldCount) % trackFieldCount
	return f.takeFocus(delta)
}

func (f *timeTrackForm) takeFocus(delta int) tea.Cmd {
	f.category.Blur()
	f.notes.Blur()
	f.starts.blur()
	f.ends.blur()

	if picker := f.picker(f.focus); picker != nil {
		if delta < 0 {
			return picker.focusLast()
		}
		return picker.focusFirst()
	}
	switch f.focus {
	case trackFieldCategory:
		return f.category.Focus()
	case trackFieldNotes:
		return f.notes.Focus()
	}
	return nil
}

func (f *timeTrackForm) capturesKeys() bool {
	picker := f.picker(f.focus)
	return picker != nil && picker.capturesKeys()
}

func (f *timeTrackForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	if f.capturesKeys() {
		return f.picker(f.focus).handleKey(msg), false
	}

	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		return f.step(-1), false
	case msg.Key().Code == tea.KeyTab, msg.Key().Code == tea.KeyEnter:
		return f.step(1), false
	case msg.String() == "ctrl+s":
		return nil, f.submit()
	}

	if picker := f.picker(f.focus); picker != nil {
		return picker.handleKey(msg), false
	}
	if f.focus == trackFieldCategory && f.chooseOffered(msg) {
		return nil, false
	}
	return f.update(msg), false
}

// submit answers whether there is a write to make. A form nobody changed is not a save: HEY
// completes a track on every update, and a track this list holds is already complete, but a
// request that says nothing is still a request.
func (f *timeTrackForm) submit() bool {
	if problem := f.validate(); problem != "" {
		f.status = problem
		f.isError = true
		return false
	}
	if _, changed := f.payload(); !changed {
		f.status = "Nothing changed"
		f.isError = false
		return false
	}
	f.saving = true
	f.status = "Saving…"
	f.isError = false
	return true
}

// chooseOffered puts one of the page's categories in the field. It answers false for every
// other key, which is how typing a name of one's own goes on working.
func (f *timeTrackForm) chooseOffered(msg tea.KeyPressMsg) bool {
	if len(f.offered) == 0 {
		return false
	}
	switch msg.Key().Code {
	case tea.KeyUp:
		f.offer = (f.offer + len(f.offered) - 1) % len(f.offered)
	case tea.KeyDown:
		f.offer = (f.offer + 1) % len(f.offered)
	default:
		return false
	}
	f.category.SetValue(f.offered[f.offer])
	f.category.CursorEnd()
	return true
}

func (f *timeTrackForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch f.focus {
	case trackFieldCategory:
		f.category, cmd = f.category.Update(msg)
	case trackFieldNotes:
		f.notes, cmd = f.notes.Update(msg)
	}
	return cmd
}

func (f *timeTrackForm) validate() string {
	if problem := f.starts.problem(); problem != "" {
		return "Starts: " + problem
	}
	if problem := f.ends.problem(); problem != "" {
		return "Ends: " + problem
	}
	starts, _ := f.starts.moment()
	ends, _ := f.ends.moment()
	if ends.Before(starts) {
		return "It cannot end before it starts"
	}
	// Notes are sent as a value or not at all, and HEY reads an empty one as nothing said —
	// so there is no emptying them through this write. Saying so beats a save that looks
	// like it worked and leaves the note where it was.
	if strings.TrimSpace(f.notes.Value()) == "" && strings.TrimSpace(f.notesArrived) != "" {
		return "Notes can be rewritten here but not emptied"
	}
	return ""
}

// payload is what changed, and nothing else: an omitted field is a field HEY leaves alone.
func (f *timeTrackForm) payload() (generated.UpdateTimeTrackPayload, bool) {
	var payload generated.UpdateTimeTrackPayload
	changed := false

	if starts, ok := f.starts.moment(); ok && !starts.Equal(f.startsArrived.Truncate(time.Minute)) {
		utc := starts.UTC()
		payload.StartsAt = &utc
		changed = true
	}
	if ends, ok := f.ends.moment(); ok && !ends.Equal(f.endsArrived.Truncate(time.Minute)) {
		utc := ends.UTC()
		payload.EndsAt = &utc
		changed = true
	}
	if notes := strings.TrimSpace(f.notes.Value()); notes != strings.TrimSpace(f.notesArrived) {
		payload.Notes = notes
		changed = true
	}
	// A blank category is left out rather than sent: HEY reads it as no instruction, and the
	// form has already said that is what a blank field means.
	if title := strings.TrimSpace(f.category.Value()); title != "" && title != strings.TrimSpace(f.categoryArrived) {
		payload.CategoryTitle = title
		changed = true
	}
	return payload, changed
}

func (f *timeTrackForm) helpBindings() []helpBinding {
	if picker := f.picker(f.focus); picker != nil && picker.capturesKeys() {
		return picker.helpBindings()
	}

	bindings := []helpBinding{{"tab", "next field"}}
	if picker := f.picker(f.focus); picker != nil {
		bindings = append(bindings, picker.helpBindings()...)
	}
	if f.focus == trackFieldCategory && len(f.offered) > 0 {
		bindings = append(bindings, helpBinding{"↑↓", "a category"})
	}
	return append(bindings, helpBinding{"ctrl+s", "save"}, helpBinding{"esc", "cancel"})
}

func (f *timeTrackForm) title() string { return "Edit tracked time" }

func (f *timeTrackForm) view() string {
	var b strings.Builder
	f.writeRow(&b, "Starts", f.starts.view(), trackFieldStarts)
	f.writeRow(&b, "Ends", f.ends.view(), trackFieldEnds)
	f.writeRow(&b, "Category", f.category.View(), trackFieldCategory)
	f.writeRow(&b, "Notes", f.notes.View(), trackFieldNotes)

	b.WriteString("\n" + styleMuted.Width(max(f.width, 20)).Render(f.caution()) + "\n")
	fmt.Fprintf(&b, "%s\n", styleMuted.Render(fmt.Sprintf("%s · %s",
		formatElapsed(f.length()), f.spanLine())))

	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(truncateToWidth(terminal.SanitizeLine(f.status), max(f.width, 20))))
	}
	return strings.TrimRight(b.String(), "\n")
}

// caution says what the category field does, since neither half of it is guessable: a blank
// leaves the track filed where it is, and a name HEY does not have becomes a new category.
func (f *timeTrackForm) caution() string {
	if strings.TrimSpace(f.categoryArrived) == "" {
		return "A name HEY has no category for becomes one. Left blank, this track stays uncategorized."
	}
	return "A name HEY has no category for becomes one. Left blank, this track stays where it is — " +
		"HEY has no way to un-file one."
}

// length and spanLine are the track as the list shows it, kept in front of the reader while
// they move its ends around.
func (f *timeTrackForm) length() time.Duration {
	starts, startsOK := f.starts.moment()
	ends, endsOK := f.ends.moment()
	if !startsOK || !endsOK {
		return 0
	}
	return max(ends.Sub(starts), 0)
}

func (f *timeTrackForm) spanLine() string {
	starts, startsOK := f.starts.moment()
	if !startsOK {
		return "when this ran"
	}
	return starts.Format("Mon Jan 2 2006")
}

func (f *timeTrackForm) writeRow(b *strings.Builder, label, value string, field int) {
	labelStyle := styleMuted
	if f.focus == field {
		labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	}
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%-*s", trackFieldLabelWidth, label)), value)
}
