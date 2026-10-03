package markdown

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// reScheme is where a bare http or https URL starts, in any case.
var reScheme = regexp.MustCompile(`(?i:https?)://`)

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
// already sit inside one alone. A URL ends where a bare link in prose would — at the
// end of its run of text (linkTokenEnd: a space, a control, punctuation outside ASCII, a
// wide bracket it did not open), then at what a link's end sheds — and the search for
// the next one carries on from there, so https://…/one。https://…/two is two links. The
// run's end is found once and kept for the URLs after the first in it.
func LinkifyURLs(text string) string {
	var b strings.Builder
	last, run := 0, 0
	for at := 0; ; {
		loc := reScheme.FindStringIndex(text[at:])
		if loc == nil {
			break
		}
		start := at + loc[0]
		at += loc[1]
		if insideHyperlink(text[:start]) {
			continue
		}
		if start >= run {
			run = start + linkTokenEnd(text[start:])
		}
		url := trimLinkEnd(cutAtUnopenedBracket(text[start:run]))
		if len(url) <= loc[1]-loc[0] {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString(Hyperlink(url, url))
		last = start + len(url)
		at = last
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
