package client

import "fmt"

func (c *Client) CompleteHabit(date string, id string) ([]byte, error) {
	path := fmt.Sprintf("/calendar/days/%s/habits/%s/completions.json", date, id)
	return c.PostJSON(path, nil)
}

func (c *Client) UncompleteHabit(date string, id string) ([]byte, error) {
	path := fmt.Sprintf("/calendar/days/%s/habits/%s/completions.json", date, id)
	return c.Delete(path)
}
