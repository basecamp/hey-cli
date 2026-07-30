package cmd

import (
	"html"
	"strings"
)

// formatMessageContent converts plain text into the HTML/Action Text content
// expected by HEY. Without block markup, browsers collapse newlines and render
// an entire plain-text message as a single paragraph.
func formatMessageContent(message string, rawHTML bool) string {
	if rawHTML {
		return message
	}

	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	paragraphs := splitParagraphs(message)
	formatted := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		escaped := html.EscapeString(paragraph)
		escaped = strings.ReplaceAll(escaped, "\n", "<br>")
		formatted = append(formatted, "<p>"+escaped+"</p>")
	}

	return strings.Join(formatted, "\n")
}

func splitParagraphs(message string) []string {
	lines := strings.Split(message, "\n")
	paragraphs := make([]string, 0, len(lines))
	current := make([]string, 0, 1)

	flush := func() {
		if len(current) == 0 {
			return
		}
		paragraphs = append(paragraphs, strings.Join(current, "\n"))
		current = current[:0]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()

	return paragraphs
}
