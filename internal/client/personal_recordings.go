package client

import (
	"fmt"
	"strings"
	"time"

	"hey-cli/internal/models"
)

const (
	personalRecordingsLookbackYears  = 4
	personalRecordingsLookaheadYears = 1
)

func (c *Client) listPersonalCalendarRecordings() (models.RecordingsResponse, error) {
	personalCalendarID, err := c.personalCalendarID()
	if err != nil {
		return nil, err
	}

	startsOn := time.Now().AddDate(-personalRecordingsLookbackYears, 0, 0).Format("2006-01-02")
	endsOn := time.Now().AddDate(personalRecordingsLookaheadYears, 0, 0).Format("2006-01-02")

	return c.GetCalendarRecordings(personalCalendarID, startsOn, endsOn)
}

func (c *Client) personalCalendarID() (int, error) {
	calendars, err := c.ListCalendars()
	if err != nil {
		return 0, fmt.Errorf("could not list calendars: %w", err)
	}

	for _, calendar := range calendars {
		if calendar.Personal {
			return calendar.ID, nil
		}
	}

	for _, calendar := range calendars {
		if strings.EqualFold(calendar.Name, "Personal") {
			return calendar.ID, nil
		}
	}

	return 0, fmt.Errorf("personal calendar not found")
}
