package client

import (
	"fmt"

	"hey-cli/internal/models"
)

func (c *Client) ListTimeTracks() ([]models.TimeTrack, error) {
	var tracks []models.TimeTrack
	if err := c.GetJSON("/calendar/time_tracks.json", &tracks); err != nil {
		return nil, err
	}
	return tracks, nil
}

func (c *Client) GetOngoingTimeTrack() (models.TimeTrack, error) {
	var track models.TimeTrack
	if err := c.GetJSON("/calendar/ongoing_time_track.json", &track); err != nil {
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
