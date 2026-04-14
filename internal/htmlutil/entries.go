package htmlutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/basecamp/hey-cli/internal/models"
)

var (
	entryBlockRe     = regexp.MustCompile(`(?s)data-entry-id="(\d+)"`)
	senderRe         = regexp.MustCompile(`id="sender_entry_(\d+)"[^>]*>\s*([^<]+?)\s*<`)
	senderEmailRe    = regexp.MustCompile(`(?s)sender_entry_(\d+).*?entry__sender-email[^>]*><span[^>]*>[^<]*</span>([^<]+)<`)
	timeRe           = regexp.MustCompile(`<time[^>]*datetime="([^"]+)"`)
	srcdocRe         = regexp.MustCompile(`(?s)srcdoc="([^"]*trix-content[^"]*)"`)
	fullRecipientsRe = regexp.MustCompile(`(?s)entry__full-recipients[^>]*>(.*?)</span>`)
	titleEmailRe     = regexp.MustCompile(`title="([^"]+)"`)
	ccSplitRe        = regexp.MustCompile(`(CC:|BCC:)`)
)

// TopicAddressed holds the To, CC, and BCC recipients for a topic.
type TopicAddressed struct {
	To  []string
	CC  []string
	BCC []string
}

// ParseTopicAddressed extracts To/CC/BCC recipients from a topic's HTML page.
func ParseTopicAddressed(html string) *TopicAddressed {
	m := fullRecipientsRe.FindStringSubmatch(html)
	if m == nil {
		return &TopicAddressed{}
	}
	content := m[1]

	result := &TopicAddressed{}
	parts := ccSplitRe.Split(content, -1)
	labels := ccSplitRe.FindAllString(content, -1)

	// First part is always "to"
	result.To = extractEmails(parts[0])
	for i, label := range labels {
		emails := extractEmails(parts[i+1])
		switch label {
		case "CC:":
			result.CC = emails
		case "BCC:":
			result.BCC = emails
		}
	}
	return result
}

func extractEmails(html string) []string {
	matches := titleEmailRe.FindAllStringSubmatch(html, -1)
	var addrs []string
	for _, m := range matches {
		addr := strings.TrimSpace(m[1])
		if addr != "" && strings.Contains(addr, "@") {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

// ParseTopicEntriesHTML extracts structured entry data from the HTML page
// served by /topics/{id}/entries. The JSON API does not return full entry
// bodies, so this HTML-based extraction is required.
func ParseTopicEntriesHTML(html string) []models.Entry {
	// Find unique entry IDs in order
	idMatches := entryBlockRe.FindAllStringSubmatch(html, -1)
	seen := map[string]bool{}
	var entryIDs []string
	for _, m := range idMatches {
		if !seen[m[1]] {
			seen[m[1]] = true
			entryIDs = append(entryIDs, m[1])
		}
	}

	// Build lookup maps
	senders := map[string]string{}
	for _, m := range senderRe.FindAllStringSubmatch(html, -1) {
		if _, exists := senders[m[1]]; !exists {
			senders[m[1]] = m[2]
		}
	}
	senderEmails := map[string]string{}
	for _, m := range senderEmailRe.FindAllStringSubmatch(html, -1) {
		if _, exists := senderEmails[m[1]]; !exists {
			senderEmails[m[1]] = strings.TrimSpace(m[2])
		}
	}

	// Associate times with entries by finding the first <time> after each entry anchor
	entryTimes := map[string]string{}
	for _, eid := range entryIDs {
		anchor := fmt.Sprintf(`id="entry_%s"`, eid)
		idx := strings.Index(html, anchor)
		if idx < 0 {
			continue
		}
		if m := timeRe.FindStringSubmatch(html[idx:]); m != nil {
			entryTimes[eid] = m[1]
		}
	}

	// Associate recipients with entries by slicing between entry anchors.
	entryRecipients := map[string][]models.Contact{}
	for i, eid := range entryIDs {
		anchor := fmt.Sprintf(`id="entry_%s"`, eid)
		start := strings.Index(html, anchor)
		if start < 0 {
			continue
		}
		end := len(html)
		if i+1 < len(entryIDs) {
			nextAnchor := fmt.Sprintf(`id="entry_%s"`, entryIDs[i+1])
			if n := strings.Index(html[start:], nextAnchor); n > 0 {
				end = start + n
			}
		}
		m := fullRecipientsRe.FindStringSubmatch(html[start:end])
		if m == nil {
			continue
		}
		seen := map[string]bool{}
		for _, addr := range extractEmails(m[1]) {
			if seen[addr] {
				continue
			}
			seen[addr] = true
			entryRecipients[eid] = append(entryRecipients[eid], models.Contact{EmailAddress: addr})
		}
	}

	// Extract bodies from srcdoc iframes - they appear in entry order
	type body struct{ html, text string }
	bodyMatches := srcdocRe.FindAllStringSubmatch(html, -1)
	bodies := make([]body, 0, len(bodyMatches))
	for _, m := range bodyMatches {
		raw := m[1]
		raw = strings.ReplaceAll(raw, "&lt;", "<")
		raw = strings.ReplaceAll(raw, "&gt;", ">")
		raw = strings.ReplaceAll(raw, "&quot;", "\"")
		raw = strings.ReplaceAll(raw, "&amp;", "&")
		raw = strings.ReplaceAll(raw, "&#39;", "'")
		bodies = append(bodies, body{html: raw, text: ToText(raw)})
	}

	entries := make([]models.Entry, 0, len(entryIDs))
	for i, eid := range entryIDs {
		id, _ := strconv.ParseInt(eid, 10, 64)
		e := models.Entry{
			ID:        id,
			CreatedAt: entryTimes[eid],
		}
		if name, ok := senders[eid]; ok {
			e.Creator = models.Contact{Name: name, EmailAddress: senderEmails[eid]}
		}
		if recips, ok := entryRecipients[eid]; ok {
			e.Recipients = recips
		}
		if i < len(bodies) {
			e.Body = bodies[i].text
			e.BodyHTML = bodies[i].html
		}
		entries = append(entries, e)
	}

	return entries
}
