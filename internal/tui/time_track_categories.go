package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

type timeTrackCategoryEditMode int

const (
	timeTrackCategoryBrowse timeTrackCategoryEditMode = iota
	timeTrackCategoryCreate
	timeTrackCategoryRename
)

type timeTrackCategoryManager struct {
	categories       []generated.TimeTrackCategory
	cursor           int
	mode             timeTrackCategoryEditMode
	input            textinput.Model
	status           string
	confirmingDelete bool
}

func newTimeTrackCategoryManager() *timeTrackCategoryManager {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Category title…"
	return &timeTrackCategoryManager{input: input}
}

func (m *timeTrackCategoryManager) selected() *generated.TimeTrackCategory {
	if m.cursor < 0 || m.cursor >= len(m.categories) {
		return nil
	}
	return &m.categories[m.cursor]
}

func (m *timeTrackCategoryManager) setCategories(categories []generated.TimeTrackCategory) {
	selectedID := int64(0)
	if selected := m.selected(); selected != nil {
		selectedID = selected.Id
	}
	m.categories = categories
	m.cursor = min(m.cursor, max(len(categories)-1, 0))
	for i := range categories {
		if categories[i].Id == selectedID {
			m.cursor = i
			break
		}
	}
	m.confirmingDelete = false
}

func (m *timeTrackCategoryManager) startCreate() tea.Cmd {
	m.mode = timeTrackCategoryCreate
	m.confirmingDelete = false
	m.status = ""
	m.input.SetValue("")
	return m.input.Focus()
}

func (m *timeTrackCategoryManager) startRename() tea.Cmd {
	selected := m.selected()
	if selected == nil {
		m.status = "Choose a category to rename"
		return nil
	}
	m.mode = timeTrackCategoryRename
	m.confirmingDelete = false
	m.status = ""
	m.input.SetValue(selected.Title)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *timeTrackCategoryManager) cancelEdit() {
	m.mode = timeTrackCategoryBrowse
	m.status = ""
	m.input.Blur()
}

func (m *timeTrackCategoryManager) title() (string, bool) {
	title := strings.TrimSpace(m.input.Value())
	if title == "" {
		m.status = "Enter a category title"
		return "", false
	}
	return title, true
}

func (m *timeTrackCategoryManager) update(msg tea.Msg) tea.Cmd {
	if m.mode == timeTrackCategoryBrowse {
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *timeTrackCategoryManager) move(key tea.Key) {
	switch key.Code {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < len(m.categories)-1 {
			m.cursor++
		}
	}
	m.confirmingDelete = false
}

func (m *timeTrackCategoryManager) view(styles styles, width, height int) string {
	contentWidth := max(width-4, 1)
	var b strings.Builder

	if m.mode != timeTrackCategoryBrowse {
		title := "Create time track category"
		if m.mode == timeTrackCategoryRename {
			title = "Rename time track category"
		}
		b.WriteString(styles.title.Render(title))
		b.WriteString("\n\n")
		b.WriteString(styleMuted.Render("Title: "))
		m.input.SetWidth(max(contentWidth-9, 10))
		b.WriteString(m.input.View())
		if m.status != "" {
			b.WriteString("\n\n")
			b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(m.status))
		}
		return b.String()
	}

	b.WriteString(styles.title.Render("Time track categories"))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("Categories organize completed time tracks."))
	b.WriteString("\n\n")

	if len(m.categories) == 0 {
		b.WriteString(styleMuted.Render("No categories yet. Press n to create one."))
		b.WriteString("\n")
	} else {
		visibleRows := max(height-7, 1)
		start := 0
		if m.cursor >= visibleRows {
			start = m.cursor - visibleRows + 1
		}
		end := min(start+visibleRows, len(m.categories))
		for i := start; i < end; i++ {
			category := m.categories[i]
			prefix := "  "
			line := fmt.Sprintf("%d  %s", category.Id, terminalSafeFolderText(category.Title))
			line = truncateStr(line, max(contentWidth-2, 1))
			if i == m.cursor {
				prefix = "› "
				line = styles.title.Render(line)
			}
			fmt.Fprintf(&b, "%s%s\n", prefix, line)
		}
	}

	if m.confirmingDelete {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render("Press x again to delete this category. Its time tracks will become uncategorized."))
	} else if m.status != "" {
		b.WriteString("\n")
		b.WriteString(styleMuted.Render(terminalSafeFolderText(m.status)))
	}
	return b.String()
}

func (m *timeTrackCategoryManager) helpBindings() []helpBinding {
	if m.mode != timeTrackCategoryBrowse {
		return []helpBinding{{"enter", "save"}, {"esc", "cancel"}}
	}
	return []helpBinding{
		{"↑↓", "select"},
		{"n", "new"},
		{"enter/r", "rename"},
		{"x", "delete"},
		{"esc/q", "back"},
	}
}
