package tui

import (
	"encoding/json"
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"hey-cli/internal/client"
	"hey-cli/internal/models"
)

type viewState int

const (
	viewBoxes viewState = iota
	viewBox
	viewTopic
)

// Async messages
type boxesLoadedMsg []models.Box

type boxLoadedMsg struct {
	box      models.Box
	postings []models.Posting
}

type topicLoadedMsg struct {
	title   string
	entries []models.Entry
}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

type model struct {
	state  viewState
	width  int
	height int
	client *client.Client
	styles styles

	boxes boxesModel
	box   boxModel
	topic topicModel

	loading bool
	err     error
}

func newModel(c *client.Client) model {
	s := newStyles()
	return model{
		state:  viewBoxes,
		client: c,
		styles: s,
		boxes:  newBoxesModel(),
		box:    newBoxModel(),
		topic:  newTopicModel(s),
	}
}

func (m model) Init() tea.Cmd {
	return m.fetchBoxes()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.boxes.setSize(msg.Width, msg.Height)
		m.box.setSize(msg.Width, msg.Height)
		m.topic.setSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case boxesLoadedMsg:
		m.loading = false
		cmd := m.boxes.setItems([]models.Box(msg))
		return m, cmd

	case boxLoadedMsg:
		m.loading = false
		cmd := m.box.setItems(msg.box, msg.postings)
		m.state = viewBox
		return m, cmd

	case topicLoadedMsg:
		m.loading = false
		m.topic.setEntries(msg.title, msg.entries)
		m.state = viewTopic
		return m, nil

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil
	}

	var cmd tea.Cmd
	switch m.state {
	case viewBoxes:
		cmd = m.updateBoxes(msg)
	case viewBox:
		cmd = m.updateBox(msg)
	case viewTopic:
		cmd = m.updateTopic(msg)
	}
	return m, cmd
}

func (m *model) updateBoxes(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "enter":
			if m.boxes.list.FilterState() != list.Filtering {
				box := m.boxes.selectedBox()
				if box != nil {
					m.loading = true
					return m.fetchBox(box.ID)
				}
			}
		}
	}

	var cmd tea.Cmd
	m.boxes, cmd = m.boxes.update(msg)
	return cmd
}

func (m *model) updateBox(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "esc", "backspace":
			if m.box.list.FilterState() == list.Unfiltered {
				m.state = viewBoxes
				return nil
			}
		case "enter":
			if m.box.list.FilterState() != list.Filtering {
				posting := m.box.selectedPosting()
				if posting != nil && posting.Topic != nil {
					m.loading = true
					return m.fetchTopic(posting.Topic.ID, posting.Summary)
				}
			}
		}
	}

	var cmd tea.Cmd
	m.box, cmd = m.box.update(msg)
	return cmd
}

func (m *model) updateTopic(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "esc", "backspace":
			m.state = viewBox
			return nil
		case "q":
			m.state = viewBox
			return nil
		}
	}

	var cmd tea.Cmd
	m.topic, cmd = m.topic.update(msg)
	return cmd
}

func (m model) View() tea.View {
	var content string

	if m.err != nil {
		content = fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	} else if m.loading {
		content = "Loading..."
	} else {
		switch m.state {
		case viewBoxes:
			content = m.boxes.view()
		case viewBox:
			content = m.box.view()
		case viewTopic:
			content = m.topic.view()
		}
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// Async data fetching commands

func (m model) fetchBoxes() tea.Cmd {
	return func() tea.Msg {
		var boxes []models.Box
		if err := m.client.GetJSON("/boxes.json", &boxes); err != nil {
			return errMsg{err}
		}
		return boxesLoadedMsg(boxes)
	}
}

func (m model) fetchBox(boxID int) tea.Cmd {
	return func() tea.Msg {
		var resp models.BoxShowResponse
		path := fmt.Sprintf("/boxes/%d.json", boxID)
		if err := m.client.GetJSON(path, &resp); err != nil {
			return errMsg{err}
		}

		var postings []models.Posting
		for _, raw := range resp.Postings {
			var p models.Posting
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			postings = append(postings, p)
		}

		return boxLoadedMsg{box: resp.Box, postings: postings}
	}
}

func (m model) fetchTopic(topicID int, title string) tea.Cmd {
	return func() tea.Msg {
		var entries []models.Entry
		path := fmt.Sprintf("/topics/%d/entries.json", topicID)
		if err := m.client.GetJSON(path, &entries); err != nil {
			return errMsg{err}
		}
		return topicLoadedMsg{title: title, entries: entries}
	}
}

// Run starts the TUI program.
func Run(c *client.Client) error {
	p := tea.NewProgram(newModel(c))
	_, err := p.Run()
	return err
}
