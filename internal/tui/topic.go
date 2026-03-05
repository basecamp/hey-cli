package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

type topicModel struct {
	viewport viewport.Model
	title    string
	content  string
	styles   styles
}

func newTopicModel(s styles) topicModel {
	vp := viewport.New(viewport.WithWidth(0), viewport.WithHeight(0))
	return topicModel{viewport: vp, styles: s}
}

func (m *topicModel) setEntries(title string, entries []models.Entry) {
	m.title = title
	m.content = m.renderEntries(entries)
	m.viewport.SetContent(m.content)
	m.viewport.GotoTop()
}

func (m *topicModel) appendContent(extra string) {
	m.content += extra
	m.viewport.SetContent(m.content)
}

func (m *topicModel) setSize(w, h int) {
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h - 1) // leave room for title bar
}

func (m topicModel) update(msg tea.Msg) (topicModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m topicModel) view() string {
	titleBar := m.styles.title.Render(m.title)
	return titleBar + "\n" + m.viewport.View()
}

func (m topicModel) renderEntries(entries []models.Entry) string {
	var b strings.Builder
	sep := m.styles.separator.Render(strings.Repeat("─", 60))

	for i, e := range entries {
		if i > 0 {
			fmt.Fprintf(&b, "%s\n", sep)
		}

		from := e.Creator.Name
		if from == "" {
			from = e.Creator.EmailAddress
		}
		if e.AlternativeSenderName != "" {
			from = e.AlternativeSenderName
		}

		date := ""
		if len(e.CreatedAt) >= 16 {
			date = e.CreatedAt[:16]
		}

		fmt.Fprintf(&b, "%s  %s\n", m.styles.entryFrom.Render(from), m.styles.entryDate.Render(date))
		if e.Summary != "" {
			fmt.Fprintf(&b, "%s\n", e.Summary)
		}
		if e.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", m.styles.entryBody.Render(htmlToText(e.Body)))
		}
		b.WriteString("\n")
	}

	return b.String()
}
