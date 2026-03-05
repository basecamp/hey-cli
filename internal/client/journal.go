package client

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/basecamp/hey-cli/internal/models"
)

func (c *Client) ListJournalEntries() ([]models.JournalEntry, error) {
	var entries []models.JournalEntry
	if err := c.GetJSON("/calendar/journal_entries.json", &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// GetJournalEntry fetches a journal entry by date.
// The JSON API (/calendar/days/{date}/journal_entry.json) returns 204 for
// journal entries, so we scrape the edit page to get the full HTML body.
func (c *Client) GetJournalEntry(date string) (models.JournalEntry, error) {
	var entry models.JournalEntry
	entry.Date = date

	path := fmt.Sprintf("/calendar/days/%s/journal_entry/edit", date)
	data, err := c.GetHTML(path)
	if err != nil {
		return entry, err
	}

	body, err := extractTrixContent(data)
	if err != nil {
		return entry, err
	}
	entry.Body = body
	return entry, nil
}

func extractTrixContent(data []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return findTrixInput(doc), nil
}

func findTrixInput(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "input" {
		isTarget := false
		value := ""
		for _, a := range n.Attr {
			if a.Key == "id" && strings.Contains(a.Val, "journal") && strings.HasSuffix(a.Val, "trix_input") {
				isTarget = true
			}
			if a.Key == "value" {
				value = a.Val
			}
		}
		if isTarget && value != "" {
			return value
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if v := findTrixInput(c); v != "" {
			return v
		}
	}
	return ""
}

func (c *Client) UpdateJournalEntry(date string, body any) ([]byte, error) {
	path := fmt.Sprintf("/calendar/days/%s/journal_entry.json", date)
	return c.PatchJSON(path, body)
}
