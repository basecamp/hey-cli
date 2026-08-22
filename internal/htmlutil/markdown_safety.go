package htmlutil

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// Everything ToMarkdown writes came out of somebody else's email, and the Markdown it
// produces is parsed again downstream — by glamour on the way to a terminal, by whatever
// an agent renders `--json` bodies with. Each of the four places content can land has its
// own grammar, so each has its own serializer here, and the invariant every one of them
// keeps is the same: the Markdown parses back to the literal text or URL it was given,
// with no control character left in it. The text that reads "&#27;[31m" in an email stays
// those eight characters on screen rather than turning red. Prose is the one context
// written in its sanitized form — terminal.Sanitize runs over it before anything is
// escaped — because markdown.Render runs that sanitizer over the whole body before
// parsing it, and an escape decided on the text as written can be undone by what the
// sanitizer removes in front of it. Code and destinations are written as they are and
// only measured through the sanitizer; the HTML keeps the original of everything.

// inlineMetacharacters are the ASCII characters that open Markdown syntax wherever they
// stand in a line. `&` is not among them because it is written as an entity instead.
const inlineMetacharacters = "\\`*_~[]<>|"

// lineStartMetacharacters can only open syntax at the start of a line: a heading, a
// quote, a list item, a setext underline, a thematic break.
const lineStartMetacharacters = "#>+-="

// entityReference is the shape of an entity or numeric character reference, which
// CommonMark decodes anywhere outside code — text, link destinations, info strings.
var entityReference = regexp.MustCompile(`^&(#[0-9]{1,8}|#[xX][0-9a-fA-F]{1,8}|[A-Za-z][A-Za-z0-9]{1,31});`)

// escapeText serializes a run of prose onto a line that already holds `line`. The
// sanitizer goes first, so that what is left is what gets looked at — "&\x1f#27;" is
// "&#27;" once the control is gone, and "\u200b# heading" is "# heading" once the zero
// width space is. markdown.Render runs the same sanitizer over the whole body before
// parsing it, so prose is written here in the form it will be parsed in; escaping the
// text as written and letting the renderer strip an invisible in front of a block marker
// would hand glamour a heading the email never had. Then every metacharacter is
// backslash-escaped, and at the start of a line so is anything that would begin a block.
//
// Every `&` becomes `&amp;`, which decodes back to `&` in CommonMark and in glamour
// alike. Escaping only the ampersands that already spell an entity is not enough: text
// arrives a node at a time, and "&#27" at the end of one node followed by ";" at the
// start of the next is an entity once they sit side by side. For the same reason the
// line-start checks look at the line being built rather than at the run alone.
func escapeText(s, line string) string {
	s = sanitizeProse(s, line)

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

// sanitizeProse runs terminal.Sanitize over a run of prose the way the renderer will: in
// the context of the line it joins. Text arrives a node at a time, and HTML can split a
// joining sequence or a stack of marks across two of them — "👨<span>\u200d👩</span>"
// — where a run sanitized on its own would lose the joiner for want of a base on its
// left, and a ninth mark would pass for a first. The context is the last base on the
// line and what rides on it (lineContext), which is all the sanitizer's decisions look
// back at. A joiner a run ends with is kept: what follows is not known yet, the next run
// is judged with it in its context, and one left dangling at the end of a line is
// trimmed there (flushLine), as the renderer would trim it anyway.
func sanitizeProse(s, line string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return -1
		}
		return r
	}, s)
	context := lineContext(line)
	whole := terminal.Sanitize(context + s)
	var tail string
	switch head := terminal.Sanitize(context); {
	case strings.HasPrefix(whole, context):
		// The context as written, a joiner kept at its end included, stands.
		tail = whole[len(context):]
	case strings.HasPrefix(whole, head):
		// A joiner kept at the end of the line found nothing to join; it stays on
		// the line as the renderer's pass will see and drop it.
		tail = whole[len(head):]
	default:
		return terminal.Sanitize(s)
	}
	// A trailing joiner that went for want of a right-hand side is kept for the next
	// run, but only one a base would have taken: the probe asks the sanitizer itself.
	if last, _ := utf8.DecodeLastRuneInString(s); isJoiner(last) && !strings.HasSuffix(tail, string(last)) &&
		strings.HasSuffix(terminal.Sanitize(context+tail+string(last)+"é"), string(last)+"é") {
		tail += string(last)
	}
	return recollapse(tail, line)
}

// recollapse folds the whitespace the sanitizer uncovered — "\u200b # heading" is
// " # heading" once the zero width space is gone — the way writeText folded the run
// before it: runs of whitespace to one space, none at the start of a line, so that a
// block marker cannot hide behind a space the line-start escape does not look past, and
// four of them cannot open an indented code block. A space at either edge of the run
// stays one space between it and its neighbours, as writeSpace would have left it.
func recollapse(s, line string) string {
	folded := strings.Join(strings.Fields(s), " ")
	if folded == "" {
		return ""
	}
	if startsWithSpace(s) && line != "" && !strings.HasSuffix(line, " ") {
		folded = " " + folded
	}
	if endsWithSpace(s) {
		folded += " "
	}
	return folded
}

// lineContext is the end of a line that the sanitizer's decisions about what follows
// look back at: the last rune that is neither a mark nor a joiner, and everything after
// it. It is bounded, since a line can be long and the context cannot be — the sanitizer
// keeps at most a handful of marks on a base.
func lineContext(line string) string {
	const maxContextRunes = 32
	end := len(line)
	for i := 0; i < maxContextRunes && end > 0; i++ {
		r, size := utf8.DecodeLastRuneInString(line[:end])
		end -= size
		if !isCombiningMark(r) && !isJoiner(r) {
			break
		}
	}
	return line[end:]
}

func isJoiner(r rune) bool { return r == 0x200c || r == 0x200d }

func isCombiningMark(r rune) bool {
	return r >= 0x300 && unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
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
	delimiter := strings.Repeat("`", backtickRun(content)+1)
	if needsPadding(content) || needsPadding(terminal.Sanitize(content)) {
		content = " " + content + " "
	}
	return delimiter + content + delimiter
}

// needsPadding reports code-span content that would run into its delimiters: a backtick
// at either end, or a space at both. It is asked of the content as written and as the
// renderer's sanitizer leaves it, since a stripped character before a leading backtick
// would otherwise bring that backtick up against the delimiter on screen.
func needsPadding(content string) bool {
	return strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") ||
		(strings.HasPrefix(content, " ") && strings.HasSuffix(content, " "))
}

// codeFence is the fence that contains content: at least three backticks and, whatever
// run of them the content holds, one more than that.
func codeFence(content string) string {
	return strings.Repeat("`", max(3, backtickRun(content)+1))
}

// backtickRun is the longest run of backticks in code content as a terminal will see
// it. markdown.Render puts the whole document through terminal.Sanitize before
// parsing it, and that drops characters that draw nothing, so two runs with a zero
// width space between them are one run by the time the delimiters are read; a
// delimiter sized on the content as written would then be closed from inside. The
// content itself is written as it is — the JSON keeps it — and only the measure
// looks through the sanitizer.
func backtickRun(content string) int {
	return max(longestRun(content, '`'), longestRun(terminal.Sanitize(content), '`'))
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
