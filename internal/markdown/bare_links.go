package markdown

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// glamour finds a bare link — a URL, a www. address, an email address — with goldmark's
// linkify, which matches the Markdown source with its escapes still in it. ToMarkdown
// writes prose with every metacharacter backslash-escaped and every & as &amp;, which
// CommonMark reads back as the text the email held, so a bare URL in that prose is whole
// to a renderer that finds links in the text as CommonMark reads it. linkify stops at the
// first backslash instead, linking https://example.com/my for my\_plan, and copies &amp;
// into the destination it opens. Its pattern is ASCII, too, so a path with an é in it
// ends at the é, and it only starts a link after a space, *, _, ~ or ( — so the URL in
// "<https://…>", "Link:https://…" or a quotation, and the address in "Jane <jane@…>",
// were left to LinkifyURLs, which sees glamour's output after it has been wrapped and
// linked only the first line of a long one.
//
// Each bare link is therefore found here, in the text as CommonMark reads it, by the
// rules GFM's autolink extension sets (findBareLinks), and handed to glamour as an
// angle-bracket autolink, which it reads verbatim and wraps whole.

// proseParser is glamour's configuration without linkify, so that prose linkify would
// have cut into pieces arrives here as one run of text.
var proseParser = goldmark.New(goldmark.WithExtensions(
	extension.Table, extension.Strikethrough, extension.TaskList, extension.DefinitionList,
)).Parser()

var (
	// bareURL and bareWWW match a link from its start to as far as it could run: a
	// scheme in any case (or www.), a host with a dot in it, and a path. Next to the
	// ASCII GFM allows, the host and the path take non-ASCII other than spaces,
	// punctuation and controls — letters in any script, an emoji, a € — so a curly
	// quote, a dash or a no-break space still ends a link. A link also ends at "<", at
	// a straight double quote, at ">" and at "|", which would end a table cell; what is
	// left is trimmed by trimLinkEnd.
	bareURL = regexp.MustCompile(`^(?i:https?|ftp)://` + bareHost + `(?::\d+)?(?:[/#?]` + barePath + `)?`)
	bareWWW = regexp.MustCompile(`^(?i:www)\.` + bareHost + `(?:[/#?]` + barePath + `)?`)

	// bareEmail is an address as GFM finds one: a local part of letters, digits and
	// . + - _, an @, and a host of at least two labels.
	bareEmail = regexp.MustCompile(`[A-Za-z0-9.+_-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+`)
)

const (
	// otherLocalPart is what an address's local part may hold beyond what bareEmail
	// matches.
	otherLocalPart = "!#$%&'*/=?^`{|}~"

	// nonASCIIInLink is what a link may hold beyond ASCII: anything but spaces,
	// punctuation and controls, and the zero width joiner an emoji sequence is built
	// with. A path also takes the brackets of other scripts — （最終版） — which
	// trimLinkEnd balances as it balances ( and ).
	nonASCIIInLink = `[^\x00-\x7f\p{Z}\p{P}\p{C}]|\x{200D}`
	nonASCIIInPath = nonASCIIInLink + `|[（）［］｛｝「」『』【】〔〕〈〉《》]`
	bareHost       = `(?:[-a-zA-Z0-9@:%._\+~#=]|` + nonASCIIInLink + `){1,256}\.(?:[a-zA-Z]|\p{L})+`
	barePath       = `(?:[-a-zA-Z0-9@:%_+*.~#$!?&/=\(\);,'\^{}\[\]` + "`" + `]|` + nonASCIIInPath + `)*`
)

// replacement swaps the source between start and stop for text.
type replacement struct {
	start, stop int
	text        string
}

// wholeBareLinks returns md with every bare link in its prose written as an autolink.
func wholeBareLinks(md string) string {
	if !mayHoldLink(md) {
		return md
	}
	source := []byte(md)
	var replacements []replacement
	_ = ast.Walk(proseParser.Parse(text.NewReader(source)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.Link, *ast.Image, *ast.CodeSpan, *ast.AutoLink:
			// No link is found in a link's label, an image's alt text or code.
			return ast.WalkSkipChildren, nil
		}
		replacements = append(replacements, bareLinksUnder(source, n)...)
		return ast.WalkContinue, nil
	})
	if len(replacements) == 0 {
		return md
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })

	var b strings.Builder
	b.Grow(len(md) + 2*len(replacements))
	last := 0
	for _, r := range replacements {
		if r.start < last {
			continue
		}
		b.WriteString(md[last:r.start])
		b.WriteString(r.text)
		last = r.stop
	}
	b.WriteString(md[last:])
	return b.String()
}

// mayHoldLink is the cheap test for whether there is anything to find.
func mayHoldLink(s string) bool {
	if strings.Contains(s, "://") || strings.Contains(s, "@") {
		return true
	}
	// www. in any case — WwW.example.com is matched by bareWWW too.
	for i := 3; i < len(s); i++ {
		if s[i] == '.' && strings.EqualFold(s[i-3:i], "www") {
			return true
		}
	}
	return false
}

// bareLinksUnder finds the bare links in the prose directly under parent. Text sits in
// runs — goldmark splits it wherever an inline parser looked — and a run is the span of
// adjacent text nodes on one line.
func bareLinksUnder(source []byte, parent ast.Node) []replacement {
	var found []replacement
	start, stop := -1, -1
	flush := func() {
		if start >= 0 {
			found = append(found, bareLinksIn(source, start, stop)...)
		}
		start = -1
	}
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		t, ok := child.(*ast.Text)
		if !ok || t.IsRaw() {
			flush()
			continue
		}
		if start >= 0 && t.Segment.Start != stop {
			flush()
		}
		if start < 0 {
			start = t.Segment.Start
		}
		stop = t.Segment.Stop
		if t.SoftLineBreak() || t.HardLineBreak() {
			flush()
		}
	}
	flush()
	return found
}

// bareLinksIn finds the links in one run of prose, source[start:stop].
func bareLinksIn(source []byte, start, stop int) []replacement {
	run := source[start:stop]
	if !mayHoldLink(string(run)) {
		return nil
	}
	plain, at := unescapeProse(run)
	var found []replacement
	for _, link := range findBareLinks(string(plain)) {
		if target := percentEncodeNonASCII(link.target); autolinks(target) {
			found = append(found, replacement{start + at[link.start], start + at[link.end], "<" + target + ">"})
		}
	}
	return found
}

// The ANSI parser glamour and lipgloss share mistakes a UTF-8 continuation byte inside
// an escape sequence's parameters for a C1 control: the "最" in https://example.com/最終版
// comes out of glamour's hyperlink as a C1 control — and the containment check, rightly,
// then strips the whole body of its styling — and anything that measures or strips a
// line with such a hyperlink in it misreads the sequence. A hyperlink's destination is
// therefore kept ASCII: a link's non-ASCII is handed to glamour percent-encoded, which
// is the URI a browser opens anyway, and restoreNonASCIILinks decodes it again in the
// text glamour shows. The encoding is lowercase hex, which the RFC allows and almost
// nothing writes — and an escape the URL already had in lowercase is written in
// uppercase, the same address, so that what is decoded afterwards is only ever what
// was encoded here.
func percentEncodeNonASCII(s string) string {
	s = encodedNonASCII.ReplaceAllStringFunc(s, strings.ToUpper)
	if isASCII(s) {
		return s
	}
	const digits = "0123456789abcdef"
	var b strings.Builder
	b.Grow(len(s) * 3)
	for i := range len(s) {
		if c := s[i]; c >= utf8.RuneSelf {
			b.Write([]byte{'%', digits[c>>4], digits[c&0x0f]})
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// encodedNonASCII is a run of lowercase percent-encoded bytes at or above 0x80, as
// percentEncodeNonASCII writes them.
var encodedNonASCII = regexp.MustCompile(`(?:%[89a-f][0-9a-f])+`)

// restoreNonASCIILinks decodes, in the text of each hyperlink glamour wrote — never in
// its destination — what percentEncodeNonASCII encoded. A run is decoded only when it is
// whole UTF-8 of characters a link may show (shownInLink), so a line glamour wrapped in the
// middle of one stays encoded rather than turning into something else. glamour ends its
// hyperlink sequences with BEL; one that ends otherwise is left for contain to judge.
func restoreNonASCIILinks(out string) string {
	if !strings.Contains(out, "\x1b]8;") || !encodedNonASCII.MatchString(out) {
		return out
	}
	var b strings.Builder
	b.Grow(len(out))
	inLink := false
	for {
		start := strings.Index(out, "\x1b]8;")
		if start < 0 {
			break
		}
		end := strings.IndexByte(out[start:], '\a')
		if end < 0 {
			break
		}
		end += start + 1
		if inLink {
			b.WriteString(decodeNonASCII(out[:start]))
		} else {
			b.WriteString(out[:start])
		}
		sequence := out[start:end]
		b.WriteString(sequence)
		// A sequence with no destination — "\x1b]8;;\a" — closes the link.
		inLink = !strings.HasSuffix(sequence, ";\a")
		out = out[end:]
	}
	b.WriteString(out)
	return b.String()
}

func decodeNonASCII(s string) string {
	return encodedNonASCII.ReplaceAllStringFunc(s, func(run string) string {
		decoded := make([]byte, 0, len(run)/3)
		for i := 0; i+2 < len(run); i += 3 {
			decoded = append(decoded, unhex(run[i+1])<<4|unhex(run[i+2]))
		}
		if !utf8.Valid(decoded) {
			return run
		}
		for _, r := range string(decoded) {
			if !shownInLink(r) {
				return run
			}
		}
		return string(decoded)
	})
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func unhex(c byte) byte {
	if c >= 'a' {
		return c - 'a' + 10
	}
	return c - '0'
}

// shownInLink reports whether r, decoded, may be shown in a link: any non-ASCII but a
// space or a control — a format character such as a bidi override or a zero width
// space among them — except the zero width joiner an emoji sequence is built with.
func shownInLink(r rune) bool {
	return r >= utf8.RuneSelf && (r == '\u200d' || !unicode.In(r, unicode.Z, unicode.C))
}

// bareLink is a link found in plain text: the bytes it spans, and what it links to.
type bareLink struct {
	start, end int
	target     string
}

// findBareLinks finds the URLs, www. addresses and email addresses in plain text, in
// order. A URL or a www. address starts wherever a word could — anywhere not straight
// after a letter or a digit — and an email address is matched wherever an @ sits
// between a local part and a host, as GFM matches one, with a "mailto:" in front of it
// taken in. An address inside a URL is part of the URL.
func findBareLinks(s string) []bareLink {
	var links []bareLink
	for i := 0; i < len(s); {
		if link, ok := urlAt(s, i); ok {
			links = append(links, link)
			i = link.end
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	if !strings.Contains(s, "@") {
		return links
	}

	urls := links
	var addresses []bareLink
	for _, match := range bareEmail.FindAllStringIndex(s, -1) {
		start, end := match[0], match[1]
		// An address with more in its local part than GFM matches — devi*rao@ — is
		// left alone rather than linked as the different address rao@ would be.
		if start > 0 && strings.IndexByte(otherLocalPart, s[start-1]) >= 0 ||
			end < len(s) && (s[end] == '-' || s[end] == '_') || inside(urls, start) {
			continue
		}
		target := s[start:end]
		if strings.HasSuffix(strings.ToLower(s[:start]), "mailto:") {
			start -= len("mailto:")
			target = s[start:end]
		}
		addresses = append(addresses, bareLink{start, end, target})
	}
	// An address that starts before a URL holds it — jane@www.example.com is an
	// address at a www host — so the URL gives way to it.
	links = links[:0]
	for _, url := range urls {
		if !overlaps(addresses, url) {
			links = append(links, url)
		}
	}
	links = append(links, addresses...)
	sort.Slice(links, func(i, j int) bool { return links[i].start < links[j].start })
	return links
}

// urlAt matches a URL or a www. address starting at s[i].
func urlAt(s string, i int) (bareLink, bool) {
	switch s[i] {
	case 'h', 'H', 'f', 'F', 'w', 'W':
	default:
		return bareLink{}, false
	}
	// Only an ASCII letter or digit in front keeps a link from starting — xhttps:// is
	// not a scheme — since Chinese and Japanese put no space before one: 詳細はhttps://…
	if i > 0 && isAlphanumeric(s[i-1:i]) {
		return bareLink{}, false
	}
	if m := bareURL.FindString(s[i:]); m != "" {
		if url := trimLinkEnd(cutAtUnopenedBracket(m)); strings.Contains(url[strings.Index(url, "://")+3:], ".") {
			return bareLink{i, i + len(url), url}, true
		}
	}
	if m := bareWWW.FindString(s[i:]); m != "" {
		url := trimLinkEnd(cutAtUnopenedBracket(m))
		return bareLink{i, i + len(url), "http://" + url}, true
	}
	return bareLink{}, false
}

// cutAtUnopenedBracket ends a link at the first non-ASCII closing bracket it did not
// open — the pairs in openingBracket, which are all a path admits. Chinese and Japanese put no space after a link either, so the 」 that closes a
// quotation around one is followed by the sentence, which would otherwise run on into
// the link; a path's own （最終版） is opened in it and stays.
func cutAtUnopenedBracket(url string) string {
	open := map[string]int{}
	for i, r := range url {
		if r < utf8.RuneSelf {
			continue
		}
		if opener := openingBracket[r]; opener != "" {
			if open[opener] == 0 {
				return url[:i]
			}
			open[opener]--
		} else {
			open[string(r)]++
		}
	}
	return url
}

func overlaps(links []bareLink, span bareLink) bool {
	for _, link := range links {
		if link.start < span.end && span.start < link.end {
			return true
		}
	}
	return false
}

func inside(links []bareLink, at int) bool {
	for _, link := range links {
		if at >= link.start && at < link.end {
			return true
		}
	}
	return false
}

// trimLinkEnd takes off the end of a link what GFM leaves outside one: the punctuation
// a sentence puts after a link (? ! . , : * _ ~ ' " and a backtick), a closing bracket
// — ) or a full-width ） among them — with no opening one in the link, an entity-shaped "&name;", a ";", and punctuation
// outside ASCII — a closing curly quote, a guillemet, an ellipsis. It is the one rule
// for both passes, so a link ends in the same place whatever stands in front of it.
func trimLinkEnd(url string) string {
	for url != "" {
		last, size := utf8.DecodeLastRuneInString(url)
		switch {
		case strings.ContainsRune("?!.,:*_~'\"`", last):
			url = url[:len(url)-size]
		case last == ';':
			url = url[:len(url)-1]
			if amp := strings.LastIndexByte(url, '&'); amp >= 0 && amp < len(url)-1 && isAlphanumeric(url[amp+1:]) {
				url = url[:amp]
			}
		case openingBracket[last] != "":
			if strings.Count(url, openingBracket[last]) >= strings.Count(url, string(last)) {
				return url
			}
			url = url[:len(url)-size]
		case last >= utf8.RuneSelf && unicode.IsPunct(last):
			url = url[:len(url)-size]
		default:
			return url
		}
	}
	return url
}

// openingBracket pairs each closing bracket a link may end with to the one that opens
// it, in ASCII and in the full-width and CJK forms.
var openingBracket = map[rune]string{
	')': "(", ']': "[", '}': "{",
	'）': "（", '］': "［", '｝': "｛", '」': "「", '』': "『", '】': "【", '〕': "〔", '〉': "〈", '》': "《",
}

func isAlphanumeric(s string) bool {
	for i := range len(s) {
		if c := s[i]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// unescapeProse reads a run of prose as CommonMark would for the two escapes ToMarkdown
// writes in it: a backslash before ASCII punctuation, and &amp;. at maps each byte of
// the result back to where it began in the run, with one more entry for the run's end.
func unescapeProse(run []byte) (plain []byte, at []int) {
	plain = make([]byte, 0, len(run))
	at = make([]int, 0, len(run)+1)
	for i := 0; i < len(run); {
		at = append(at, i)
		switch {
		case run[i] == '\\' && i+1 < len(run) && util.IsPunct(run[i+1]):
			plain = append(plain, run[i+1])
			i += 2
		case bytes.HasPrefix(run[i:], []byte("&amp;")):
			plain = append(plain, '&')
			i += len("&amp;")
		default:
			plain = append(plain, run[i])
			i++
		}
	}
	return plain, append(at, len(run))
}

// autolinks reports whether "<target>" is an autolink to goldmark — an email address or
// a URL to its last byte — and stays one in a table row, where a | would end the cell.
func autolinks(target string) bool {
	if strings.Contains(target, "|") {
		return false
	}
	b := []byte(target)
	return util.FindEmailIndex(b) == len(b) || util.FindURLIndex(b) == len(b)
}
