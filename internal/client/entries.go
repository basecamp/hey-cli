package client

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/models"
)

func (c *Client) GetEntry(id string) (models.Entry, error) {
	path := fmt.Sprintf("/entries/%s", id)
	data, err := c.GetHTML(path)
	if err != nil {
		return models.Entry{}, err
	}

	entries := parseTopicEntriesHTML(string(data))
	if len(entries) == 0 {
		return models.Entry{}, fmt.Errorf("entry %s not found", id)
	}
	return entries[0], nil
}

func (c *Client) ListDrafts() ([]models.Draft, error) {
	var drafts []models.Draft
	if err := c.GetJSON("/entries/drafts.json", &drafts); err != nil {
		return nil, err
	}
	return drafts, nil
}

var (
	entryBlockRe = regexp.MustCompile(`(?s)data-entry-id="(\d+)"`)
	senderRe     = regexp.MustCompile(`id="sender_entry_(\d+)"[^>]*>\s*([^<]+?)\s*<`)
	timeRe       = regexp.MustCompile(`<time[^>]*datetime="([^"]+)"`)
	srcdocRe     = regexp.MustCompile(`(?s)srcdoc="([^"]*trix-content[^"]*)"`)
)

func (c *Client) GetTopicEntries(id int) ([]models.Entry, error) {
	path := fmt.Sprintf("/topics/%d/entries", id)
	data, err := c.GetHTML(path)
	if err != nil {
		return nil, err
	}

	return parseTopicEntriesHTML(string(data)), nil
}

func parseTopicEntriesHTML(html string) []models.Entry {
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
		bodies = append(bodies, body{html: raw, text: htmlutil.ToText(raw)})
	}

	entries := make([]models.Entry, 0, len(entryIDs))
	for i, eid := range entryIDs {
		id, _ := strconv.Atoi(eid)
		e := models.Entry{
			ID:        id,
			CreatedAt: entryTimes[eid],
		}
		if name, ok := senders[eid]; ok {
			e.Creator = models.Contact{Name: name}
		}
		if i < len(bodies) {
			e.Body = bodies[i].text
			e.BodyHTML = bodies[i].html
		}
		entries = append(entries, e)
	}

	return entries
}

func (c *Client) CreateMessage(topicID *int, body any) ([]byte, error) {
	path := "/topics/messages"
	if topicID != nil {
		path = fmt.Sprintf("/topics/%d/messages", *topicID)
	}
	return c.PostJSON(path, body)
}

func (c *Client) ReplyToEntry(id string, body any) ([]byte, error) {
	path := fmt.Sprintf("/entries/%s/replies", id)
	return c.PostJSON(path, body)
}
