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
	links := newLinkState(text)
	for at := 0; ; {
		loc := reScheme.FindStringIndex(text[at:])
		if loc == nil {
			break
		}
		start := at + loc[0]
		at += loc[1]
		if links.inside(start) {
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

// linkState follows the OSC 8 hyperlinks in a text as a scan moves forward through it,
// so whether a position sits inside one is answered without reading the text before it
// again: each sequence is read once.
type linkState struct {
	text string
	next int  // where the next sequence not yet read starts, or len(text)
	open bool // whether the sequences read so far leave a hyperlink open
}

func newLinkState(text string) *linkState {
	return &linkState{text: text, next: nextHyperlink(text, 0)}
}

// inside reports whether pos sits inside a hyperlink — in its text, or in a sequence's
// parameters — given that no later call asks about an earlier position.
func (l *linkState) inside(pos int) bool {
	for l.next < pos {
		// hyperlinkEnd stops at the sequence's own terminator, BEL or ST, so each
		// sequence is read once; one with none is taken to run on.
		end, terminator := hyperlinkEnd(l.text[l.next:])
		if end < 0 || pos < l.next+end+terminator {
			return true
		}
		_, uri, _ := strings.Cut(l.text[l.next+len("\x1b]8;"):l.next+end], ";")
		l.open = uri != ""
		l.next = nextHyperlink(l.text, l.next+end+terminator)
	}
	return l.open
}

func nextHyperlink(text string, from int) int {
	if next := strings.Index(text[from:], "\x1b]8;"); next >= 0 {
		return from + next
	}
	return len(text)
}
