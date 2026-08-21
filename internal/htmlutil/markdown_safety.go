package htmlutil

import (
	"regexp"
	"strings"
)

// Everything ToMarkdown writes came out of somebody else's email, and the Markdown it
// produces is parsed again downstream — by glamour on the way to a terminal, by whatever
// an agent renders `--json` bodies with. Each of the four places content can land has its
// own grammar, so each has its own serializer here, and the invariant every one of them
// keeps is the same: the Markdown parses back to the literal text or URL it was given,
// with no control character left in it. The text that reads "&#27;[31m" in an email stays
// those eight characters on screen rather than turning red.

// inlineMetacharacters are the ASCII characters that open Markdown syntax wherever they
// stand in a line. `&` is not among them because it is written as an entity instead.
const inlineMetacharacters = "\\`*_~[]<>|"

// lineStartMetacharacters can only open syntax at the start of a line: a heading, a
// quote, a list item, a setext underline, a thematic break.
const lineStartMetacharacters = "#>+-="

// entityReference is the shape of an entity or numeric character reference, which
// CommonMark decodes anywhere outside code — text, link destinations, info strings.
var entityReference = regexp.MustCompile(`^&(#[0-9]{1,8}|#[xX][0-9a-fA-F]{1,8}|[A-Za-z][A-Za-z0-9]{1,31});`)

// escapeText serializes a run of prose onto a line that already holds `line`. Controls go
// first, so that what is left is what gets looked at — "&\x1f#27;" is "&#27;" once the
// control is gone. Then every metacharacter is backslash-escaped, and at the start of a
// line so is anything that would begin a block.
//
// Every `&` becomes `&amp;`, which decodes back to `&` in CommonMark and in glamour
// alike. Escaping only the ampersands that already spell an entity is not enough: text
// arrives a node at a time, and "&#27" at the end of one node followed by ";" at the
// start of the next is an entity once they sit side by side. For the same reason the
// line-start checks look at the line being built rather than at the run alone.
func escapeText(s, line string) string {
	s = strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)

	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
			continue
		case strings.ContainsRune(inlineMetacharacters, r),
			line == "" && i == 0 && strings.ContainsRune(lineStartMetacharacters, r),
			(r == '.' || r == ')') && closesOrderedMarker(line, s[:i]):
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// closesOrderedMarker reports whether the line so far — what was already on it and the
// run up to here — is the digits of a list marker about to be closed by "." or ")".
// The length check comes before the two are joined: a marker is at most nine digits,
// and joining a long line for every "." in a long run would be quadratic.
func closesOrderedMarker(line, run string) bool {
	if len(line)+len(run) > 9 {
		return false
	}
	return endsOrderedMarker(line + run)
}

// endsOrderedMarker reports whether prefix is the digits of a list marker about to be
// closed by "." or ")": "1" in "1. first", or "2024" in "2024. was a year".
func endsOrderedMarker(prefix string) bool {
	if prefix == "" || len(prefix) > 9 {
		return false
	}
	for _, r := range prefix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// codeSpan serializes inline code. Backslashes mean nothing inside a code span, so the
// content is carried verbatim — a run of spaces included, since it can mean something in
// code — with only the line endings turned into the spaces CommonMark would make of
// them, and the delimiters are chosen to be a longer run of backticks than any the
// content holds. A renderer strips one space from each end of content that both begins
// and ends with one, so such content is padded by one more on each side, as is content
// that begins or ends with a backtick, which would otherwise run into the delimiter.
func codeSpan(content string) string {
	content = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(stripControls(content))
	if strings.TrimSpace(content) == "" {
		return ""
	}
	delimiter := strings.Repeat("`", longestRun(content, '`')+1)
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") ||
		(strings.HasPrefix(content, " ") && strings.HasSuffix(content, " ")) {
		content = " " + content + " "
	}
	return delimiter + content + delimiter
}

// codeFence is the fence that contains content: at least three backticks and, whatever
// run of them the content holds, one more than that.
func codeFence(content string) string {
	return strings.Repeat("`", max(3, longestRun(content, '`')+1))
}

// codeFenceInfo is the info string a fence may carry: a language name, nothing else. A
// backtick would close the fence and a space would start a second word, so anything
// outside a short list of word characters is dropped rather than repaired.
var codeFenceInfo = regexp.MustCompile(`^[A-Za-z0-9_+#.-]{1,32}$`)

func fenceInfo(language string) string {
	if codeFenceInfo.MatchString(language) {
		return language
	}
	return ""
}

// stripControls removes C0 and C1 controls and DEL, keeping the newline and tab that
// preformatted text carries on purpose.
func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !isControl(r) {
			return r
		}
		return -1
	}, s)
}

// isControl reports C0, DEL and C1. U+FFFD is none of those: a replacement character,
// whether the email carried it or an invalid byte became one, is a printable glyph.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func longestRun(s string, c byte) int {
	longest, run := 0, 0
	for i := range len(s) {
		if s[i] == c {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// destination serializes a link or image URL, reporting false when it may not be linked
// at all. Only http, https, mailto and relative references are linked: HEY's attachment
// URLs are relative paths, and everything else — javascript:, data:, file: — is a scheme
// a terminal or a browser acts on rather than one a reader follows.
//
// The characters that would end a Markdown destination early are percent-encoded, which
// a URL consumer decodes back. An `&` that happens to spell an entity reference, which
// CommonMark decodes inside a destination too, is written as `&amp;`, which decodes to
// the `&` that was there. Nothing else is touched: a query string's `&` stays a
// separator, so the URL an agent reads out of `--json` is the URL that was sent.
func destination(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, raw))
	if raw == "" || !allowedScheme(raw) {
		return "", false
	}

	var b strings.Builder
	b.Grow(len(raw))
	for i := range len(raw) {
		c := raw[i]
		switch {
		case c <= ' ' || strings.IndexByte(`<>()\"|`, c) >= 0:
			b.WriteString(percentEncoded(c))
		case c == '&' && entityReference.MatchString(raw[i:]):
			b.WriteString("&amp;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

func percentEncoded(c byte) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{'%', digits[c>>4], digits[c&0x0f]})
}

// destinationScheme is how a URL begins when it carries a scheme at all.
var destinationScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

func allowedScheme(raw string) bool {
	scheme := destinationScheme.FindString(raw)
	if scheme == "" {
		return true
	}
	switch strings.ToLower(scheme) {
	case "http:", "https:", "mailto:":
		return true
	default:
		return false
	}
}

// absolute reports whether a serialized destination can stand as an autolink, which
// needs a scheme: <https://example.com> is a link, </rails/blobs/chart.png> is raw HTML.
func absolute(dest string) bool {
	return destinationScheme.MatchString(dest)
}
