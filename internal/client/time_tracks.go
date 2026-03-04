package client

import (
	"errors"
	"fmt"

	"hey-cli/internal/models"
)

func (c *Client) ListTimeTracks() ([]models.TimeTrack, error) {
	recordingsByType, err := c.listPersonalCalendarRecordings()
	if err != nil {
		return nil, err
	}

	recordings := recordingsByType["Calendar::TimeTrack"]
	tracks := make([]models.TimeTrack, 0, len(recordings))
	for _, recording := range recordings {
		tracks = append(tracks, models.TimeTrack{
			ID:        recording.ID,
			Title:     recording.Title,
			StartsAt:  recording.StartsAt,
			EndsAt:    recording.EndsAt,
			CreatedAt: recording.CreatedAt,
			UpdatedAt: recording.UpdatedAt,
		})
	}

	return tracks, nil
}

func (c *Client) GetOngoingTimeTrack() (models.TimeTrack, error) {
	var track models.TimeTrack
	if err := c.GetJSON("/calendar/ongoing_time_track", &track); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return models.TimeTrack{}, nil
		}
		return track, err
	}
	return track, nil
}

func (c *Client) StartTimeTrack(body any) ([]byte, error) {
	return c.PostJSON("/calendar/ongoing_time_track.json", body)
}

func (c *Client) StopTimeTrack(id int) ([]byte, error) {
	path := fmt.Sprintf("/calendar/time_tracks/%d.json", id)
	return c.PutJSON(path, map[string]any{"stopped": true})
}
