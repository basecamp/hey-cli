package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

type contactList struct {
	contacts  []Contact
	cursor    int
	scrollOff int
	width     int
	height    int
}

func (l *contactList) setContacts(contacts []Contact) {
	l.contacts = contacts
	l.cursor = 0
	l.scrollOff = 0
}

// growContacts adds the page below the list, leaving the cursor and the scroll where the
// reader left them.
func (l *contactList) growContacts(contacts []Contact) {
	l.contacts = append(l.contacts, contacts...)
}

func (l *contactList) setSize(width, height int) {
	l.width = width
	l.height = height
}

func (l *contactList) moveUp() {
	if l.cursor > 0 {
		l.cursor--
		l.ensureVisible()
	}
}

func (l *contactList) moveDown() {
	if l.cursor < len(l.contacts)-1 {
		l.cursor++
		l.ensureVisible()
	}
}

// visibleCount is how many contacts the window holds. Each one takes two lines.
func (l *contactList) visibleCount() int {
	return max(l.height/2, 1)
}

// hasRowsBelow reports whether the list carries on past the bottom of the window. A list
// that does not is a list the reader can see the end of, which is a reason to read the page
// below it without waiting to be asked.
func (l *contactList) hasRowsBelow() bool {
	return l.scrollOff+l.visibleCount() < len(l.contacts)
}

func (l *contactList) ensureVisible() {
	visible := l.visibleCount()
	if l.cursor < l.scrollOff {
		l.scrollOff = l.cursor
	}
	if l.cursor >= l.scrollOff+visible {
		l.scrollOff = l.cursor - visible + 1
	}
}

func (l *contactList) selected() *Contact {
	if l.cursor < 0 || l.cursor >= len(l.contacts) {
		return nil
	}
	return &l.contacts[l.cursor]
}

func (l *contactList) remove(id int64) {
	for i := range l.contacts {
		if l.contacts[i].ID != id {
			continue
		}
		l.contacts = append(l.contacts[:i], l.contacts[i+1:]...)
		if l.cursor > i {
			l.cursor--
		}
		if l.cursor >= len(l.contacts) && l.cursor > 0 {
			l.cursor--
		}
		l.ensureVisible()
		return
	}
}

func (l *contactList) view() string {
	if len(l.contacts) == 0 {
		return styleMuted.Render("  (no contacts)")
	}
	end := min(l.scrollOff+l.visibleCount(), len(l.contacts))
	cursorMarker, selected := cursorStyles()
	selectedGap := selectionStyle(lipgloss.NewStyle())
	normal := lipgloss.NewStyle().Foreground(colorBright)
	muted := styleMuted

	var b strings.Builder
	for i := l.scrollOff; i < end; i++ {
		contact := l.contacts[i]
		cursor := i == l.cursor
		prefix := "  "
		if cursor {
			prefix = cursorMarker.Render("│") + selectedGap.Render(" ")
		}
		name := truncateStr(terminal.SanitizeLine(contact.Name), max(l.width-4, 10))
		email := truncateStr(terminal.SanitizeLine(contact.EmailAddress), max(l.width-18, 10))
		line2 := fmt.Sprintf("#%d  %s", contact.ID, email)
		if cursor {
			fmt.Fprintf(&b, "%s%s\n", prefix, selected.Render(name))
			fmt.Fprintf(&b, "%s%s%s\n", cursorMarker.Render("│"), selectedGap.Render("  "), selected.Render(line2))
		} else {
			fmt.Fprintf(&b, "%s%s\n", prefix, normal.Render(name))
			fmt.Fprintf(&b, "    %s\n", muted.Render(line2))
		}
	}
	return b.String()
}
