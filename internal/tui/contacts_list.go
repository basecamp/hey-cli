package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

type contactList struct {
	contacts  []models.Contact
	cursor    int
	scrollOff int
	width     int
	height    int
}

func (l *contactList) setContacts(contacts []models.Contact) {
	l.contacts = contacts
	l.cursor = 0
	l.scrollOff = 0
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

func (l *contactList) ensureVisible() {
	visible := max(l.height/2, 1)
	if l.cursor < l.scrollOff {
		l.scrollOff = l.cursor
	}
	if l.cursor >= l.scrollOff+visible {
		l.scrollOff = l.cursor - visible + 1
	}
}

func (l *contactList) selected() *models.Contact {
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
		return lipgloss.NewStyle().Foreground(colorMuted).Render("  (no contacts)")
	}
	visible := max(l.height/2, 1)
	end := min(l.scrollOff+visible, len(l.contacts))
	selected := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	normal := lipgloss.NewStyle().Foreground(colorBright)
	muted := lipgloss.NewStyle().Foreground(colorMuted)

	var b strings.Builder
	for i := l.scrollOff; i < end; i++ {
		contact := l.contacts[i]
		cursor := i == l.cursor
		prefix := "  "
		if cursor {
			prefix = selected.Render("│") + " "
		}
		name := truncateStr(contact.Name, max(l.width-4, 10))
		email := truncateStr(contact.EmailAddress, max(l.width-18, 10))
		line2 := fmt.Sprintf("#%d  %s", contact.ID, email)
		if cursor {
			fmt.Fprintf(&b, "%s%s\n", prefix, selected.Render(name))
			fmt.Fprintf(&b, "%s  %s\n", selected.Render("│"), selected.Render(line2))
		} else {
			fmt.Fprintf(&b, "%s%s\n", prefix, normal.Render(name))
			fmt.Fprintf(&b, "    %s\n", muted.Render(line2))
		}
	}
	return b.String()
}
