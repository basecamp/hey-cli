package client

import (
	"fmt"

	"hey-cli/internal/models"
)

func (c *Client) ListTodos() ([]models.Todo, error) {
	recordingsByType, err := c.listPersonalCalendarRecordings()
	if err != nil {
		return nil, err
	}

	recordings := recordingsByType["Calendar::Todo"]
	todos := make([]models.Todo, 0, len(recordings))
	for _, recording := range recordings {
		todos = append(todos, models.Todo{
			ID:          recording.ID,
			Title:       recording.Title,
			StartsAt:    recording.StartsAt,
			CompletedAt: recording.CompletedAt,
			CreatedAt:   recording.CreatedAt,
			UpdatedAt:   recording.UpdatedAt,
		})
	}

	return todos, nil
}

func (c *Client) CreateTodo(body any) ([]byte, error) {
	return c.PostJSON("/calendar/todos.json", body)
}

func (c *Client) CompleteTodo(id string) ([]byte, error) {
	path := fmt.Sprintf("/calendar/todos/%s/completions.json", id)
	return c.PostJSON(path, nil)
}

func (c *Client) UncompleteTodo(id string) ([]byte, error) {
	path := fmt.Sprintf("/calendar/todos/%s/completions.json", id)
	return c.Delete(path)
}

func (c *Client) DeleteTodo(id string) ([]byte, error) {
	path := fmt.Sprintf("/calendar/todos/%s.json", id)
	return c.Delete(path)
}
