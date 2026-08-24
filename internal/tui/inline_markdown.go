package tui

import (
	"strings"
	"unicode"
)

// styleInlineMarkdown restyles a textarea's rendered view so the Markdown being
// written reads the way it will arrive: **bold** turns bold, *italic* italic,
// ~~struck~~ struck through, `code` and # headings take the colors
// internal/markdown gives them, and the delimiters themselves dim. Only SGR
// styling is added — every character and every sequence the textarea drew stays,
// and its own styling (the cursor, the cursor line) is restored where a span
// ends. A span styles only once its delimiters pair up on one visual line, so
// half-typed markup stays plain.
func styleInlineMarkdown(view string) string {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = styleInlineMarkdownLine(line)
	}
	return strings.Join(lines, "\n")
}

type inlineAttr uint8

const (
	attrMarker inlineAttr = 1 << iota // a Markdown delimiter, dimmed
	attrBold
	attrItalic
	attrStrike
	attrCode
	attrHeading
)

const ansiReset = "\x1b[0m"

func styleInlineMarkdownLine(line string) string {
	visible, prefixes, trailing := splitANSI(line)
	attrs := markdownLineAttrs(visible)
	for _, a := range attrs {
		if a != 0 {
			return rebuildLine(visible, prefixes, trailing, attrs)
		}
	}
	return line
}

// splitANSI takes a rendered line apart into its visible runes, the escape
// sequences preceding each of them, and the sequences after the last one.
func splitANSI(line string) (visible []rune, prefixes [][]string, trailing []string) {
	runes := []rune(line)
	var pending []string
	for i := 0; i < len(runes); {
		if runes[i] == 0x1b {
			seq := ansiSequenceAt(runes[i:])
			pending = append(pending, seq)
			i += len([]rune(seq))
			continue
		}
		visible = append(visible, runes[i])
		prefixes = append(prefixes, pending)
		pending = nil
		i++
	}
	return visible, prefixes, pending
}

// ansiSequenceAt reads one escape sequence starting at the ESC in rs[0]:
// a CSI sequence up to its final byte, an OSC up to its terminator, or the
// two-rune form everything else takes.
func ansiSequenceAt(rs []rune) string {
	if len(rs) < 2 {
		return string(rs)
	}
	switch rs[1] {
	case '[':
		for i := 2; i < len(rs); i++ {
			if rs[i] >= 0x40 && rs[i] <= 0x7e {
				return string(rs[:i+1])
			}
		}
		return string(rs)
	case ']':
		for i := 2; i < len(rs); i++ {
			if rs[i] == 0x07 {
				return string(rs[:i+1])
			}
			if rs[i] == 0x1b && i+1 < len(rs) && rs[i+1] == '\\' {
				return string(rs[:i+2])
			}
		}
		return string(rs)
	default:
		return string(rs[:2])
	}
}

func markdownLineAttrs(line []rune) []inlineAttr {
	attrs := make([]inlineAttr, len(line))
	markHeading(line, attrs)
	inCode := markCodeSpans(line, attrs)
	markEmphasis(line, attrs, inCode)
	return attrs
}

func markHeading(line []rune, attrs []inlineAttr) {
	hashes := runLength(line, 0, '#')
	if hashes == 0 || hashes > 6 || hashes >= len(line) || line[hashes] != ' ' {
		return
	}
	for i := range hashes + 1 {
		attrs[i] |= attrMarker
	}
	for i := hashes + 1; i < len(line); i++ {
		attrs[i] |= attrHeading
	}
}

// markCodeSpans pairs backtick runs of equal length, CommonMark's rule, and
// answers which positions are inside a span so the emphasis scan leaves them be.
func markCodeSpans(line []rune, attrs []inlineAttr) []bool {
	inCode := make([]bool, len(line))
	i := 0
	for i < len(line) {
		if line[i] != '`' {
			i++
			continue
		}
		open := runLength(line, i, '`')
		closer := findBacktickRun(line, i+open, open)
		if closer < 0 {
			i += open
			continue
		}
		for k := i; k < i+open; k++ {
			attrs[k] |= attrMarker
		}
		for k := i + open; k < closer; k++ {
			attrs[k] |= attrCode
			inCode[k] = true
		}
		for k := closer; k < closer+open; k++ {
			attrs[k] |= attrMarker
		}
		i = closer + open
	}
	return inCode
}

func findBacktickRun(line []rune, from, length int) int {
	for j := from; j < len(line); {
		if line[j] != '`' {
			j++
			continue
		}
		n := runLength(line, j, '`')
		if n == length {
			return j
		}
		j += n
	}
	return -1
}

func markEmphasis(line []rune, attrs []inlineAttr, inCode []bool) {
	stars := delimiterRuns(line, inCode, '*', false)
	pairStyle(stars, attrs, inCode, attrBold, 2)
	pairStyle(stars, attrs, inCode, attrItalic, 1)
	underscores := delimiterRuns(line, inCode, '_', true)
	pairStyle(underscores, attrs, inCode, attrBold, 2)
	pairStyle(underscores, attrs, inCode, attrItalic, 1)
	tildes := delimiterRuns(line, inCode, '~', false)
	pairStyle(tildes, attrs, inCode, attrStrike, 2)
}

type delimiterRun struct {
	start, length     int
	canOpen, canClose bool
}

// delimiterRuns collects the maximal runs of a delimiter outside code spans.
// A run only opens against text (no space after it) and only closes against
// text (no space before it); wordEdge also demands a word boundary outside the
// run, which is what keeps snake_case plain.
func delimiterRuns(line []rune, inCode []bool, r rune, wordEdge bool) []delimiterRun {
	var runs []delimiterRun
	for i := 0; i < len(line); {
		if line[i] != r || inCode[i] {
			i++
			continue
		}
		n := runLength(line, i, r)
		after := i + n
		run := delimiterRun{start: i, length: n}
		run.canOpen = after < len(line) && !unicode.IsSpace(line[after])
		run.canClose = i > 0 && !unicode.IsSpace(line[i-1])
		if wordEdge {
			run.canOpen = run.canOpen && (i == 0 || !isWordRune(line[i-1]))
			run.canClose = run.canClose && (after == len(line) || !isWordRune(line[after]))
		}
		runs = append(runs, run)
		i = after
	}
	return runs
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

// pairStyle matches openers to closers left to right among the runs that can
// carry the style: a run of the style's own width, or of three, which serves
// bold and italic at once (***both***). Unpaired delimiters stay plain.
func pairStyle(runs []delimiterRun, attrs []inlineAttr, inCode []bool, style inlineAttr, width int) {
	open := -1
	for idx, run := range runs {
		if run.length != width && run.length != 3 {
			continue
		}
		if open < 0 {
			if run.canOpen {
				open = idx
			}
			continue
		}
		if run.canClose {
			markPair(runs[open], run, attrs, inCode, style)
			open = -1
		}
	}
}

func markPair(opener, closer delimiterRun, attrs []inlineAttr, inCode []bool, style inlineAttr) {
	for k := opener.start; k < opener.start+opener.length; k++ {
		attrs[k] |= attrMarker
	}
	for k := closer.start; k < closer.start+closer.length; k++ {
		attrs[k] |= attrMarker
	}
	for k := opener.start + opener.length; k < closer.start; k++ {
		if !inCode[k] {
			attrs[k] |= style
		}
	}
}

func runLength(line []rune, at int, r rune) int {
	n := 0
	for at+n < len(line) && line[at+n] == r {
		n++
	}
	return n
}

// rebuildLine writes the line back with the spans styled. The textarea's own
// sequences pass through where they were; ours layer on top of them, re-asserted
// after any of theirs, and a span ends by resetting and replaying the SGR state
// the line had built up, so their styling is exactly restored.
func rebuildLine(visible []rune, prefixes [][]string, trailing []string, attrs []inlineAttr) string {
	var b strings.Builder
	var replay []string
	current := inlineAttr(0)
	for i, r := range visible {
		reassert := false
		for _, seq := range prefixes[i] {
			b.WriteString(seq)
			if isSGR(seq) {
				replay = sgrState(seq, replay)
				reassert = true
			}
		}
		switch {
		case attrs[i] == current:
			if reassert && current != 0 {
				b.WriteString(sgrFor(current))
			}
		case current == 0:
			b.WriteString(sgrFor(attrs[i]))
		default:
			b.WriteString(ansiReset)
			for _, seq := range replay {
				b.WriteString(seq)
			}
			if attrs[i] != 0 {
				b.WriteString(sgrFor(attrs[i]))
			}
		}
		current = attrs[i]
		b.WriteRune(r)
	}
	if current != 0 {
		b.WriteString(ansiReset)
		for _, seq := range replay {
			b.WriteString(seq)
		}
	}
	for _, seq := range trailing {
		b.WriteString(seq)
	}
	return b.String()
}

func isSGR(seq string) bool {
	return strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m")
}

// sgrState folds one SGR sequence into the state to replay: a reset empties it,
// anything else stacks on.
func sgrState(seq string, replay []string) []string {
	params := seq[2 : len(seq)-1]
	if params == "" || params == "0" {
		return replay[:0]
	}
	if strings.HasPrefix(params, "0;") {
		return append(replay[:0], seq)
	}
	return append(replay, seq)
}

// sgrFor styles a span the way internal/markdown styles the rendered message:
// slots 11 and 12 are the code and heading colors terminalStyle names there.
func sgrFor(a inlineAttr) string {
	var params []string
	if a&attrMarker != 0 {
		params = append(params, "2")
	}
	if a&(attrBold|attrHeading) != 0 {
		params = append(params, "1")
	}
	if a&attrItalic != 0 {
		params = append(params, "3")
	}
	if a&attrStrike != 0 {
		params = append(params, "9")
	}
	if a&attrCode != 0 {
		params = append(params, "38;5;11")
	} else if a&attrHeading != 0 {
		params = append(params, "38;5;12")
	}
	return "\x1b[" + strings.Join(params, ";") + "m"
}
