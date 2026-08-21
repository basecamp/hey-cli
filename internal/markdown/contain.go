package markdown

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// What leaves Render is meant to carry exactly two kinds of escape sequence: SGR, for
// the styling, and OSC 8, for the hyperlinks. contain checks that it does. Anything
// else — a window title, a cursor movement, a device control string, a stray escape
// byte, a C1 control — means the source got something past prepareSource, and the
// honest answer is to strip every sequence from the output and lose the styling rather
// than guess which ones were ours.
//
// This is a last resort, not the guarantee. An injected SGR is byte-for-byte one of
// ours, so a sequence allow-list cannot tell them apart; what keeps an email from
// coloring the terminal is the escaping in htmlutil and the source rewrite in this
// package, both of which act before glamour. contain is here for the sequence classes
// those would never emit.
//
// A hyperlink is kept only for a destination a terminal should open: http, https or
// mailto. Anything else loses its OSC 8 and stays visible text.

func contain(out string) string {
	contained, ok := allowSequences(out)
	if !ok {
		return stripAll(out)
	}
	return contained
}

func allowSequences(out string) (string, bool) {
	var b strings.Builder
	b.Grow(len(out))
	for i := 0; i < len(out); {
		c := out[i]
		switch {
		case c == 0x1b:
			sequence, keep, ok := escapeSequence(out[i:])
			if !ok {
				return "", false
			}
			if keep {
				b.WriteString(sequence)
			}
			i += len(sequence)
		case c == '\n' || c == '\t':
			b.WriteByte(c)
			i++
		case c < 0x20 || c == 0x7f:
			return "", false
		case c == 0xc2 && i+1 < len(out) && out[i+1] >= 0x80 && out[i+1] <= 0x9f:
			return "", false
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), true
}

// escapeSequence reads the sequence at the start of s, which begins with ESC. It reports
// the sequence, whether the output keeps it, and whether it was one of the two allowed.
func escapeSequence(s string) (sequence string, keep, ok bool) {
	switch {
	case strings.HasPrefix(s, "\x1b["):
		return sgr(s)
	case strings.HasPrefix(s, "\x1b]8;"):
		return hyperlink(s)
	default:
		return "", false, false
	}
}

// sgr accepts a Select Graphic Rendition: CSI, parameter bytes, and a final `m`.
func sgr(s string) (string, bool, bool) {
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c == 'm':
			return s[:i+1], true, true
		case (c >= '0' && c <= '9') || c == ';' || c == ':':
		default:
			return "", false, false
		}
	}
	return "", false, false
}

// hyperlink accepts an OSC 8 open or reset, terminated by BEL or ST, and keeps the open
// only for a destination with an allowed scheme.
func hyperlink(s string) (string, bool, bool) {
	end, terminator := hyperlinkEnd(s)
	if end < 0 {
		return "", false, false
	}
	sequence := s[:end+terminator]
	body := s[len("\x1b]8;"):end]
	params, uri, found := strings.Cut(body, ";")
	if !found || strings.ContainsFunc(params+uri, isControl) {
		return "", false, false
	}
	return sequence, uri == "" || allowedHyperlink(uri), true
}

func hyperlinkEnd(s string) (end, terminator int) {
	for i := len("\x1b]8;"); i < len(s); i++ {
		switch s[i] {
		case 0x07:
			return i, 1
		case 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i, 2
			}
			return -1, 0
		}
	}
	return -1, 0
}

func allowedHyperlink(uri string) bool {
	lower := strings.ToLower(uri)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:")
}

// stripAll removes every escape sequence and control character, newlines and tabs
// excepted, from text that contain could not vouch for.
func stripAll(out string) string {
	return stripControls(ansi.Strip(out))
}
