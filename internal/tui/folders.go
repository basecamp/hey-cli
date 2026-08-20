package tui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

const mailSourceKindFolder = "folder"

type folderPickerChoice int

const (
	folderPickerExisting folderPickerChoice = iota
	folderPickerCreate
	folderPickerRemoveAll
)

type folderPickerSelection struct {
	kind   folderPickerChoice
	folder models.Box
}

type folderPicker struct {
	posting  models.Posting
	folders  []models.Box
	cursor   int
	offset   int
	height   int
	creating bool
	input    textinput.Model
	status   string
}

func newFolderPicker(posting models.Posting, sources []models.Box) *folderPicker {
	folders := make([]models.Box, 0, len(sources))
	for _, source := range sources {
		if source.Kind == mailSourceKindFolder {
			folders = append(folders, source)
		}
	}
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Label name…"
	return &folderPicker{posting: posting, folders: folders, input: input}
}

func (p *folderPicker) choices() []folderPickerSelection {
	choices := make([]folderPickerSelection, 0, len(p.folders)+2)
	for _, folder := range p.folders {
		choices = append(choices, folderPickerSelection{kind: folderPickerExisting, folder: folder})
	}
	choices = append(choices, folderPickerSelection{kind: folderPickerCreate})
	if len(p.posting.Folders) > 0 {
		choices = append(choices, folderPickerSelection{kind: folderPickerRemoveAll})
	}
	return choices
}

func (p *folderPicker) selected() *folderPickerSelection {
	choices := p.choices()
	if p.cursor < 0 || p.cursor >= len(choices) {
		return nil
	}
	return &choices[p.cursor]
}

func (p *folderPicker) postingHasFolder(folderID int64) bool {
	for _, folder := range p.posting.Folders {
		if folder.ID == folderID {
			return true
		}
	}
	return false
}

func (p *folderPicker) startCreate() tea.Cmd {
	p.creating = true
	p.status = ""
	p.input.SetValue("")
	return p.input.Focus()
}

func (p *folderPicker) cancelCreate() {
	p.creating = false
	p.status = ""
	p.input.Blur()
}

func (p *folderPicker) createName() (string, bool) {
	name := strings.TrimSpace(p.input.Value())
	if name == "" {
		p.status = "Enter a label name"
		return "", false
	}
	return name, true
}

func (p *folderPicker) update(msg tea.Msg) tea.Cmd {
	if !p.creating {
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

func (p *folderPicker) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if p.creating {
		return p.update(msg)
	}
	switch msg.Key().Code {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.choices())-1 {
			p.cursor++
		}
	}
	p.ensureVisible()
	return nil
}

func (p *folderPicker) resize(width, height int) {
	p.height = height
	p.input.SetWidth(max(width-16, 10))
	p.ensureVisible()
}

func (p *folderPicker) visibleRows() int {
	return max(p.height-5, 1)
}

func (p *folderPicker) ensureVisible() {
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	rows := p.visibleRows()
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
}

func (p *folderPicker) view(styles styles, width int) string {
	contentWidth := max(width-4, 1)
	var b strings.Builder
	if p.creating {
		b.WriteString(styles.title.Render("Create label"))
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("Name: "))
		b.WriteString(p.input.View())
		if p.status != "" {
			b.WriteString("\n\n")
			b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(p.status))
		}
		return b.String()
	}

	b.WriteString(styles.title.Render("Label thread"))
	if p.posting.Summary != "" {
		fmt.Fprintf(&b, "\n%s", truncateStr(p.posting.Summary, contentWidth))
	}
	b.WriteString("\n\n")
	choices := p.choices()
	end := min(p.offset+p.visibleRows(), len(choices))
	for i := p.offset; i < end; i++ {
		choice := choices[i]
		prefix := "  "
		if i == p.cursor {
			prefix = "› "
		}
		label := ""
		switch choice.kind {
		case folderPickerExisting:
			mark := "[ ]"
			if p.postingHasFolder(choice.folder.ID) {
				mark = "[x]"
			}
			label = mark + " " + terminalSafeFolderText(choice.folder.Name)
		case folderPickerCreate:
			label = "+ Create a new label…"
		case folderPickerRemoveAll:
			label = "− Remove all labels"
		}
		if i == p.cursor {
			label = styles.title.Render(label)
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, label)
	}
	return b.String()
}

func terminalSafeFolderText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, value)
}

func (p *folderPicker) helpBindings() []helpBinding {
	if p.creating {
		return []helpBinding{{"enter", "create"}, {"esc", "back"}}
	}
	return []helpBinding{{"↑↓", "select"}, {"enter", "toggle"}, {"esc", "cancel"}}
}
