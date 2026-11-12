package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	case "/":
		return find.start(), true
	case "n", "N":
		if find.query != "" {
			delta := 1
			if msg.String() == "N" {
				delta = -1
			}
			if len(find.matches) > 0 {
				find.next(delta)
				v.showBodyMatch()
			}
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
	if _, found := v.bodyFind.selected(); found {
		v.followFind = true
	}
	if v.followFind {
		v.selectFindLink()
		if v.bodyFind.query == "" {
			v.followFind = false
		}
	}
	v.topicViewport.SetContentLines(v.bodyFind.highlight(v.topicLines))
	if v.followFind {
		v.revealBodyMatch()
	}
}

// A find-owned link always follows the current result, including after a rescan
// changes the result set. Manual Tab selections belong to link navigation instead.
func (v *mailView) selectFindLink() {
	v.restoreLinkBody(v.selectedLink)
	v.selectedLink = -1
	v.selectedLinkKey = ""
	match, found := v.bodyFind.selected()
	if !found {
		return
	}
	for i, link := range v.links {
		if link.body < 0 || link.body >= len(v.linkBodies) {
			continue
		}
		start := v.linkBodies[link.body].start
		for _, run := range link.textRuns {
			for _, span := range run.Spans {
				if start+span.Line == match.line && span.Start <= match.start && match.start < span.End {
					v.selectedLink = i
					v.selectedLinkKey = link.key
					return
				}
			}
		}
	}
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
