package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// isOrganizedMailSource reports whether a source is one of the reader's own ways of
// organizing mail rather than one of HEY's boxes. Those page through their own feeds and
// have no box to be told about.
func isOrganizedMailSource(kind mail.Kind) bool {
	return kind == mail.KindFolder || kind == mail.KindCollection
}

type folderPickerChoice int

const (
	folderPickerExisting folderPickerChoice = iota
	folderPickerCreate
	folderPickerRemoveAll
)

type folderPickerSelection struct {
	kind   folderPickerChoice
	folder mail.Source
}

type folderPicker struct {
	plainModal

	posting  mail.Posting
	folders  []mail.Source
	cursor   int
	offset   int
	height   int
	creating bool
	input    textinput.Model
	status   string
}

func newFolderPicker(posting mail.Posting, sources []mail.Source) *folderPicker {
	folders := make([]mail.Source, 0, len(sources))
	for _, source := range sources {
		if source.Kind == mail.KindFolder {
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

// handleKey routes keys through the picker's two states. Escape while a label is being
// named steps back to the list rather than closing the picker, which is the one place
// escape does not mean cancel.
func (p *folderPicker) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if msg.Key().Code == tea.KeyEscape {
		if p.creating {
			p.cancelCreate()
			return nil, true
		}
		return nil, false
	}
	if p.creating {
		if msg.Key().Code == tea.KeyEnter {
			name, ok := p.createName()
			if !ok {
				return nil, true
			}
			return view.createFolderForPosting(p.posting.ID, name), false
		}
		return p.update(msg), true
	}
	if msg.Key().Code == tea.KeyEnter {
		if selection := p.selected(); selection != nil {
			switch selection.kind {
			case folderPickerExisting:
				if p.postingHasFolder(selection.folder.ID) {
					return view.unfilePosting(p.posting.ID, selection.folder.ID, selection.folder.Name), false
				}
				return view.filePosting(p.posting.ID, selection.folder.ID, selection.folder.Name), false
			case folderPickerCreate:
				return p.startCreate(), true
			case folderPickerRemoveAll:
				return view.unfilePosting(p.posting.ID, 0, ""), false
			}
		}
		return nil, true
	}
	p.moveCursor(msg)
	return nil, true
}

// handleMsg gives the name field its cursor blinks, and only while it is up.
func (p *folderPicker) handleMsg(msg tea.Msg) (tea.Cmd, bool) {
	if p.creating {
		return p.update(msg), true
	}
	return nil, false
}

func (p *folderPicker) draw(view *mailView) string {
	return p.view(view.vc.styles, view.vc.width)
}

func (p *folderPicker) update(msg tea.Msg) tea.Cmd {
	if !p.creating {
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

func (p *folderPicker) moveCursor(msg tea.KeyPressMsg) {
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
		b.WriteString(styleMuted.Render("Name: "))
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
			label = mark + " " + terminal.SanitizeLine(choice.folder.Name)
		case folderPickerCreate:
			label = "+ Create a new label…"
		case folderPickerRemoveAll:
			label = "− Remove all labels"
		}
		label = truncateStr(label, max(contentWidth-lipgloss.Width(prefix), 1))
		if i == p.cursor {
			label = styles.title.Render(label)
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, label)
	}
	return b.String()
}

func (p *folderPicker) helpBindings() []helpBinding {
	if p.creating {
		return []helpBinding{{"enter", "create"}, {"esc", "back"}}
	}
	return []helpBinding{{"↑↓", "select"}, {"enter", "toggle"}, {"esc", "cancel"}}
}
