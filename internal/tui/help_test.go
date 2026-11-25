package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDisplayDateOmitsOnlyCurrentLocalYear(t *testing.T) {
	// UTC is already in the next year; the reader's local year still decides.
	previous := time.Local
	time.Local = time.FixedZone("Reader", -5*60*60)
	t.Cleanup(func() { time.Local = previous })
	now := time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		stamp time.Time
		want  string
	}{
		{time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC), "Aug 20"},
		{time.Date(2025, 8, 20, 12, 0, 0, 0, time.UTC), "Aug 20, 2025"},
		{time.Date(2027, 8, 20, 12, 0, 0, 0, time.UTC), "Aug 20, 2027"},
		{time.Time{}, ""},
	} {
		if got := formatDisplayDateAt(tt.stamp, now); got != tt.want {
			t.Errorf("date = %q, want %q", got, tt.want)
		}
	}
}

func TestCompactFooterFitsOneLineAndAlwaysOffersHelp(t *testing.T) {
	m := modelWithBoxes()
	for _, width := range []int{12, 24, 80, 160} {
		m.help.setWidth(width)
		footer := ansi.Strip(m.help.view())
		if strings.Contains(footer, "\n") || displayWidth(footer) > width {
			t.Errorf("footer at width %d: %q", width, footer)
		}
		if !strings.Contains(footer, "? help") {
			t.Errorf("no help key at width %d: %q", width, footer)
		}
	}
	// Rare actions leave the short footer, but remain in the modal's complete help.
	full := ansi.Strip(strings.Join(m.help.dialogLines(80), "\n"))
	for _, want := range []string{"paper trail", "spam", "help"} {
		if !strings.Contains(full, want) {
			t.Errorf("full help lost %q", want)
		}
	}
	m.help.setNotice(strings.Repeat("Could not reconnect. ", 30))
	if strings.Contains(m.help.view(), "\n") || displayWidth(m.help.view()) > 160 {
		t.Fatal("long notice expanded the footer")
	}
	if !strings.Contains(strings.Join(m.help.dialogLines(80), "\n"), "Could not reconnect.") {
		t.Fatal("full help lost the long notice")
	}
}

func TestHelpDialogScrollsAndCapturesMailActions(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(model)
	originalCursor := m.mailView.postingList.cursor
	updated, _ = m.Update(keyPress("?"))
	m = updated.(model)
	for _, key := range []string{"down", "pgdown", "end"} {
		updated, _ = m.Update(keyPress(key))
		m = updated.(model)
	}
	if m.helpScroll == 0 {
		t.Fatal("help did not scroll to the remaining shortcuts")
	}
	updated, _ = m.Update(keyPress("home"))
	m = updated.(model)
	found := false
	for range 60 {
		if strings.Contains(ansi.Strip(m.View().Content), "ctrl+c") {
			found = true
			break
		}
		previous := m.helpScroll
		updated, _ = m.Update(keyPress("down"))
		m = updated.(model)
		if m.helpScroll == previous {
			break
		}
	}
	if !found {
		t.Fatal("app shortcuts are unreachable by scrolling")
	}
	updated, cmd := m.Update(keyPress("r"))
	m = updated.(model)
	if cmd != nil || m.mailView.postingList.cursor != originalCursor {
		t.Fatal("help let a list action through")
	}
	// A resize clamps the displayed window even when the old scroll offset was large.
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updated.(model)
	for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if displayWidth(line) > 40 {
			t.Fatalf("help overflow: %q", line)
		}
	}
	updated, _ = m.Update(keyPress("?"))
	m = updated.(model)
	if m.helpOpen {
		t.Fatal("help key did not close help")
	}
}

func TestHelpNeverRepeatsBindings(t *testing.T) {
	h := newHelpBar(newStyles())
	h.bindings = []helpBinding{{"enter", "open"}, {"enter", "open"}, {"f1", "help"}}
	full := ansi.Strip(strings.Join(h.dialogLines(80), "\n"))
	if n := strings.Count(full, "open"); n != 1 {
		t.Fatalf("full help repeats open %d times", n)
	}
}

func TestDesktopHelpUsesTwoColumnsWithoutLosingBindings(t *testing.T) {
	m := modelWithBoxes()
	desktop := m.help.dialogLines(84)
	narrow := m.help.dialogLines(60)
	if len(desktop) >= len(narrow) {
		t.Fatal("two columns did not shorten help")
	}
	if !strings.Contains(ansi.Strip(desktop[0]), "Actions") || !strings.Contains(ansi.Strip(desktop[0]), "Filing") {
		t.Fatal("desktop help did not place sections side by side")
	}
	full := ansi.Strip(strings.Join(desktop, "\n"))
	for _, binding := range m.help.bindings {
		if binding.desc != "" && !strings.Contains(full, binding.desc) {
			t.Errorf("two-column help lost %q", binding.desc)
		}
	}
	for _, line := range desktop {
		if lipgloss.Width(line) > 84 {
			t.Fatal("two-column help overflowed")
		}
	}
}

func TestWideFooterKeepsCommonMailActionsContextual(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m = updated.(model)
	footer := ansi.Strip(m.help.view())
	for _, want := range []string{"r reply", "f forward", "v move", "t trash", "/ search", "c compose", "? help"} {
		if !strings.Contains(footer, want) {
			t.Errorf("wide footer lost %q: %s", want, footer)
		}
	}
	m.activeView = m.contactsView
	m.section = sectionContacts
	m.updateHelpBindings()
	if strings.Contains(ansi.Strip(m.help.view()), "r reply") {
		t.Fatal("contacts footer kept a mail-only action")
	}
}

func TestHelpHintStaysVisibleWithNoticesAndForms(t *testing.T) {
	m := modelWithBoxes()
	for _, key := range []string{"?", "f1"} {
		m.help.helpKey = key
		m.help.setNotice(strings.Repeat("Could not reconnect. ", 20) + "\nTry again.")
		for _, width := range []int{12, 24, 40, 80, 160} {
			m.help.setWidth(width)
			got := ansi.Strip(m.help.view())
			if !strings.HasSuffix(got, key+" help") || strings.Contains(got, "\n") || displayWidth(got) > width {
				t.Fatalf("footer at %d: %q", width, got)
			}
		}
	}
}

func TestHelpBlocksPasteWithoutBlockingAsyncUpdates(t *testing.T) {
	for _, context := range []string{"compose", "search", "find"} {
		t.Run(context, func(t *testing.T) {
			m := modelWithBoxes()
			var value func() string
			switch context {
			case "compose":
				m.mailView.startCompose()
				value = func() string { return composeModal(m.mailView).inputs[fieldTo].Value() }
			case "search":
				m.mailView.startSearch()
				value = func() string { return searchModal(m.mailView).input.Value() }
			case "find":
				m = bodyFindModel(t)
				updated, _ := m.Update(keyPress("/"))
				m = updated.(model)
				value = func() string { return m.mailView.bodyFind.input.Value() }
			}
			m.updateHelpBindings()
			updated, _ := m.Update(keyPress("q"))
			m = updated.(model)
			before := value()
			updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyF1}))
			m = updated.(model)
			updated, cmd := m.Update(tea.PasteMsg{Content: "pasted behind help"})
			m = updated.(model)
			if cmd != nil || value() != before {
				t.Fatal("paste changed the hidden input")
			}
			updated, _ = m.Update(errMsg{err: errors.New("connection interrupted")})
			m = updated.(model)
			if m.err == nil || !m.helpOpen {
				t.Fatal("help blocked the asynchronous error update")
			}
			updated, _ = m.Update(keyPress("esc"))
			m = updated.(model)
			if m.helpOpen || value() != before {
				t.Fatal("closing help changed the form")
			}
		})
	}
}

func TestSmallHelpKeepsEveryBindingReachable(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.screenerCount = 6
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updated.(model)
	updated, _ = m.Update(keyPress("?"))
	m = updated.(model)
	if strings.Contains(m.helpDialog(), "╭") {
		t.Fatal("small help should omit its frame")
	}
	seen := ""
	for range 250 {
		dialog := ansi.Strip(m.helpDialog())
		screen := ansi.Strip(m.View().Content)
		if strings.Contains(screen, "Screen 6") {
			t.Fatal("mail content bled through the unframed help")
		}
		for _, line := range strings.Split(dialog, "\n") {
			if !strings.Contains(screen, strings.TrimRight(line, " ")) {
				t.Fatalf("screen clipped %q: %q", line, screen)
			}
		}
		if strings.Contains(dialog, "enter") || strings.Contains(dialog, "pgup/pgdown") {
			seen += dialog
		}
		previous := m.helpScroll
		updated, _ = m.Update(keyPress("down"))
		m = updated.(model)
		if previous == m.helpScroll {
			break
		}
	}
	for _, want := range []string{"enter", "pgup/pgdown"} {
		if !strings.Contains(seen, want) {
			t.Fatalf("binding %q never appeared in small help", want)
		}
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "? help") {
		t.Fatal("help hint disappeared while help was open")
	}
}

func TestFullHelpIncludesContextualNavigation(t *testing.T) {
	for _, context := range []string{"box", "search", "bundle", "seen", "contacts", "journal", "screener", "compose", "find", "thread"} {
		t.Run(context, func(t *testing.T) {
			m := modelWithBoxes()
			switch context {
			case "search":
				m.mailView.searchActive = true
			case "bundle":
				m.mailView.bundleActive = true
			case "seen":
				m.mailView.seenActive = true
			case "contacts":
				m.activeView = m.contactsView
				m.section = sectionContacts
			case "journal":
				m.activeView = m.journalView
				m.section = sectionJournal
			case "screener":
				m.activeView = m.screenerView
			case "compose":
				m.mailView.startCompose()
			case "find":
				m = bodyFindModel(t)
				updated, _ := m.Update(keyPress("/"))
				m = updated.(model)
			case "thread":
				m = bodyFindModel(t)
			}
			m.updateHelpBindings()
			full := ansi.Strip(strings.Join(m.help.dialogLines(84), "\n"))
			paging := context != "compose" && context != "find"
			if strings.Contains(full, "pgup/pgdown") != paging {
				t.Fatalf("paging availability is wrong in %s: %s", context, full)
			}
			global := context != "compose" && context != "find" && context != "screener"
			if strings.Contains(full, "Calendar") != global {
				t.Fatalf("section navigation availability is wrong in %s", context)
			}
		})
	}
}
