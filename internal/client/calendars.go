package client

import (
	"fmt"

	"hey-cli/internal/models"
)

func (c *Client) ListCalendars() ([]models.Calendar, error) {
	var resp models.CalendarsResponse
	if err := c.GetJSON("/calendars.json", &resp); err != nil {
		return nil, err
	}
	return resp.Calendars, nil
}

func (c *Client) GetCalendarRecordings(id int, from, to string) (models.RecordingsResponse, error) {
	path := fmt.Sprintf("/calendars/%d/recordings.json", id)
	sep := "?"
	if from != "" {
		path += sep + "from=" + from
		sep = "&"
	}
	if to != "" {
		path += sep + "to=" + to
	}

	var resp models.RecordingsResponse
	if err := c.GetJSON(path, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}
