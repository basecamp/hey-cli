package markdown

import (
	"net/url"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

// LinkOccurrence describes one selectable hyperlink in rendered document order.
// Lines are zero-based and inclusive. A wrapped link therefore has different
// StartLine and EndLine values.
type LinkOccurrence struct {
	Destination string
	StartLine   int
	EndLine     int
}

// LinkedRender is the contained terminal rendering of one Markdown body and
// the safe hyperlink occurrences it contains.
type LinkedRender struct {
	Text  string
	Links []LinkOccurrence
}

// RenderLinked renders one sealed Markdown body and records each selectable
// hyperlink in the order it appears. selected is a zero-based occurrence index;
// a negative value leaves all links unselected.
func RenderLinked(md htmlutil.Markdown, width, selected int) LinkedRender {
	out := render(md.String(), width)
	return linkedRender(out, selected)
}

func linkedRender(out string, selected int) LinkedRender {
	var b strings.Builder
	b.Grow(len(out))
	links := make([]LinkOccurrence, 0)
	line := 0
	currentDestination := ""
	currentShownDestination := false
	currentShownText := ""
	currentComplete := false
	for i := 0; i < len(out); {
		if strings.HasPrefix(out[i:], "\x1b]8;") {
			openEnd, _, ok := hyperlink(out[i:])
			if ok {
				_, destination, found := hyperlinkDestination(openEnd)
				if found && destination != "" {
					contentStart := i + len(openEnd)
					closeRel := strings.Index(out[contentStart:], "\x1b]8;")
					if closeRel >= 0 {
						closeStart := contentStart + closeRel
						closeEnd, _, closeOK := hyperlink(out[closeStart:])
						if closeOK {
							if allowedHyperlink(destination) {
								startLine := line
								endLine := line + strings.Count(out[contentStart:closeStart], "\n")
								content := out[contentStart:closeStart]
								if destination != currentDestination || currentComplete {
									links = append(links, LinkOccurrence{Destination: destination, StartLine: startLine, EndLine: endLine})
									currentDestination = destination
									currentShownDestination = false
									currentShownText = ""
								} else {
									links[len(links)-1].EndLine = endLine
								}
								// Glamour renders a named anchor as one OSC 8 span for
								// its label and one or more underlined spans for the shown
								// destination. Only those destination spans complete the
								// occurrence. A plain fallback URL has no style prefix.
								if !currentShownDestination && (precededByUnderline(out, i) || content == destination) {
									currentShownDestination = true
								}
								if currentShownDestination {
									currentShownText += withoutWhitespace(ansi.Strip(content))
									currentComplete = currentShownText == withoutWhitespace(destination)
								} else {
									currentComplete = false
								}

								b.WriteString(openEnd)
								if len(links)-1 == selected {
									b.WriteString("\x1b[7m")
									b.WriteString(selectedContent(content))
									b.WriteString("\x1b[27m")
								} else {
									b.WriteString(content)
								}
								b.WriteString(closeEnd)
								line = endLine
								i = closeStart + len(closeEnd)
								continue
							}
						}
					}
				}
			}
		}
		if out[i] == '\n' {
			line++
		}
		b.WriteByte(out[i])
		i++
	}
	return LinkedRender{Text: contain(b.String()), Links: links}
}

func hyperlinkDestination(sequence string) (params, destination string, found bool) {
	body := sequence[len("\x1b]8;"):]
	body = strings.TrimSuffix(body, "\a")
	body = strings.TrimSuffix(body, "\x1b\\")
	params, destination, found = strings.Cut(body, ";")
	return params, destination, found
}

func allowedHyperlink(uri string) bool {
	if strings.ContainsFunc(uri, isControl) {
		return false
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.User == nil && parsed.Host != "" && parsed.Hostname() != ""
	case "mailto":
		return parsed.Opaque != ""
	default:
		return false
	}
}

func precededByUnderline(out string, position int) bool {
	start := strings.LastIndex(out[:position], "\x1b[")
	if start < 0 {
		return false
	}
	sequence, _, ok := sgr(out[start:position])
	if !ok || start+len(sequence) != position {
		return false
	}
	parameters := strings.FieldsFunc(sequence[2:len(sequence)-1], func(r rune) bool {
		return r == ';' || r == ':'
	})
	for _, parameter := range parameters {
		if parameter == "4" {
			return true
		}
	}
	return false
}

func withoutWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
}

func selectedContent(content string) string {
	var b strings.Builder
	b.Grow(len(content) + 16)
	for i := 0; i < len(content); {
		if content[i] == '\x1b' && strings.HasPrefix(content[i:], "\x1b[") {
			sequence, _, ok := sgr(content[i:])
			if ok {
				b.WriteString(sequence)
				// A nested reset can turn reverse video off. Reapply it after
				// every SGR while the selected label is still visible.
				b.WriteString("\x1b[7m")
				i += len(sequence)
				continue
			}
		}
		b.WriteByte(content[i])
		i++
	}
	return b.String()
}
