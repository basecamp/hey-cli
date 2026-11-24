package tui

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/markdown"
)

// Search runs carry their own exact display coordinates. Prose can continue onto
// another row; tables stay cell-local, and URLs use the renderer's link fragments.
type findDocument struct {
	lines []string
	start int
	links []markdown.LinkTextRun
}

type bodySpan struct {
	line       int
	start, end int
}

type highlightSpan struct {
	bodySpan
	selected bool
}

type findRunPart struct {
	bodySpan
	offset int
}

type findRun struct {
	text  strings.Builder
	parts []findRunPart
}

func (r *findRun) append(text string, span bodySpan, join string) {
	if len(r.parts) > 0 {
		r.text.WriteString(join)
	}
	r.parts = append(r.parts, findRunPart{bodySpan: span, offset: r.text.Len()})
	r.text.WriteString(text[span.start:span.end])
}

func (r *findRun) matches(pattern *regexp.Regexp, budget *findBudget) []bodyMatch {
	if budget.limited {
		return nil
	}
	var matches []bodyMatch
	partIndex := 0
	// Request one extra hit to detect truncation without materializing every
	// occurrence in a dense document.
	for _, bounds := range pattern.FindAllStringIndex(r.text.String(), min(budget.candidates, budget.spans)+1) {
		if budget.candidates == 0 || budget.spans == 0 {
			budget.limited = true
			break
		}
		var parts []bodySpan
		for partIndex < len(r.parts) && r.parts[partIndex].offset+r.parts[partIndex].end-r.parts[partIndex].start <= bounds[0] {
			partIndex++
		}
		for i := partIndex; i < len(r.parts) && r.parts[i].offset < bounds[1]; i++ {
			part := r.parts[i]
			start := max(bounds[0], part.offset)
			end := min(bounds[1], part.offset+part.end-part.start)
			if end > start {
				span := bodySpan{line: part.line, start: part.start + start - part.offset, end: part.start + end - part.offset}
				if len(parts) > 0 && parts[len(parts)-1].line == span.line && parts[len(parts)-1].end == span.start {
					parts[len(parts)-1].end = span.end
				} else {
					if len(parts) == budget.spans {
						budget.limited = true
						return matches
					}
					parts = append(parts, span)
				}
			}
		}
		if len(parts) > 0 {
			budget.candidates--
			budget.spans -= len(parts)
			matches = append(matches, bodyMatch{bodySpan: parts[0], parts: parts})
		}
	}
	return matches
}

func (d findDocument) matches(pattern *regexp.Regexp, budget *findBudget) []bodyMatch {
	plain := make([]string, len(d.lines))
	for i, line := range d.lines {
		plain[i] = ansi.Strip(line)
	}
	// A URL continued onto the next row has no space at that wrap. The
	// renderer, rather than a guess from the URL's spelling, identifies it.
	urlJoins := make(map[int]bool)
	for _, link := range d.links {
		if !link.Destination {
			continue
		}
		for i := 1; i < len(link.Spans); i++ {
			prev, next := link.Spans[i-1], link.Spans[i]
			if next.Line != prev.Line+1 {
				continue
			}
			start, _, _, _ := findLineBounds(plain[next.Line])
			end := len(strings.TrimRightFunc(plain[prev.Line], unicode.IsSpace))
			if prev.End >= end && next.Start <= start {
				urlJoins[prev.Line] = true
			}
		}
	}

	var matches, literals, urlMatches []bodyMatch
	// Generate the highest-priority representation first so a limited scan
	// cannot replace a wrapped URL occurrence with a partial cell-line hit.
	for _, link := range d.links {
		if budget.limited {
			break
		}
		if !link.Destination || len(link.Spans) < 2 {
			continue
		}
		url := findRun{}
		for _, span := range link.Spans {
			text := plain[span.Line]
			part := text[span.Start:span.End]
			start := span.Start + len(part) - len(strings.TrimLeftFunc(part, unicode.IsSpace))
			end := span.Start + len(strings.TrimRightFunc(part, unicode.IsSpace))
			if end > start {
				url.append(text, bodySpan{line: d.start + span.Line, start: start, end: end}, "")
			}
		}
		urlMatches = append(urlMatches, url.matches(pattern, budget)...)
	}
	var run findRun
	flush := func() {
		matches = append(matches, run.matches(pattern, budget)...)
		run = findRun{}
	}
	previousQuote := 0
	for line, text := range plain {
		if budget.limited {
			break
		}
		start, end, quote, list := findLineBounds(text)
		if start == end {
			flush()
			continue
		}
		if quote != previousQuote || list {
			flush()
		}
		previousQuote = quote
		if strings.ContainsRune(text[start:end], '│') || strings.Trim(text[start:end], "─┼┌┐└┘├┤┬┴") == "" {
			flush()
			// Do not infer table row identity: wrapped cells interleave, and
			// neighboring data rows need not have a visible separator.
			for column := start; column < end && !budget.limited; {
				stop := end
				if next := strings.IndexRune(text[column:end], '│'); next >= 0 {
					stop = column + next
				}
				cell := findRun{}
				cell.append(text, bodySpan{line: d.start + line, start: column, end: stop}, "")
				matches = append(matches, cell.matches(pattern, budget)...)
				column = stop + len("│")
			}
			continue
		}
		join := " "
		if urlJoins[line-1] {
			join = ""
		}
		run.append(text, bodySpan{line: d.start + line, start: start, end: end}, join)
	}
	flush()

	// Ordinary prose is already indexed. Only structural-looking rows need
	// the literal fallback, avoiding duplicate candidates for every normal row.
	for line, text := range plain {
		if budget.limited {
			break
		}
		if !strings.ContainsAny(text, "│─┼┌┐└┘├┤┬┴") {
			continue
		}
		literal := findRun{}
		literal.append(text, bodySpan{line: d.start + line, end: len(text)}, "")
		literals = append(literals, literal.matches(pattern, budget)...)
	}
	matches = appendNonoverlappingMatches(urlMatches, matches)
	matches = appendNonoverlappingMatches(matches, literals)
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].line != matches[j].line {
			return matches[i].line < matches[j].line
		}
		return matches[i].start < matches[j].start
	})
	return slices.CompactFunc(matches, func(a, b bodyMatch) bool { return slices.Equal(a.parts, b.parts) })
}

// URL runs are authoritative at URL wraps, then prose/cell runs, then literal
// fallbacks. A lower-priority run cannot introduce overlapping occurrences.
func appendNonoverlappingMatches(matches, candidates []bodyMatch) []bodyMatch {
	rows := make(map[int][]bodySpan)
	for _, match := range matches {
		for _, span := range match.parts {
			rows[span.line] = append(rows[span.line], span)
		}
	}
	for line, spans := range rows {
		sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
		merged := spans[:0]
		for _, span := range spans {
			if len(merged) > 0 && span.start <= merged[len(merged)-1].end {
				merged[len(merged)-1].end = max(merged[len(merged)-1].end, span.end)
			} else {
				merged = append(merged, span)
			}
		}
		rows[line] = merged
	}
	for _, match := range candidates {
		overlaps := false
		for _, part := range match.parts {
			spans := rows[part.line]
			i := sort.Search(len(spans), func(i int) bool { return spans[i].end > part.start })
			if i < len(spans) && spans[i].start < part.end {
				overlaps = true
				break
			}
		}
		if !overlaps {
			matches = append(matches, match)
		}
	}
	return matches
}

func findLineBounds(text string) (start, end, quote int, list bool) {
	end = len(strings.TrimRightFunc(text, unicode.IsSpace))
	start = len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	if start >= end {
		return end, end, 0, false
	}
	for strings.HasPrefix(text[start:end], "│ ") {
		quote++
		start += len("│ ")
		start += len(text[start:end]) - len(strings.TrimLeftFunc(text[start:end], unicode.IsSpace))
	}
	visible := text[start:end]
	list = strings.HasPrefix(visible, "• ")
	if first, _, found := strings.Cut(visible, ". "); found && first != "" {
		list = list || strings.Trim(first, "0123456789") == ""
	}
	return start, end, quote, list
}
