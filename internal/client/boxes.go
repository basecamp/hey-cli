package client

import (
	"fmt"

	"github.com/basecamp/hey-cli/internal/models"
)

func (c *Client) ListBoxes() ([]models.Box, error) {
	var boxes []models.Box
	if err := c.GetJSON("/boxes.json", &boxes); err != nil {
		return nil, err
	}
	return boxes, nil
}

func (c *Client) GetBox(id int) (models.BoxShowResponse, error) {
	var resp models.BoxShowResponse
	path := fmt.Sprintf("/boxes/%d.json", id)
	if err := c.GetJSON(path, &resp); err != nil {
		return resp, err
	}
	return resp, nil
}
