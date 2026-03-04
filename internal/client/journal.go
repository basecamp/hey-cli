package client

import (
	"fmt"

	"hey-cli/internal/models"
)

func (c *Client) ListJournalEntries() ([]models.JournalEntry, error) {
	var entries []models.JournalEntry
	if err := c.GetJSON("/calendar/journal_entries.json", &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Client) GetJournalEntry(date string) (models.JournalEntry, error) {
	var entry models.JournalEntry
	path := fmt.Sprintf("/calendar/days/%s/journal_entry.json", date)
	if err := c.GetJSON(path, &entry); err != nil {
		return entry, err
	}
	return entry, nil
}

func (c *Client) UpdateJournalEntry(date string, body any) ([]byte, error) {
	path := fmt.Sprintf("/calendar/days/%s/journal_entry.json", date)
	return c.PatchJSON(path, body)
}
