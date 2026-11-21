package tui

import (
	"fmt"
	"strings"

	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// envelopeLines is the delivery address and the visible envelope, one truncated
// line each, under a message's From line. A line HEY left empty is left out.
// The caller decides whether the entry was emailed: a note or a share notice
// has none of these lines. Each line is cut to width before it is styled. A
// wrap would make a Screener preview header taller than the space layoutPreview
// counted for it.
func envelopeLines(recipients mail.Recipients, receivedVia []string, width int) []string {
	width = max(width, 1)
	var lines []string
	add := func(label, value string) {
		if value == "" {
			return
		}
		lines = append(lines, truncateStr(fmt.Sprintf("%-3s  %s", label, value), width))
	}
	add("Via", strings.Join(receivedVia, ", "))
	add("To", formatContacts(recipients.To))
	add("Cc", formatContacts(recipients.Cc))
	add("Bcc", formatContacts(recipients.Bcc))
	return lines
}

func formatContacts(contacts []mail.Contact) string {
	parts := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		name := terminal.SanitizeLine(contact.Name)
		email := terminal.SanitizeLine(contact.EmailAddress)
		switch {
		case name != "" && email != "":
			parts = append(parts, name+" <"+email+">")
		case email != "":
			parts = append(parts, email)
		case name != "":
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}
