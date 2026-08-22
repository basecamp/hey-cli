// Package terminal makes text that came from somewhere else safe to print.
//
// Everything HEY serves — a sender's name, a subject, a filename, a label — was
// written by somebody else, and a terminal acts on the escape sequences and
// control characters it is handed. Stripping the sequence outright is what makes
// the string inert. Defacing its ESC byte only hides the trigger: the payload
// stays behind as visible debris of somebody else's choosing, and whatever lays
// the output out then measures that debris as text.
//
// The bidirectional controls go the same way. They do not move the cursor, but
// they move what the reader sees: a right-to-left override (U+202E) between
// "invoice" and "fdp.exe" shows a PDF on screen and an executable on disk, and an
// isolate can swap the order of a sender's name and address. Nothing HEY shows in a single line needs
// them — an RTL name still reads right-to-left without an explicit override.
//
// # Confusables
//
// Past the controls lies text that reads as one thing and is another. The policy,
// class by class:
//
// Format characters that draw nothing are stripped: the soft hyphen, the zero
// width space, the word joiner, the byte order mark, the combining grapheme joiner,
// the Mongolian vowel separator, the invisible mathematical operators and the
// deprecated format controls next to them. A zero width space in "paypal" or a soft
// hyphen in "invoicepdf.exe" renders as the honest spelling while being a different
// string, which is the same class as a bidi override. They are stripped rather than
// replaced, since a replacement mark is visible debris in a name column. The list is
// enumerated; it is not the Cf category wholesale, which also holds the joiners
// below, the Arabic number and verse signs that are visible, and the tag characters
// a subdivision flag is made of.
//
// The zero width joiner and non-joiner are kept where they can join. They carry
// text: an emoji family is emoji joined by U+200D, and Persian, Urdu and the Indic
// scripts write with both. A joiner survives only between two non-ASCII,
// non-space base characters — the marks on the left base, a virama or an emoji
// presentation selector, are part of it, but a mark is not a base on its own — and a
// run of them collapses to one; at the start or end of a string, or next to an
// ASCII letter — a joiner inside "paypal" — it joins nothing and is dropped.
//
// Combining marks are kept up to eight on a base and the rest of the run dropped.
// The deepest stacks a writing system produces reach five — a fully pointed Hebrew
// letter with its shin dot, dagesh, vowel, meteg and cantillation; a Tibetan stack
// with two subjoined letters and a vowel sign is four — where decomposed Vietnamese,
// Hindi and the keycap emoji use two, so eight is room for any of them and Zalgo
// needs dozens to climb out of its cell. Variation selectors are marks and sit
// under the same cap, so the U+FE0F that makes a heart red stays. A format
// character that is kept — a tag character, an Arabic number sign — is not a base:
// it draws nothing of its own, so the marks after it still belong to the letter
// before it and count against the same cap.
//
// A byte that is not UTF-8 is dropped. Left as it is, a stray 0x85 is a C1 control
// to a terminal that reads bytes; and it goes the way everything else here goes,
// stripped rather than replaced, so the output is never longer than the input.
//
// Spaces that are not U+0020 are left alone. A no-break space is ordinary in text
// that came out of HTML, as are the fixed-width spaces; they render as a space and
// read as one, and normalizing them would rewrite names for no safety gain.
//
// Homoglyphs — a Cyrillic "а" in a Latin name — are not detected. Without
// confusable tables and a script heuristic the check cannot tell a forged name from
// a multilingual one, and the false positives land on real people. What a link's
// label says against where it goes is the Markdown serializer's business: a label
// that reads as a URL is written beside its destination rather than collapsed into
// it (see htmlutil.ToMarkdown).
package terminal

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

var lineBreaks = strings.NewReplacer("\n", " ", "\t", " ")

// maxCombiningMarks is how many combining marks may follow one base before the
// rest of the run is dropped: the package doc says why eight.
const maxCombiningMarks = 8

const (
	zeroWidthNonJoiner = 0x200c
	zeroWidthJoiner    = 0x200d
)

// Sanitize removes escape sequences, control characters, bidirectional controls
// and the confusables described in the package doc from text on its way to a
// terminal. Newlines and tabs survive, because text is not necessarily one line: a
// message body, a jq result and a Markdown cell all carry them on purpose.
//
// One pass, and no allocation for text that needs nothing removed. Every decision
// is made on what has been kept, so sanitizing twice is the same as once.
func Sanitize(value string) string {
	p := pass{in: ansi.Strip(value), kept: -1, base: -1}
	for i, r := range p.in {
		switch {
		case r == '\n' || r == '\t':
			p.keep(r)
			p.marks = 0
		case r == utf8.RuneError && !strings.HasPrefix(p.in[i:], "\ufffd"):
			// An invalid byte, not a replacement character the text carried.
			p.drop(i)
		case isControl(r), isBidiControl(r), isInvisible(r):
			p.drop(i)
		case r == zeroWidthJoiner || r == zeroWidthNonJoiner:
			p.hold(i, r)
		case isCombiningMark(r):
			if p.marks < maxCombiningMarks {
				p.keep(r)
				p.marks++
			} else {
				p.drop(i)
			}
		default:
			p.keep(r)
			if !unicode.Is(unicode.Cf, r) {
				p.marks = 0
			}
		}
	}
	return p.String()
}

// pass is one walk over the input. The output is only built once something is
// dropped; until then the input is its own output. A joiner is held back until the
// rune after it is kept, and written only when that rune is something it can join.
type pass struct {
	in       string
	out      *strings.Builder
	kept     rune // the last rune written, other than a joiner
	base     rune // the last rune written that is not a mark, other than a joiner
	marks    int  // combining marks written since the last base
	joiner   rune // a joiner waiting for its right-hand side, or zero
	joinerAt int  // its byte offset
}

func (p *pass) keep(r rune) {
	if p.joiner != 0 {
		if isBase(r) {
			p.write(p.joiner)
		} else {
			p.diverge(p.joinerAt)
		}
		p.joiner = 0
	}
	p.write(r)
	p.kept = r
	if !isCombiningMark(r) {
		p.base = r
	}
}

// isBase reports a rune a joiner can join: non-ASCII text that is neither a space, a
// mark, a format character nor punctuation. The marks on a base ride along with it; a
// tag or any other format character ends it, so a joiner after one has nothing to join;
// and a dash or a quotation mark is not something letters join to. Non-ASCII digits stay
// bases on purpose: Persian writes a ZWNJ between a number and its suffix — "۱۰‌ها" —
// so a digit is something a joiner legitimately rides against, and dropping it there
// would rewrite the word (the Unicode core spec's chapter on joining controls makes the
// same point about removing them blindly).
func isBase(r rune) bool {
	return joinable(r) && !isCombiningMark(r) && !unicode.Is(unicode.Cf, r) && !unicode.IsPunct(r)
}

func (p *pass) drop(i int) {
	if p.joiner != 0 {
		i = p.joinerAt
	}
	p.diverge(i)
}

// hold keeps a joiner back until keep decides it. A joiner with no base on its
// left, or one held while another is pending, is dropped.
func (p *pass) hold(i int, r rune) {
	switch {
	case p.joiner != 0 || !isBase(p.base):
		p.drop(i)
	default:
		p.joiner, p.joinerAt = r, i
	}
}

func (p *pass) write(r rune) {
	if p.out != nil {
		p.out.WriteRune(r)
	}
}

// diverge starts the output at the point the first dropped rune would have gone,
// which is i or the joiner pending before it.
func (p *pass) diverge(i int) {
	if p.out == nil {
		p.out = &strings.Builder{}
		p.out.Grow(len(p.in))
		p.out.WriteString(p.in[:i])
	}
}

func (p *pass) String() string {
	if p.joiner != 0 {
		p.diverge(p.joinerAt)
	}
	if p.out == nil {
		return p.in
	}
	return p.out.String()
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// isBidiControl reports the Unicode Bidi_Control property: the Arabic letter mark,
// the left-to-right and right-to-left marks, the embeddings, overrides and their
// pop, and the isolates and theirs.
func isBidiControl(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}

// isInvisible reports the format characters that draw nothing and carry no text:
// the soft hyphen, the combining grapheme joiner, the Mongolian vowel separator,
// the zero width space, the word joiner and the invisible operators after it, the
// deprecated format controls, and the byte order mark.
func isInvisible(r rune) bool {
	switch {
	case r == 0x00ad, r == 0x034f, r == 0x180e, r == 0x200b, r == 0xfeff:
		return true
	case r >= 0x2060 && r <= 0x2064:
		return true
	case r >= 0x206a && r <= 0x206f:
		return true
	default:
		return false
	}
}

// joinable reports whether r is something a joiner next to it can join: non-ASCII
// text that is not a space. Whether it is a base rather than a mark is the caller's
// question.
func joinable(r rune) bool {
	return r >= utf8.RuneSelf && !unicode.IsSpace(r)
}

func isCombiningMark(r rune) bool {
	return r >= 0x300 && unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
}

// SanitizeLine is Sanitize for somewhere only one line fits — a table cell, a
// confirmation — where a newline or a tab would move what comes after it rather
// than merely reading oddly. Both become a space, so the words stay apart.
func SanitizeLine(value string) string {
	return lineBreaks.Replace(Sanitize(value))
}
