package client

import (
	"fmt"

	"github.com/basecamp/hey-cli/internal/models"
)

func (c *Client) ListTodos() ([]models.Todo, error) {
	var todos []models.Todo
	if err := c.GetJSON("/calendar/todos.json", &todos); err != nil {
		return nil, err
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
