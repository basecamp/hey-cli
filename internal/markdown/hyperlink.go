package markdown

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// reBareURL matches bare http/https URLs not already inside an OSC 8 sequence.
// Trailing punctuation is trimmed by trimURL to preserve balanced parentheses.
var reBareURL = regexp.MustCompile(`https?://[^\s\x1b\x07<>"\x00-\x1f]+`)

// Hyperlink wraps text in an OSC 8 terminal hyperlink sequence, returning it
// unchanged when there is no URL to link to.
func Hyperlink(text, url string) string {
	url = sanitizeURL(url)
	if url == "" {
		return text
	}
	return ansi.SetHyperlink(url) + text + ansi.ResetHyperlink()
}

// LinkifyURLs wraps bare URLs in OSC 8 hyperlink sequences, leaving URLs that
// already sit inside one alone.
func LinkifyURLs(text string) string {
	var b strings.Builder
	last := 0
	for _, loc := range reBareURL.FindAllStringIndex(text, -1) {
		start := loc[0]
		if insideHyperlink(text[:start]) {
			continue
		}
		url := trimURL(text[start:loc[1]])
		if url == "" {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString(Hyperlink(url, url))
		last = start + len(url)
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// sanitizeURL strips terminal control characters from a URL to prevent OSC 8
// sequence injection. BEL (\x07) would terminate the sequence early, ESC
// (\x1b) could start new escape sequences, and the 8-bit ST (\x9c) can
// terminate OSC in some terminals. All C0, C1, and DEL characters have no
// place in a URL.
func sanitizeURL(url string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, url)
}

// trimURL trims trailing punctuation from a URL match while preserving
// balanced parentheses (e.g., Wikipedia URLs).
func trimURL(url string) string {
	for len(url) > 0 {
		switch url[len(url)-1] {
		case '.', ',', ';', ':', '!', '?', '\'', ']', '`':
			url = url[:len(url)-1]
		case ')':
			if strings.Count(url, "(") >= strings.Count(url, ")") {
				return url
			}
			url = url[:len(url)-1]
		default:
			return url
		}
	}
	return url
}

// insideHyperlink reports whether the text following prefix is part of an
// existing OSC 8 hyperlink — either as the URI parameter or as the visible
// text between set and reset.
func insideHyperlink(prefix string) bool {
	if strings.HasSuffix(prefix, "\x1b]8;;") {
		return true
	}

	set := strings.LastIndex(prefix, "\x1b]8;")
	if set == -1 {
		return false
	}

	bell := strings.IndexByte(prefix[set:], '\x07')
	if bell == -1 {
		return true
	}

	reset := "\x1b]8;;\x07"
	if prefix[set:set+bell+1] == reset {
		return false
	}
	return !strings.Contains(prefix[set+bell+1:], reset)
}
