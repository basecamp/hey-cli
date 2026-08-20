// Package markdown renders Markdown for the terminal. It wraps glamour for the
// styling and adds OSC 8 hyperlinks so URLs stay clickable.
package markdown

import (
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
)

// DefaultWidth is the word wrap used when the caller has no width of its own.
const DefaultWidth = 80

var (
	renderersMutex sync.Mutex
	renderers      = map[int]*glamour.TermRenderer{}
)

// Render renders Markdown as styled terminal output wrapped to width. It falls
// back to the Markdown source when glamour cannot be set up, so a rendering
// problem costs formatting rather than the message itself.
func Render(md string, width int) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	if width <= 0 {
		width = DefaultWidth
	}

	renderer, err := cachedRenderer(width)
	if err != nil {
		return LinkifyURLs(md)
	}

	out, err := renderer.Render(md)
	if err != nil {
		return LinkifyURLs(md)
	}

	return trimBlankLines(LinkifyURLs(out))
}

// trimBlankLines strips the padding glamour adds out to the wrap width, so a
// rendered body carries neither invisible whitespace nor lines that are
// nothing but leftover color codes.
func trimBlankLines(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(ansi.Strip(line)) == "" {
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				continue
			}
			lines = append(lines, "")
		} else {
			lines = append(lines, strings.TrimRight(line, " "))
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func cachedRenderer(width int) (*glamour.TermRenderer, error) {
	renderersMutex.Lock()
	defer renderersMutex.Unlock()

	if renderer, ok := renderers[width]; ok {
		return renderer, nil
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(terminalStyle),
		glamour.WithWordWrap(width),
		// Email leans on line breaks — signatures, addresses, quoted replies —
		// so a break in the source is a break on screen.
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil, err
	}
	renderers[width] = renderer
	return renderer, nil
}
