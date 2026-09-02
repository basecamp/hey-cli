package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Layout names the amount of structure and space the TUI gives its content.
// Classic is the original edge-to-edge presentation. Spacious separates list rows
// and adds vertical breathing room, while leaving the terminal theme in charge of color.
type Layout string

const (
	LayoutClassic  Layout = "classic"
	LayoutSpacious Layout = "spacious"
)

// ParseLayout reads a layout name accepted by the tui command.
func ParseLayout(value string) (Layout, error) {
	switch layout := Layout(strings.ToLower(strings.TrimSpace(value))); layout {
	case LayoutClassic, LayoutSpacious:
		return layout, nil
	default:
		return "", fmt.Errorf("layout must be classic or spacious (got %q)", value)
	}
}

func (l Layout) normalized() Layout {
	if l == LayoutSpacious {
		return l
	}
	return LayoutClassic
}

func (l Layout) toggled() Layout {
	if l.normalized() == LayoutSpacious {
		return LayoutClassic
	}
	return LayoutSpacious
}

// layoutMetrics are the cells spent around and between content. A small terminal
// keeps the selected layout but collapses its chrome until there is room again.
type layoutMetrics struct {
	spacious bool
	paddingX int
	paddingY int
	itemGap  int
}

func (l Layout) metrics(width, height int) layoutMetrics {
	if l.normalized() != LayoutSpacious || width < 48 || height < 16 {
		return layoutMetrics{}
	}
	return layoutMetrics{spacious: true, paddingY: 1, itemGap: 1}
}

func (m layoutMetrics) horizontalChrome() int {
	if !m.spacious {
		return 0
	}
	return 2 * m.paddingX
}

func (m layoutMetrics) verticalChrome() int {
	if !m.spacious {
		return 0
	}
	return 2 * m.paddingY
}

func (m layoutMetrics) headerGap() int {
	if m.spacious {
		return 1
	}
	return 0
}

func (m layoutMetrics) footerChrome() int {
	if m.spacious {
		return 0
	}
	return 3 // two clear rows and the divider
}

func (m layoutMetrics) drawFooterRule() bool {
	return !m.spacious
}

func (m layoutMetrics) render(content string, width, height int) string {
	if !m.spacious {
		return content
	}
	return lipgloss.NewStyle().
		Padding(m.paddingY, m.paddingX).
		Width(width).
		Height(height).
		Render(content)
}
