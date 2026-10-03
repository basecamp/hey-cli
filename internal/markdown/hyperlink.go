package markdown

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// reBareURL matches bare http/https URLs not already inside an OSC 8 sequence. A
// space ends one in any script: glamour pads inline code with no-break spaces, and a
// URL that ran on into them would open with them on the end. The scheme is matched in
// any case, and what follows is trimmed by trimLinkEnd, the rule bare links in prose
// end by too.
var reBareURL = regexp.MustCompile(`(?i:https?)://[^\s\p{Z}\x1b\x07<>"\x00-\x1f]+`)

// Hyperlink wraps text in an OSC 8 terminal hyperlink sequence, returning it
// unchanged when there is no URL to link to. The destination's non-ASCII is
// percent-encoded, for the reason percentEncodeNonASCII gives.
func Hyperlink(text, url string) string {
	url = percentEncodeNonASCII(sanitizeURL(url))
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
		// The URL ends where a bare link in prose would: at its run of text's end
		// (linkTokenEnd), then at what a link's end sheds.
		match := text[start:loc[1]]
		url := trimLinkEnd(match[:linkTokenEnd(match)])
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
