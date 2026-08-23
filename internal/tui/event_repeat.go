package tui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// eventRepeat is how often an event comes round, and until when. It is a widget of its own
// because the second question depends on the first: an event that does not repeat has no end
// to its repetition, and the two shapes that do have one need a date or a number after them.
//
// There is no day-of-week choice here on purpose. HEY expresses one weekday set — Monday to
// Friday — and nothing else, so a day picker would offer schedules the server cannot keep.
type eventRepeat struct {
	// keep is the label of the leading choice, the one that sends no frequency at all. On a new
	// event that reads as "does not repeat"; on an event that already repeats it says which
	// schedule is being left alone, because HEY has no parameter for stopping a recurrence on a
	// whole-event write.
	keep   string
	choice int

	until     int
	untilDate textinput.Model
	count     textinput.Model
}

type repeatChoice struct {
	label     string
	frequency hey.RepeatFrequency
}

// eventRepeatPresets are the frequencies HEY offers, in the order its own form lists them.
var eventRepeatPresets = []repeatChoice{
	{"every day", hey.RepeatEveryDay},
	{"every weekday", hey.RepeatEveryWeekday},
	{"every week", hey.RepeatEveryWeek},
	{"every other week", hey.RepeatEveryOtherWeek},
	{"every month", hey.RepeatEveryDayOfMonth},
	{"every year", hey.RepeatEveryYear},
}

var eventRepeatUntils = []struct {
	label string
	until hey.RepeatUntil
}{
	{"forever", hey.RepeatUntilForever},
	{"until", hey.RepeatUntilDate},
	{"for", hey.RepeatUntilCount},
}

func newEventRepeat() *eventRepeat {
	return &eventRepeat{
		keep:      "does not repeat",
		untilDate: eventInput("YYYY-MM-DD", 10),
		count:     eventInput("10", 4),
	}
}

// keepSchedule is what an event that already repeats opens on. HEY serves the kind of schedule
// but not the date or the count it runs until, so re-sending the frequency would rewrite a
// series that stops in December as one that never stops — this offers to leave it alone, and
// names it so the reader can see what they are leaving.
func (r *eventRepeat) keepSchedule(kind string) {
	r.keep = "keeps its schedule"
	for _, preset := range eventRepeatPresets {
		if string(preset.frequency) == kind {
			r.keep = "keeps " + preset.label
		}
	}
}

func (r *eventRepeat) choices() []repeatChoice {
	return append([]repeatChoice{{r.keep, ""}}, eventRepeatPresets...)
}

func (r *eventRepeat) repeats() bool {
	return r.choices()[r.choice].frequency != ""
}

// needsValue says the chosen end wants a date or a count after it.
func (r *eventRepeat) needsValue() bool {
	return r.repeats() && eventRepeatUntils[r.until].until != hey.RepeatUntilForever
}

func (r *eventRepeat) valueInput() *textinput.Model {
	if eventRepeatUntils[r.until].until == hey.RepeatUntilDate {
		return &r.untilDate
	}
	return &r.count
}

func (r *eventRepeat) stepFrequency(msg tea.KeyPressMsg) {
	r.choice = wrapIndex(r.choice, len(r.choices()), msg)
}

func (r *eventRepeat) stepUntil(msg tea.KeyPressMsg) {
	r.until = wrapIndex(r.until, len(eventRepeatUntils), msg)
}

func (r *eventRepeat) blur() {
	r.untilDate.Blur()
	r.count.Blur()
}

// params is the recurrence to send, and nil is the answer whenever the reader picked the
// leading choice: nil leaves the event's schedule exactly as it is, which is both what a
// one-off event wants and the only thing a repeating one can be told on a whole-event write.
func (r *eventRepeat) params() *hey.RepeatParams {
	choice := r.choices()[r.choice]
	if choice.frequency == "" {
		return nil
	}

	params := &hey.RepeatParams{Frequency: choice.frequency, Until: eventRepeatUntils[r.until].until}
	if params.Until == hey.RepeatUntilDate {
		params.UntilDate = strings.TrimSpace(r.untilDate.Value())
	}
	if params.Until == hey.RepeatUntilCount {
		params.Count, _ = strconv.Atoi(strings.TrimSpace(r.count.Value()))
	}
	return params
}

func (r *eventRepeat) problem() string {
	params := r.params()
	if params == nil {
		return ""
	}
	switch params.Until {
	case hey.RepeatUntilForever:
		return ""
	case hey.RepeatUntilDate:
		if _, err := time.Parse("2006-01-02", params.UntilDate); err != nil {
			return "Repeat until must be YYYY-MM-DD"
		}
	case hey.RepeatUntilCount:
		if params.Count < 1 || params.Count > 9999 {
			return "Repeat for must be 1 to 9999 times"
		}
	}
	return ""
}

func (r *eventRepeat) frequencyLabel() string {
	return r.choices()[r.choice].label
}

func (r *eventRepeat) untilLabel() string {
	return eventRepeatUntils[r.until].label
}

// timesSuffix is the word after a count, which reads as part of the answer rather than as a
// label: "every week · for · 10 times".
func (r *eventRepeat) timesSuffix() string {
	if eventRepeatUntils[r.until].until == hey.RepeatUntilCount {
		return " times"
	}
	return ""
}
