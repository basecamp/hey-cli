package client

import (
	"fmt"

	"github.com/basecamp/hey-cli/internal/models"
)

func (c *Client) ListCalendars() ([]models.Calendar, error) {
	var resp models.CalendarsResponse
	if err := c.GetJSON("/calendars.json", &resp); err != nil {
		return nil, err
	}
	calendars := make([]models.Calendar, len(resp.Calendars))
	for i, w := range resp.Calendars {
		calendars[i] = w.Calendar
	}
	return calendars, nil
}

func (c *Client) GetCalendarRecordings(id int, startsOn, endsOn string) (models.RecordingsResponse, error) {
	path := fmt.Sprintf("/calendars/%d/recordings.json?starts_on=%s&ends_on=%s", id, startsOn, endsOn)

	var resp models.RecordingsResponse
	if err := c.GetJSON(path, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}
