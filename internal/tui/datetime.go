package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// A moment on a form is three questions that only make sense together — which day, what
// time of day, and on whose clock — so dateTimePicker asks them as one widget with one
// answer. A form embeds one per moment and reads date(), clock() and zoneName() off it.
//
// The zone is a picker rather than a field because there are some six hundred IANA names
// and no reader knows whether theirs is Europe/Kiev or Europe/Kyiv. Its first option is
// literally Local, which means "send no zone": the caller converts to UTC, which needs no
// name and is exact.
const (
	dateTimeFieldDate = iota
	dateTimeFieldTime
	dateTimeFieldZone
	dateTimeFieldCount
)

// localZoneLabel is the option that means the reader's own clock. No IANA name can collide
// with it: every name kept by zoneNamesFrom has a slash in it.
const localZoneLabel = "Local"

// zoneMatchRows is how much of the filtered list is on screen at once. The list is a
// side-effect of typing, and a form is not the place for a full-height scroller.
const zoneMatchRows = 6

type dateTimePicker struct {
	dateInput textinput.Model
	timeInput textinput.Model
	allDay    bool
	focus     int

	// zone is the chosen IANA name, or localZoneLabel for the reader's own clock. It is
	// held as a label rather than a *time.Location so that a name this machine's zoneinfo
	// does not know can still be shown back to the reader who saved it.
	zone    string
	choices []string

	// zoneFilter and zoneMatches are live only while the list is open, which is also the
	// only time this widget owns keys the enclosing form would otherwise take.
	zoneOpen    bool
	zoneFilter  textinput.Model
	zoneMatches []string
	zoneCursor  int
}

// newDateTimePicker starts on a moment. An all-day moment has no time of day and no zone,
// so those fields are absent rather than empty — a date is not at a time of day anywhere.
func newDateTimePicker(at time.Time, allDay bool) *dateTimePicker {
	picker := &dateTimePicker{
		dateInput:  dateTimeInput("2026-08-22", 10),
		timeInput:  dateTimeInput("09:00", 5),
		zoneFilter: dateTimeInput("type to filter", 26),
		allDay:     allDay,
		zone:       localZoneLabel,
		choices:    zoneChoices(),
	}
	picker.dateInput.SetValue(at.Format("2006-01-02"))
	picker.timeInput.SetValue(at.Format("15:04"))
	return picker
}

func dateTimeInput(placeholder string, width int) textinput.Model {
	input := newTextInput()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.SetWidth(width)
	return input
}

// setZoneName puts a saved event's own zone back on the widget. An empty name is the
// reader's own clock, which is what an event with no zone was written on.
func (p *dateTimePicker) setZoneName(name string) {
	if name == "" {
		p.zone = localZoneLabel
	} else {
		p.zone = name
	}
	if !slices.Contains(p.choices, p.zone) {
		p.choices = append(p.choices, p.zone)
	}
}

func (p *dateTimePicker) setAllDay(allDay bool) {
	p.allDay = allDay
	if allDay {
		p.closeZoneList()
		if p.focus != dateTimeFieldDate {
			p.focus = dateTimeFieldDate
			p.focusCurrent()
		}
	}
}

func (p *dateTimePicker) date() string {
	return strings.TrimSpace(p.dateInput.Value())
}

func (p *dateTimePicker) clock() string {
	if p.allDay {
		return ""
	}
	return strings.TrimSpace(p.timeInput.Value())
}

// zoneName is the IANA name to send, and nothing at all for the reader's own clock: the
// caller converts those times to UTC instead of naming a zone Go may not be able to name.
func (p *dateTimePicker) zoneName() string {
	if p.allDay || p.zone == localZoneLabel {
		return ""
	}
	return p.zone
}

// moment is the date and time read as belonging to the zone chosen, which is what a form
// comparing two of these wants.
func (p *dateTimePicker) moment() (time.Time, bool) {
	in := time.Local
	if name := p.zoneName(); name != "" {
		zone, err := time.LoadLocation(name)
		if err != nil {
			return time.Time{}, false
		}
		in = zone
	}
	if p.allDay {
		at, err := time.ParseInLocation("2006-01-02", p.date(), in)
		return at, err == nil
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", p.date()+" "+p.clock(), in)
	return at, err == nil
}

// problem says the first thing wrong with what the reader filled in, and nothing when the
// widget has an answer.
func (p *dateTimePicker) problem() string {
	if _, err := time.Parse("2006-01-02", p.date()); err != nil {
		return "Date must be YYYY-MM-DD"
	}
	if p.allDay {
		return ""
	}
	if _, err := time.Parse("15:04", p.clock()); err != nil {
		return "Time must be HH:MM"
	}
	if name := p.zoneName(); name != "" {
		if _, err := time.LoadLocation(name); err != nil {
			return "That is not a time zone"
		}
	}
	return ""
}

// --- Focus ---

// fieldCount is how many fields the widget is showing, so an enclosing form can count them
// into its own tab order.
func (p *dateTimePicker) fieldCount() int {
	if p.allDay {
		return 1
	}
	return dateTimeFieldCount
}

func (p *dateTimePicker) focusFirst() tea.Cmd {
	return p.focusField(dateTimeFieldDate)
}

func (p *dateTimePicker) focusLast() tea.Cmd {
	return p.focusField(p.fieldCount() - 1)
}

func (p *dateTimePicker) focusField(field int) tea.Cmd {
	p.focus = min(max(field, 0), p.fieldCount()-1)
	return p.focusCurrent()
}

// step moves within the widget and answers false when it walked off either end, which is
// the enclosing form's cue to take the focus back.
func (p *dateTimePicker) step(delta int) (tea.Cmd, bool) {
	next := p.focus + delta
	if next < 0 || next >= p.fieldCount() {
		p.blur()
		return nil, false
	}
	return p.focusField(next), true
}

func (p *dateTimePicker) blur() {
	p.closeZoneList()
	p.dateInput.Blur()
	p.timeInput.Blur()
}

func (p *dateTimePicker) focusCurrent() tea.Cmd {
	p.dateInput.Blur()
	p.timeInput.Blur()
	switch p.focus {
	case dateTimeFieldDate:
		return p.dateInput.Focus()
	case dateTimeFieldTime:
		return p.timeInput.Focus()
	}
	return nil
}

// --- Keys ---

// capturesKeys is true while the zone list is open, and says that every key belongs to the
// widget — the list takes tab, enter and esc, which the enclosing form otherwise reads as
// next field, next field and cancel.
func (p *dateTimePicker) capturesKeys() bool {
	return p.zoneOpen
}

func (p *dateTimePicker) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if p.zoneOpen {
		return p.handleZoneKey(msg)
	}
	switch p.focus {
	case dateTimeFieldDate:
		if days, stepped := dateStep(msg); stepped {
			p.shiftDate(days)
			return nil
		}
		var cmd tea.Cmd
		p.dateInput, cmd = p.dateInput.Update(msg)
		return cmd
	case dateTimeFieldTime:
		var cmd tea.Cmd
		p.timeInput, cmd = p.timeInput.Update(msg)
		return cmd
	default:
		// A printable key both opens the list and starts the filter, so the reader can type
		// "zag" at the zone and land on Europe/Zagreb without first pressing anything.
		opened := p.openZoneList()
		if msg.Key().Text != "" {
			return tea.Batch(opened, p.handleZoneKey(msg))
		}
		return opened
	}
}

// dateStep is the day-at-a-time keys. Both pairs are here because neither means anything
// else on a date field: the arrows are unbound in a single-line text input, and a + or a -
// cannot appear in YYYY-MM-DD, so typing one is only ever a step.
func dateStep(msg tea.KeyPressMsg) (days int, stepped bool) {
	switch msg.Key().Code {
	case tea.KeyUp:
		return 1, true
	case tea.KeyDown:
		return -1, true
	}
	switch msg.String() {
	case "+", "=":
		return 1, true
	case "-":
		return -1, true
	}
	return 0, false
}

// shiftDate moves the date by whole days, and leaves a date it cannot read alone: stepping
// nonsense would replace what the reader typed with a day nobody asked for, and problem()
// is already telling them what is wrong with it.
func (p *dateTimePicker) shiftDate(days int) {
	at, err := time.Parse("2006-01-02", p.date())
	if err != nil {
		return
	}
	p.dateInput.SetValue(at.AddDate(0, 0, days).Format("2006-01-02"))
}

func (p *dateTimePicker) openZoneList() tea.Cmd {
	p.zoneOpen = true
	p.zoneFilter.SetValue("")
	p.zoneMatches = p.choices
	p.zoneCursor = max(slices.Index(p.zoneMatches, p.zone), 0)
	return p.zoneFilter.Focus()
}

func (p *dateTimePicker) closeZoneList() {
	p.zoneOpen = false
	p.zoneFilter.Blur()
	p.zoneMatches = nil
	p.zoneCursor = 0
}

func (p *dateTimePicker) handleZoneKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyEscape:
		p.closeZoneList()
		return p.focusCurrent()
	case tea.KeyEnter:
		p.pickHighlightedZone()
		return p.focusCurrent()
	case tea.KeyUp:
		p.moveZoneCursor(-1)
		return nil
	case tea.KeyDown:
		p.moveZoneCursor(1)
		return nil
	}

	var cmd tea.Cmd
	p.zoneFilter, cmd = p.zoneFilter.Update(msg)
	p.zoneMatches = matchingZones(p.choices, p.zoneFilter.Value())
	p.zoneCursor = 0
	return cmd
}

func (p *dateTimePicker) moveZoneCursor(delta int) {
	if len(p.zoneMatches) == 0 {
		return
	}
	p.zoneCursor = min(max(p.zoneCursor+delta, 0), len(p.zoneMatches)-1)
}

// pickHighlightedZone leaves the chosen zone as it was when nothing matches, so a filter
// typed past every name costs nothing.
func (p *dateTimePicker) pickHighlightedZone() {
	if p.zoneCursor >= 0 && p.zoneCursor < len(p.zoneMatches) {
		p.zone = p.zoneMatches[p.zoneCursor]
	}
	p.closeZoneList()
}

// matchingZones narrows the list by what has been typed, case-insensitively and reading an
// underscore as a space, so "new york" finds America/New_York.
func matchingZones(choices []string, filter string) []string {
	needle := normalizeZoneQuery(filter)
	if needle == "" {
		return choices
	}
	matches := make([]string, 0, len(choices))
	for _, choice := range choices {
		if strings.Contains(normalizeZoneQuery(choice), needle) {
			matches = append(matches, choice)
		}
	}
	return matches
}

func normalizeZoneQuery(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ")
}

func (p *dateTimePicker) helpBindings() []helpBinding {
	if p.zoneOpen {
		return []helpBinding{{"type", "filter"}, {"↑↓", "select"}, {"enter", "choose"}, {"esc", "close"}}
	}
	switch p.focus {
	case dateTimeFieldDate:
		return []helpBinding{{"↑↓", "day"}}
	case dateTimeFieldZone:
		return []helpBinding{{"any key", "choose zone"}}
	}
	return nil
}

// --- View ---

// view is one line — the date, the time and the zone — with the open zone list underneath
// it. The three read as one moment, which is the point of the widget; the enclosing form
// supplies the label that says which moment it is.
func (p *dateTimePicker) view() string {
	segments := []string{p.segment(dateTimeFieldDate, p.dateInput.View())}
	if !p.allDay {
		segments = append(segments,
			p.segment(dateTimeFieldTime, p.timeInput.View()),
			p.segment(dateTimeFieldZone, p.zone))
	}
	line := strings.Join(segments, styleMuted.Render(" · "))
	if !p.zoneOpen {
		return line
	}
	return line + "\n" + p.zoneListView()
}

// segment marks the focused part of the line. The two text inputs carry a cursor of their
// own, so only the zone — a picker with nothing blinking in it — needs coloring to say the
// keys belong to it.
func (p *dateTimePicker) segment(field int, value string) string {
	if field == dateTimeFieldZone {
		style := styleMuted
		if p.focus == dateTimeFieldZone {
			style = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
		}
		return style.Render(value)
	}
	return value
}

func (p *dateTimePicker) zoneListView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", styleMuted.Render("zone"), p.zoneFilter.View())

	if len(p.zoneMatches) == 0 {
		b.WriteString(styleMuted.Render("  no zone by that name"))
		return b.String()
	}

	first, last := p.zoneWindow()
	for i := first; i < last; i++ {
		if i == p.zoneCursor {
			fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(colorActive).Bold(true).
				Render("› "+p.zoneMatches[i]))
		} else {
			fmt.Fprintf(&b, "  %s\n", p.zoneMatches[i])
		}
	}
	if hidden := len(p.zoneMatches) - last; hidden > 0 {
		fmt.Fprintf(&b, "%s", styleMuted.Render(fmt.Sprintf("  %d more", hidden)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// zoneWindow keeps the highlighted match on screen while the list scrolls under it.
func (p *dateTimePicker) zoneWindow() (first, last int) {
	if len(p.zoneMatches) <= zoneMatchRows {
		return 0, len(p.zoneMatches)
	}
	first = min(max(p.zoneCursor-zoneMatchRows/2, 0), len(p.zoneMatches)-zoneMatchRows)
	return first, first + zoneMatchRows
}

// --- The zone list ---

var (
	zoneNamesOnce  sync.Once
	zoneNamesCache []string
)

// availableZones is every IANA name this machine can name, read once: walking zoneinfo is
// cheap but not cheap enough to do on a keystroke, and the database does not change while
// the TUI is running.
func availableZones() []string {
	zoneNamesOnce.Do(func() { zoneNamesCache = zoneNamesFrom(zoneinfoDirs()) })
	return zoneNamesCache
}

// zoneChoices is the list as the reader meets it: their own clock first, then the zone this
// machine keeps — the one they are most likely to want by name — and then everything else
// alphabetically.
func zoneChoices() []string {
	choices := []string{localZoneLabel}
	local := localZoneName()
	if local != "" {
		choices = append(choices, local)
	}
	for _, name := range availableZones() {
		if name != local {
			choices = append(choices, name)
		}
	}
	return choices
}

// zoneinfoDirs is where a zone database lives, $ZONEINFO first because that is where Go
// itself looks first.
func zoneinfoDirs() []string {
	dirs := make([]string, 0, 5)
	if custom := strings.TrimPrefix(os.Getenv("ZONEINFO"), ":"); custom != "" {
		dirs = append(dirs, custom)
	}
	return append(dirs,
		"/usr/share/zoneinfo",
		"/usr/share/lib/zoneinfo",
		"/usr/lib/locale/TZ",
		"/etc/zoneinfo")
}

// zoneNamesFrom reads the first directory that yields anything and falls back to a
// shortlist. A machine with no zone database — a scratch container, a $ZONEINFO pointing at
// a zip Go can read and this cannot — is a reason to offer fewer zones, never to refuse the
// form: the reader can still leave the zone on Local, which needs no database at all.
func zoneNamesFrom(dirs []string) []string {
	for _, dir := range dirs {
		if names := walkZoneinfo(dir); len(names) > 0 {
			return names
		}
	}
	return slices.Clone(fallbackZoneNames)
}

func walkZoneinfo(dir string) []string {
	var names []string
	prefix := dir + string(filepath.Separator)
	// An entry that cannot be read is one zone fewer to offer, never a reason to stop: the
	// walk is told about it with a nil entry and carries on to the next name.
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, _ error) error {
		if entry == nil {
			return nil
		}
		name := strings.TrimPrefix(path, prefix)
		if entry.IsDir() {
			if path != dir && skippedZoneDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		if isZoneName(name) {
			names = append(names, name)
		}
		return nil
	})
	slices.Sort(names)
	return slices.Compact(names)
}

// skippedZoneDir keeps out the copies of the whole database that a zoneinfo install carries
// for other time scales: posix/Europe/Zagreb is Europe/Zagreb again, and right/ counts leap
// seconds, which is not a choice anybody makes on a calendar form.
func skippedZoneDir(name string) bool {
	switch name {
	case "posix", "right", "SystemV":
		return true
	}
	return strings.HasPrefix(filepath.Base(name), ".")
}

// isZoneName keeps the names that read as places. The slash is what does the work: it drops
// the database's own files (zone.tab, leapseconds, tzdata.zi), the legacy single-word
// aliases (EST, Factory, posixrules) and localtime, none of which a reader would pick — and
// then time.LoadLocation confirms the rest is a zone rather than a file that happens to sit
// two levels down.
func isZoneName(name string) bool {
	if !strings.Contains(name, "/") || strings.HasPrefix(filepath.Base(name), ".") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// fallbackZoneNames is what the picker offers when no zone database can be read. It is a
// shortlist of the zones a HEY reader is most likely to be writing an event in, not an
// attempt at the database.
var fallbackZoneNames = []string{
	"Africa/Cairo",
	"Africa/Johannesburg",
	"Africa/Lagos",
	"America/Chicago",
	"America/Denver",
	"America/Los_Angeles",
	"America/Mexico_City",
	"America/New_York",
	"America/Sao_Paulo",
	"America/Toronto",
	"Asia/Dubai",
	"Asia/Hong_Kong",
	"Asia/Jerusalem",
	"Asia/Kolkata",
	"Asia/Seoul",
	"Asia/Shanghai",
	"Asia/Singapore",
	"Asia/Tokyo",
	"Australia/Melbourne",
	"Australia/Sydney",
	"Etc/UTC",
	"Europe/Amsterdam",
	"Europe/Berlin",
	"Europe/Copenhagen",
	"Europe/Dublin",
	"Europe/Lisbon",
	"Europe/London",
	"Europe/Madrid",
	"Europe/Moscow",
	"Europe/Paris",
	"Europe/Stockholm",
	"Europe/Warsaw",
	"Europe/Zagreb",
	"Pacific/Auckland",
	"Pacific/Honolulu",
}
