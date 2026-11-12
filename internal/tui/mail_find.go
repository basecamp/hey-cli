package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/markdown"
)

func (v *mailView) handleFindKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	find := &v.bodyFind
	if find.editing {
		cmd, committed := find.edit(msg)
		if committed {
			v.scanBodyFind()
			find.firstFrom(v.topicViewport.YOffset())
			v.showBodyMatch()
		}
		return cmd, true
	}
	switch msg.String() {
	case "enter":
		if find.query != "" && !v.LinkSelectionActive() {
			return nil, true
		}
	case "/":
		return find.start(), true
	case "n", "N":
		if find.query != "" {
			delta := 1
			if msg.String() == "N" {
				delta = -1
			}
			find.next(delta)
			v.showBodyMatch()
			return nil, true
		}
	}
	return nil, false
}

// Only hydrated message bodies are searched, leaving headers, missing-body
// notices, attachment panels and Kitty image placeholders out of the index.
func (v *mailView) scanBodyFind() {
	documents := make([]findDocument, 0, len(v.linkBodies))
	for _, body := range v.linkBodies {
		documents = append(documents, findDocument{lines: body.lines, start: body.start, links: body.linkRuns})
	}
	v.bodyFind.scan(documents...)
}

func (v *mailView) clearBodyFind() {
	v.bodyFind = bodyFind{}
	v.followFind = false
	v.clearLinkSelection()
	v.topicViewport.SetContentLines(v.topicLines)
}

func (v *mailView) showBodyMatch() {
	v.followFind = true
	v.restoreLinkBody(v.selectedLink)
	v.selectedLink = -1
	v.selectedLinkKey = ""
	match, found := v.bodyFind.selected()
	if found {
		// A visible match inside a hyperlink selects that exact destination.
		// Enter still uses the existing full-URL review footer before opening it.
		destination := matchDestination(v.topicLines[match.line], match.start)
		selected := -1
		for i, link := range v.links {
			if destination != "" && match.line >= link.startLine && match.line <= link.endLine && markdown.SameURL(destination, link.destination) {
				selected = i
				break
			}
		}
		if selected >= 0 {
			v.selectedLink = selected
			v.selectedLinkKey = v.links[selected].key
		}
	}
	v.topicViewport.SetContentLines(v.bodyFind.highlight(v.topicLines))
	v.revealBodyMatch()
}

func (v *mailView) revealBodyMatch() {
	match, found := v.bodyFind.selected()
	if found {
		plain := ansi.Strip(v.topicLines[match.line])
		v.topicViewport.EnsureVisible(match.line, ansi.StringWidth(plain[:match.start]), ansi.StringWidth(plain[:match.end]))
	}
}

func (v *mailView) revealThreadSelection() {
	if v.followFind && v.bodyFind.active() {
		v.revealBodyMatch()
	} else {
		v.revealLink()
	}
}
