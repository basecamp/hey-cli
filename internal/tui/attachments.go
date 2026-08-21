package tui

import (
	"fmt"
	"strings"

	"github.com/basecamp/hey-cli/internal/terminal"
)

type messageAttachment struct {
	ID          string
	MessageID   int64
	Filename    string
	ContentType string
	ByteSize    *int64
	URL         string
}

func attachmentsForMessage(attachments []messageAttachment, messageID int64) []messageAttachment {
	var matches []messageAttachment
	for _, attachment := range attachments {
		if attachment.MessageID == messageID {
			matches = append(matches, attachment)
		}
	}
	return matches
}

func selectedAttachmentForMessage(attachments []messageAttachment, selected int, messageID int64) int {
	if selected < 0 || selected >= len(attachments) || attachments[selected].MessageID != messageID {
		return -1
	}
	localIndex := 0
	for index := range selected {
		if attachments[index].MessageID == messageID {
			localIndex++
		}
	}
	return localIndex
}

func renderAttachmentPanel(attachments []messageAttachment, selected int) string {
	if len(attachments) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("╭─ Attachments\n")
	for index, attachment := range attachments {
		marker := " "
		if index == selected {
			marker = "›"
		}
		contentType := terminal.SanitizeLine(attachment.ContentType)
		if contentType == "" {
			contentType = "unknown type"
		}
		fmt.Fprintf(&b, "│ %s %d. %s\n", marker, index+1, terminal.SanitizeLine(attachment.Filename))
		fmt.Fprintf(&b, "│     %s · %s\n", contentType, formatAttachmentSize(attachment.ByteSize))
	}
	b.WriteString("╰─")
	return b.String()
}

func formatAttachmentSize(size *int64) string {
	if size == nil || *size < 0 {
		return "unknown size"
	}
	switch {
	case *size < 1024:
		return fmt.Sprintf("%d B", *size)
	case *size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(*size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(*size)/(1024*1024))
	}
}
