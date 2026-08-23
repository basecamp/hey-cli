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

// visibleCount is how many contacts the window holds. Each one takes one line.
func (l *contactList) visibleCount() int {
	return max(l.height, 1)
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
	nameBase := lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	addressBase := lipgloss.NewStyle().Foreground(colorBright)

	var b strings.Builder
	for i := l.scrollOff; i < end; i++ {
		contact := l.contacts[i]
		cursor := i == l.cursor
		prefix := "  "
		if cursor {
			prefix = cursorMarker.Render("│") + selectedGap.Render(" ")
		}

		// One line per contact, written as an email From header: the name in
		// bold with the address regular in angle brackets after it, as in the
		// Screener lists. A contact with no name shows the bare address in the
		// name's position, keeping its bold.
		name := terminal.SanitizeLine(contact.Name)
		label := name
		if email := terminal.SanitizeLine(contact.EmailAddress); email != "" {
			if label == "" {
				label = email
			} else {
				label += " <" + email + ">"
			}
		}
		label = truncateStr(label, max(l.width-4, 10))

		if cursor {
			fmt.Fprintf(&b, "%s%s\n", prefix, selected.Render(label))
			continue
		}
		// A label truncated into the name renders as one bold piece.
		head, rest := label, ""
		if after, ok := strings.CutPrefix(label, name); ok && name != "" {
			head, rest = name, after
		}
		fmt.Fprintf(&b, "%s%s%s\n", prefix, nameBase.Render(head), addressBase.Render(rest))
	}
	return b.String()
}
