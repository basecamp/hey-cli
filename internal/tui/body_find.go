package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// bodyFind searches already rendered, safe text. Byte offsets belong to the plain
// line, never its SGR or OSC 8 sequences; styling preserves the original hyperlinks.
type bodyMatch struct {
	bodySpan
	parts []bodySpan
}

type bodyFind struct {
	editing bool
	input   textinput.Model
	query   string
	matches []bodyMatch
	current int
	limited bool
}

func (f *bodyFind) active() bool { return f.editing || f.query != "" }

func (f *bodyFind) start() tea.Cmd {
	f.input = newTextInput()
	f.input.Prompt = "/"
	f.input.Placeholder = "Find in message"
	f.input.CharLimit = 256
	f.input.SetValue(f.query)
	f.editing = true
	return f.input.Focus()
}

// edit returns true when Enter commits a new query. Escape keeps the previous one.
func (f *bodyFind) edit(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		f.editing = false
		f.input.Blur()
		return nil, false
	case tea.KeyEnter:
		f.query = terminal.SanitizeLine(strings.TrimSpace(f.input.Value()))
		f.editing = false
		f.input.Blur()
		return nil, true
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd, false
}

func (f *bodyFind) update(msg tea.Msg) tea.Cmd {
	if !f.editing {
		return nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *bodyFind) scan(documents ...findDocument) {
	f.matches = nil
	f.limited = false
	if f.query == "" {
		return
	}
	pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(f.query))
	budget := newFindBudget()
	for _, document := range documents {
		f.matches = append(f.matches, document.matches(pattern, &budget)...)
		if budget.limited {
			break
		}
	}
	f.limited = budget.limited
	f.current = min(max(f.current, 0), max(len(f.matches)-1, 0))
}

func (f *bodyFind) firstFrom(line int) {
	f.current = 0
	for i, match := range f.matches {
		if match.line >= line {
			f.current = i
			return
		}
	}
}

func (f *bodyFind) next(delta int) {
	if len(f.matches) > 0 {
		f.current = (f.current + delta + len(f.matches)) % len(f.matches)
	}
}

func (f *bodyFind) selected() (bodyMatch, bool) {
	if f.current < 0 || f.current >= len(f.matches) {
		return bodyMatch{}, false
	}
	return f.matches[f.current], true
}

func (f *bodyFind) highlight(lines []string) []string {
	if len(f.matches) == 0 {
		return lines
	}
	out := append([]string(nil), lines...)
	var spans []highlightSpan
	for i, match := range f.matches {
		for _, span := range match.parts {
			spans = append(spans, highlightSpan{bodySpan: span, selected: i == f.current})
		}
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].line != spans[j].line {
			return spans[i].line < spans[j].line
		}
		return spans[i].start < spans[j].start
	})
	for i := 0; i < len(spans); {
		line, start := spans[i].line, i
		for i < len(spans) && spans[i].line == line {
			i++
		}
		out[line] = highlightBodyLine(lines[line], spans[start:i])
	}
	return out
}

func (f *bodyFind) footer(width int) string {
	if f.editing {
		f.input.SetWidth(max(width-2, 1))
		return ansi.Truncate(f.input.View(), max(width, 1), "")
	}
	status := "no matches"
	if len(f.matches) > 0 {
		status = fmt.Sprintf("%d/%d", f.current+1, len(f.matches))
	}
	if f.limited {
		if len(f.matches) == 0 {
			status = "results limited"
		} else {
			status += " · limited"
		}
		return truncateStr(fmt.Sprintf("%s /%s · n/N next/previous · esc clear", status, f.query), max(width, 1))
	}
	return truncateStr(fmt.Sprintf("/%s  %s · n/N next/previous · esc clear", f.query, status), max(width, 1))
}

func (f *bodyFind) helpBindings() []helpBinding {
	if f.editing {
		return []helpBinding{{"enter", "find"}, {"esc", "cancel find"}}
	}
	bindings := []helpBinding{{"/", "find in message"}}
	if f.query != "" {
		bindings = append(bindings, helpBinding{"n/N", "next/previous match"}, helpBinding{"esc", "clear find"})
	}
	return bindings
}

// Keep OSC 8 hyperlinks intact and restore the renderer's SGR state after each
// match. The decoder keeps a grapheme and each control sequence indivisible.
func highlightBodyLine(line string, matches []highlightSpan) string {
	var out, original strings.Builder
	position, index, active := 0, 0, -1
	var state byte
	highlight := func(i int) string {
		if matches[i].selected {
			return "\x1b[1;4;7m"
		}
		return "\x1b[4m"
	}
	for line != "" {
		seq, _, n, nextState := ansi.DecodeSequence(line, state, nil)
		state = nextState
		if n == 0 {
			break
		}
		line = line[n:]
		if strings.HasPrefix(seq, "\x1b") {
			out.WriteString(seq)
			if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") {
				if seq == "\x1b[m" || seq == "\x1b[0m" {
					original.Reset()
				} else {
					original.WriteString(seq)
				}
				if active >= 0 {
					out.WriteString(highlight(active))
				}
			}
			continue
		}
		for index < len(matches) && matches[index].end <= position {
			index++
		}
		next := -1
		if index < len(matches) && matches[index].start < position+len(seq) {
			next = index
		}
		if next != active {
			if active >= 0 {
				out.WriteString("\x1b[0m")
				out.WriteString(original.String())
			}
			if next >= 0 {
				out.WriteString(highlight(next))
			}
			active = next
		}
		out.WriteString(seq)
		position += len(seq)
	}
	if active >= 0 {
		out.WriteString("\x1b[0m")
		out.WriteString(original.String())
	}
	return out.String()
}

// matchDestination reads only the safe renderer's OSC 8 metadata. Searching never
// matches escape payloads; this is used after a visible match to select its link.
func matchDestination(line string, start int) string {
	position := 0
	destination := ""
	var state byte
	for line != "" {
		seq, _, n, nextState := ansi.DecodeSequence(line, state, nil)
		state = nextState
		if n == 0 {
			break
		}
		line = line[n:]
		if strings.HasPrefix(seq, "\x1b]8;") {
			body := strings.TrimSuffix(strings.TrimSuffix(seq[4:], "\a"), "\x1b\\")
			_, destination, _ = strings.Cut(body, ";")
		} else if !strings.HasPrefix(seq, "\x1b") {
			if position <= start && start < position+len(seq) {
				return destination
			}
			position += len(seq)
		}
	}
	return ""
}
