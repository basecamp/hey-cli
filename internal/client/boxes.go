package client

func (c *Client) MarkSeen(postingIDs []int) ([]byte, error) {
	body := map[string]any{"posting_ids": postingIDs}
	return c.PostJSON("/postings/seen", body)
}

func (c *Client) MarkUnseen(postingIDs []int) ([]byte, error) {
	body := map[string]any{"posting_ids": postingIDs}
	return c.PostJSON("/postings/unseen", body)
}
