package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestEventDetailOpensOnlyHostedWebLinks(t *testing.T) {
	for _, test := range []struct {
		name string
		link string
		want bool
	}{
		{name: "http link", link: "http://meet.example.com/roadmap", want: true},
		{name: "HTTPS link (uppercase scheme)", link: "HTTPS://meet.example.com/roadmap", want: true},
		{name: "hostless HTTPS link", link: "https:roadmap", want: false},
		{name: "empty HTTPS host", link: "https://", want: false},
		{name: "port only host (https://:443)", link: "https://:443/path", want: false},
		{name: "file link", link: "file:///etc/passwd", want: false},
		{name: "application scheme", link: "zoomus://zoom.us/join/123", want: false},
		{name: "empty link", link: "", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			detail := &eventDetail{event: Recording{Link: test.link}}
			_, got := detail.openableLink()
			if got != test.want {
				t.Errorf("openableLink(%q) = %v, want %v", test.link, got, test.want)
			}
		})
	}
}

// Enter on the week view opens the selected event's card — the same as on the day view.
func TestEnterOpensTheEventCardOnWeekView(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.viewMode = viewWeek
	v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 8, Title: "Team sync", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T10:00:00"), EndsAt: atLocal("2026-08-20T11:00:00")},
	}})

	// In the week view ↑/↓ walk events within the current day.
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "8" {
		t.Fatalf("down did not select the event in week view (selectedEvent=%q)", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("enter"))
	if v.detail == nil {
		t.Fatal("enter on week view did not open the event card")
	}
	if !strings.Contains(stripANSI(v.detail.view()), "Team sync") {
		t.Errorf("week-view card does not show the event title:\n%s", stripANSI(v.detail.view()))
	}
	v.HandleContentKey(keyPress("esc"))
	if v.detail != nil {
		t.Fatal("esc did not close the card on week view")
	}
}

// An all-day event with a same-day end should show as a single day, not a range.
func TestAllDayEventSameDayEndShowsAsSingleDay(t *testing.T) {
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: at("2026-08-21T00:00:00Z"),
		EndsAt:   at("2026-08-21T23:59:59Z"),
	}}
	got := d.when()
	if strings.Contains(got, "–") {
		t.Errorf("same-day all-day event showed a range: %q", got)
	}
	if !strings.Contains(got, "all day") {
		t.Errorf("all-day event missing 'all day': %q", got)
	}
}

// An all-day event whose end is exactly midnight of the next day (exclusive) should show as
// a single day, not "Aug 21 – Aug 22".
func TestAllDayEventExclusiveMidnightEndShowsAsSingleDay(t *testing.T) {
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: at("2026-08-21T00:00:00Z"),
		EndsAt:   at("2026-08-22T00:00:00Z"), // exclusive midnight
	}}
	got := d.when()
	if strings.Contains(got, "–") {
		t.Errorf("exclusive-midnight all-day event showed a range: %q", got)
	}
	if !strings.Contains(got, "all day") {
		t.Errorf("all-day event missing 'all day': %q", got)
	}
}

// A multi-day all-day event should still show the range.
func TestAllDayEventMultiDayShowsRange(t *testing.T) {
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: at("2026-08-21T00:00:00Z"),
		EndsAt:   at("2026-08-23T00:00:00Z"), // exclusive — event covers Aug 21 & 22
	}}
	got := d.when()
	if !strings.Contains(got, "–") {
		t.Errorf("multi-day all-day event did not show a range: %q", got)
	}
}

// wrapText must hard-wrap a single token that exceeds maxWidth.
func TestWrapTextHardWrapsLongTokens(t *testing.T) {
	long := "https://meet.example.com/a-very-long-room-name-that-exceeds-the-modal-width"
	lines := wrapText(long, 20)
	for _, line := range lines {
		if displayWidth(line) > 20 {
			t.Errorf("wrapText produced a line wider than maxWidth: %q (width=%d)", line, displayWidth(line))
		}
	}
	rejoined := strings.Join(lines, "")
	if rejoined != long {
		t.Errorf("wrapText lost characters: got %q, want %q", rejoined, long)
	}
}

// wrapText must split at grapheme boundaries, never inside a rune or emoji sequence.
func TestWrapTextSplitsAtGraphemeBoundaries(t *testing.T) {
	// A string of 5 two-cell emoji, each 2 cells wide, total 10 cells.
	emoji := "🎉🎊🎈🎁🎀"
	lines := wrapText(emoji, 4) // 4-cell budget: fits two emoji per line
	for _, line := range lines {
		w := displayWidth(line)
		if w > 4 {
			t.Errorf("wrapText produced a line wider than maxWidth: %q (width=%d)", line, w)
		}
		// Verify each line decodes as valid UTF-8 grapheme clusters.
		for rest := line; rest != ""; {
			cluster, _ := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
			if cluster == "" {
				t.Errorf("wrapText produced invalid UTF-8 in %q", line)
				break
			}
			rest = rest[len(cluster):]
		}
	}
}

// When the leading grapheme of a word is wider than maxWidth, wrapText must still advance
// past it and continue wrapping the suffix rather than looping forever or dropping it.
func TestWrapTextAdvancesPastOversizedLeadingGrapheme(t *testing.T) {
	// A 3-cell wide emoji followed by ASCII; maxWidth=2 means the emoji cannot fit.
	// wrapText should emit the emoji alone and then wrap the rest normally.
	s := "🎉abc"
	lines := wrapText(s, 2)
	rejoined := strings.Join(lines, "")
	if rejoined != s {
		t.Errorf("wrapText lost characters: got %q, want %q", rejoined, s)
	}
}

// On a narrow terminal the when() line must not exceed the modal content width.
func TestEventCardWhenLineWrapsOnNarrowTerminal(t *testing.T) {
	d := &eventDetail{
		event:  Recording{StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:00:00")},
		styles: testVC().styles,
		width:  40, // narrow terminal
		height: 20,
	}
	content := d.content()
	for _, line := range strings.Split(content, "\n") {
		stripped := ansi.Strip(line)
		if displayWidth(stripped) > modalContentWidth(40) {
			t.Errorf("content line exceeds modal content width on narrow terminal: %q (width=%d)", stripped, displayWidth(stripped))
		}
	}
}

// On a very narrow terminal (contentWidth <= labelWidth) the card must use stacked layout.
func TestEventCardUsesStackedLayoutOnVeryNarrowTerminal(t *testing.T) {
	d := &eventDetail{
		event: Recording{
			StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:00:00"),
			Location: "Sala 2",
		},
		styles: testVC().styles,
		width:  10, // very narrow — contentWidth will be <= labelWidth
		height: 20,
	}
	content := d.content()
	for _, line := range strings.Split(content, "\n") {
		stripped := ansi.Strip(line)
		if displayWidth(stripped) > modalContentWidth(10) {
			t.Errorf("stacked content line exceeds modal content width on very narrow terminal: %q (width=%d)", stripped, displayWidth(stripped))
		}
	}
}
