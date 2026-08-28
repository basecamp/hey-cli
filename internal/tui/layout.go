package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Layout names the amount of structure and space the TUI gives its content.
// Classic is the original edge-to-edge presentation. Spacious insets the active
// section and separates list rows, while leaving the terminal theme in charge of color.
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
	inset    bool
	paddingX int
	paddingY int
	itemGap  int
}

func (l Layout) metrics(width, height int) layoutMetrics {
	if l.normalized() != LayoutSpacious || width < 48 || height < 16 {
		return layoutMetrics{}
	}
	return layoutMetrics{inset: true, paddingX: 2, paddingY: 1, itemGap: 1}
}

func (m layoutMetrics) horizontalChrome() int {
	if !m.inset {
		return 0
	}
	return 2 * m.paddingX
}

func (m layoutMetrics) verticalChrome() int {
	if !m.inset {
		return 0
	}
	return 2 * m.paddingY
}

func (m layoutMetrics) render(content string, width, height int) string {
	if !m.inset {
		return content
	}
	return lipgloss.NewStyle().
		Padding(m.paddingY, m.paddingX).
		Width(width).
		Height(height).
		Render(content)
}
