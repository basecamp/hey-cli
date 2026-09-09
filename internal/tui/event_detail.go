package tui

import (
	"fmt"
	"net/url"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// eventDetail is the read-only card Enter opens over a selected event: what the event carries —
// when and where it is, the link to join it, who is coming, the notes — laid out to be read
// rather than typed into. e steps through to the edit form and o opens the link; esc closes it.
//
// It holds the Recording it was opened on rather than an id: the grid read already carries the
// notes, the location, the link and the guest list (see the fields on Recording, kept for
// exactly this reason), so there is nothing here to fetch.
type eventDetail struct {
	event    Recording
	calendar string // the event's calendar by name, or "" for the personal one
	use24    bool

	body   viewport.Model
	styles styles
	width  int
	height int
}

func newEventDetail(event Recording, calendar string, use24 bool, styles styles, width, height int) *eventDetail {
	d := &eventDetail{
		event:    event,
		calendar: calendar,
		use24:    use24,
		styles:   styles,
		body:     viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
	d.resize(width, height)
	return d
}

// resize refits the card to the screen. The body is capped at what a modal has room for and
// scrolls past that, so a long set of notes does not push the frame off either end.
func (d *eventDetail) resize(width, height int) {
	d.width, d.height = width, height
	content := d.content()
	d.body.SetWidth(modalContentWidth(width))
	d.body.SetHeight(min(lineCount(content), modalContentRows(height)))
	offset := d.body.YOffset()
	d.body.SetContent(content)
	d.body.SetYOffset(offset)
}

// restyle re-renders the card with a new palette, keeping the reader's place in the notes.
func (d *eventDetail) restyle(styles styles) {
	d.styles = styles
	d.resize(d.width, d.height)
}

func (d *eventDetail) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.body, cmd = d.body.Update(msg)
	return cmd
}

func (d *eventDetail) view() string {
	return modalFrame(d.title(), d.body.View(), d.width)
}

// title is the event's own name, or the parent's for a recording that has none of its own —
// a countdown carries "10 days before" as a label and leans on the event above it for a name.
func (d *eventDetail) title() string {
	if d.event.Title != "" {
		return terminal.SanitizeLine(d.event.Title)
	}
	if d.event.ParentTitle != "" {
		return terminal.SanitizeLine(d.event.ParentTitle)
	}
	return "Event"
}

func (d *eventDetail) helpBindings() []helpBinding {
	bindings := make([]helpBinding, 0, 3)
	if _, ok := d.openableLink(); ok {
		bindings = append(bindings, helpBinding{"o", "open link"})
	}
	bindings = append(bindings, helpBinding{"e", "edit"}, helpBinding{"esc", "back"})
	return bindings
}

// openableLink is the event's link when it is a web address the OS launcher should be handed:
// http or https. Event links are server data and the edit form takes any URI with a host, so a
// shared event could carry a file:// path or an application scheme — those are shown on the
// card but not opened.
func (d *eventDetail) openableLink() (string, bool) {
	link := strings.TrimSpace(d.event.Link)
	if link == "" {
		return "", false
	}
	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	return link, true
}

// content is the card's body: the when-and-where up top, then whichever of the optional
// fields the event actually has, then the notes. A field with nothing in it is left out
// rather than shown empty, the way the web app's event popover does.
func (d *eventDetail) content() string {
	var b strings.Builder

	b.WriteString(d.styles.entryDate.Render(d.when()) + "\n")
	if label := repeatFrequencyLabel(d.event.RepeatKind); d.event.Recurring && label != "" {
		b.WriteString(styleMuted.Render("Repeats "+label) + "\n")
	}

	rows := [][2]string{
		{"Calendar", terminal.SanitizeLine(d.calendar)},
		{"Location", terminal.SanitizeLine(d.event.Location)},
		{"Link", terminal.SanitizeLine(d.event.Link)},
		{"Guests", d.guests()},
	}
	wrote := false
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		if !wrote {
			b.WriteString("\n")
			wrote = true
		}
		fmt.Fprintf(&b, "%s  %s\n", d.styles.entryFrom.Render(fmt.Sprintf("%-8s", row[0])), row[1])
	}

	if notes := strings.TrimRight(d.event.Notes, "\n"); strings.TrimSpace(notes) != "" {
		b.WriteString("\n" + d.styles.entryFrom.Render("Notes") + "\n")
		for _, line := range wrapParagraphs(terminal.Sanitize(notes), modalContentWidth(d.width)) {
			b.WriteString(line + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// when is the one line that says the whole of an event's timing: the day and the hours, on the
// reader's own clock — the same conversion Recording.Starts and Ends make, and the same one the
// grid draws by, so a zoned event is not relabelled here with a zone its shown time is not in.
// An all-day event says so instead of a clock time; one that runs past midnight names the day
// it ends on.
func (d *eventDetail) when() string {
	starts := d.event.Starts()
	if starts.IsZero() {
		return "When unknown"
	}
	ends := d.event.Ends()

	if d.event.AllDay {
		if !ends.IsZero() && ends.After(starts) {
			return starts.Format("Monday, January 2") + " – " + ends.Format("Monday, January 2") + " · all day"
		}
		return starts.Format("Monday, January 2") + " · all day"
	}

	line := starts.Format("Monday, January 2") + " · " + clockTime(starts, d.use24)
	switch {
	case ends.IsZero() || !ends.After(starts):
	case sameDay(starts, ends):
		line += "–" + clockTime(ends, d.use24)
	default:
		line += " – " + ends.Format("Monday, January 2") + " · " + clockTime(ends, d.use24)
	}
	return line
}

func (d *eventDetail) guests() string {
	if len(d.event.Attendees) == 0 {
		return ""
	}
	clean := make([]string, 0, len(d.event.Attendees))
	for _, attendee := range d.event.Attendees {
		if trimmed := terminal.SanitizeLine(attendee); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	return strings.Join(clean, ", ")
}

// repeatFrequencyLabel turns the schedule kind HEY serves — "every_week" and the like — back
// into the words the repeat picker offers, so the card and the form say a recurrence the same
// way. An unknown kind gets no line rather than a raw token.
func repeatFrequencyLabel(kind string) string {
	if kind == "" {
		return ""
	}
	for _, preset := range eventRepeatPresets {
		if string(preset.frequency) == kind {
			return preset.label
		}
	}
	return ""
}

// wrapParagraphs wraps each line to width while keeping the blank lines between paragraphs,
// which wrapText on its own drops — notes are written with those breaks and read worse without.
func wrapParagraphs(text string, width int) []string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if strings.TrimSpace(paragraph) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapText(paragraph, width)...)
	}
	return lines
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }
