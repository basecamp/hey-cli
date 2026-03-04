package client

import (
	"fmt"

	"hey-cli/internal/models"
)

func (c *Client) GetEntry(id string) (models.Entry, error) {
	var entry models.Entry
	path := fmt.Sprintf("/entries/%s.json", id)
	if err := c.GetJSON(path, &entry); err != nil {
		return entry, err
	}
	return entry, nil
}

func (c *Client) ListDrafts() ([]models.Draft, error) {
	var drafts []models.Draft
	if err := c.GetJSON("/entries/drafts.json", &drafts); err != nil {
		return nil, err
	}
	return drafts, nil
}

func (c *Client) GetTopicEntries(id int) ([]models.Entry, error) {
	var entries []models.Entry
	path := fmt.Sprintf("/topics/%d/entries.json", id)
	if err := c.GetJSON(path, &entries); err != nil {
		return nil, err
	}
	return entries, nil
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
