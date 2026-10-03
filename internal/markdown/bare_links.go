package markdown

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
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
// have cut into pieces arrives here as one run of text. ToMarkdown escapes every tilde
// in prose, so a ~ this parse sees unescaped is strikethrough, and a URL written up to
// one — https://example.com/a~~struck~~ — ends where the struck text starts.
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
	// left is trimmed by trimLinkEnd. A port may be empty — https://example.com:/x — as
	// the URL standard allows.
	bareURL = regexp.MustCompile(`^((?i:https?|ftp)://` + bareHost + `(?::\d*)?)((?:[/#?]` + barePath + `)?)`)
	bareWWW = regexp.MustCompile(`^((?i:www)\.` + bareHost + `(?::\d*)?)((?:[/#?]` + barePath + `)?)`)

	// mailtoURI is a mailto: link with its recipients and its query.
	mailtoURI = regexp.MustCompile(`^(?i:mailto):` + emailAddress + `(?:,` + emailAddress + `)*(?:\?` + barePath + `)?`)

	// bareEmail is an address as GFM finds one: a local part of letters, digits and
	// . + - _, an @, and a host of at least two labels.
	bareEmail = regexp.MustCompile(emailAddress)
)

const (
	emailAddress = `[A-Za-z0-9.+_-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+`

	// otherLocalPart is what an address's local part may hold beyond what bareEmail
	// matches.
	otherLocalPart = "!#$%&'*/=?^`{|}~"

	// nonASCIIInLink is what a link may hold beyond ASCII: anything but spaces,
	// punctuation and controls, and the zero width joiner and non-joiner the sanitizer
	// keeps — an emoji sequence is built with one, a Persian word with the other. A path also takes the brackets of other scripts — （最終版） — which
	// trimLinkEnd balances as it balances ( and ).
	nonASCIIInLink = `[^\x00-\x7f\p{Z}\p{P}\p{C}]|[\x{200C}\x{200D}]`
	nonASCIIInPath = nonASCIIInLink + `|[` + wideOpeners + wideClosers + `]`
	// bareHost is a name ending in a top-level domain of letters or punycode, and the
	// DNS root's dot if it has one, or an IPv4 address — tried in that order, so
	// 192.0.2.1.example.com is a name. A sentence's full stop after a host is taken in
	// and shed again by trimLinkEnd.
	bareHost = `(?:(?:[-a-zA-Z0-9@:%._\+~#=]|` + nonASCIIInLink + `){1,256}\.(?:(?i:xn--)[a-zA-Z0-9-]+|(?:[a-zA-Z]|\p{L})+)\.?|(?:` + octet + `\.){3}` + octet + `)`

	// octet is one number of an IPv4 address, 0 to 255.
	octet    = `(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)`
	barePath = `(?:[-a-zA-Z0-9@:%_+*.~#$!?&/=\(\);,'\^{}\[\]|` + "`" + `]|` + nonASCIIInPath + `)*`
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
	// A | — which a path may hold — would end a table cell, so percentEncodeNonASCII
	// writes it as the escape restoreNonASCIILinks shows as | again.
	var found []replacement
	links, declined := findBareLinks(string(plain))
	for _, link := range links {
		if target := percentEncodeNonASCII(link.target); autolinks(target) {

			found = append(found, replacement{start + at[link.start], start + at[link.end], "<" + target + ">"})
		}
	}
	// glamour's linkify would link the part of a declined address it can match, so its
	// @ is escaped; textAmpersands then spells it as the reference glamour shows as @.
	for _, sign := range declined {
		found = append(found, replacement{start + at[sign], start + at[sign+1], `\@`})
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
// was encoded here. A | is encoded the same way: a bare link's destination is written
// into a table cell as it is, where a raw one would end the cell.
func percentEncodeNonASCII(s string) string {
	s = encodedNonASCII.ReplaceAllStringFunc(s, strings.ToUpper)
	if isASCII(s) && !strings.Contains(s, "|") {
		return s
	}
	const digits = "0123456789abcdef"
	var b strings.Builder
	b.Grow(len(s) * 3)
	for i := range len(s) {
		if c := s[i]; c >= utf8.RuneSelf || c == '|' {
			b.Write([]byte{'%', digits[c>>4], digits[c&0x0f]})
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// encodedNonASCII is a run of lowercase percent-encoded bytes at or above 0x80, and of
// %7c for a |, as percentEncodeNonASCII writes them.
var encodedNonASCII = regexp.MustCompile(`(?:%[89a-f][0-9a-f]|%7c)+`)

// restoreNonASCIILinks decodes, in the text of each hyperlink glamour wrote — never in
// its destination — what percentEncodeNonASCII encoded. Only the destination glamour
// prints is touched — underlined, and part of the URI — so a label the email wrote is
// the email's, "%c3%a9" and all, even on a link to /é. A run is decoded only when it is
// whole UTF-8 of characters a link may show (shownInLink). glamour ends its hyperlink
// sequences with BEL; one that ends otherwise is left for contain to judge.
//
// glamour wraps a long destination wherever a line ends, which can be the middle of a
// character's escapes, so the lines of one link are decoded together and each character
// goes back on the line that held most of its escapes: a character is at most two cells
// and its escapes at least six, so no line grows past the width glamour wrapped it to.
func restoreNonASCIILinks(out string) string {
	if !strings.Contains(out, "\x1b]8;") || !encodedNonASCII.MatchString(out) {
		return out
	}
	var b strings.Builder
	b.Grow(len(out))
	last := 0
	for _, link := range printedLinks(out) {
		// A line of a link can carry its styling inside the hyperlink — glamour opens a
		// wrapped line's hyperlink before its underline — so only the text between the
		// styling is decoded.
		texts := make([]string, len(link))
		for i, span := range link {
			_, texts[i], _ = splitStyling(out[span.start:span.end])
		}
		joined := strings.Join(texts, "")
		if strings.ContainsRune(joined, '\x1b') || !strings.Contains(link[0].uri, withoutWhitespace(joined)) {
			continue
		}
		for i, text := range decodeAcross(texts) {
			lead, _, trail := splitStyling(out[link[i].start:link[i].end])
			b.WriteString(out[last:link[i].start])
			b.WriteString(lead + text + trail)
			last = link[i].end
		}
	}
	b.WriteString(out[last:])
	return b.String()
}

// leadingStyling and trailingStyling are the SGR sequences at either end of a line of a
// link.
var (
	leadingStyling  = regexp.MustCompile(`^(?:\x1b\[[0-9;:]*m)+`)
	trailingStyling = regexp.MustCompile(`(?:\x1b\[[0-9;:]*m)+$`)
)

// splitStyling separates a line of a link into the styling before its text, the text,
// and the styling after it.
func splitStyling(s string) (lead, text, trail string) {
	lead = leadingStyling.FindString(s)
	s = s[len(lead):]
	trail = trailingStyling.FindString(s)
	return lead, s[:len(s)-len(trail)], trail
}

// linkSpan is the text of one line of a hyperlink: out[start:end], linking to uri.
type linkSpan struct {
	start, end int
	uri        string
}

// printedLinks finds the destinations glamour printed, each as the spans of its lines:
// an underlined hyperlink, and the next ones to the same destination that follow it
// across nothing but a line break and styling, until their text spells the whole
// destination — the same link printed twice in a row is two links.
func printedLinks(out string) [][]linkSpan {
	var links [][]linkSpan
	current := make([]linkSpan, 0, 1)
	printed := "" // the text of current so far, without whitespace
	closedAt := -1
	for at := 0; ; {
		open := strings.Index(out[at:], "\x1b]8;")
		if open < 0 {
			break
		}
		open += at
		end := strings.IndexByte(out[open:], '\a')
		if end < 0 {
			break
		}
		end += open + 1
		_, uri, _ := strings.Cut(out[open+len("\x1b]8;"):end-1], ";")
		if uri == "" {
			closedAt = end
			at = end
			continue
		}
		text := strings.Index(out[end:], "\x1b]8;")
		if text < 0 {
			break
		}
		span := linkSpan{end, end + text, uri}
		lead, shown, _ := splitStyling(out[span.start:span.end])
		underlined := precededByUnderline(out, open) || lead != "" && precededByUnderline(out, span.start+len(lead))
		continues := len(current) > 0 && current[0].uri == uri && printed != uri && closedAt >= 0 &&
			strings.TrimSpace(ansi.Strip(out[closedAt:open])) == ""
		switch {
		case !underlined:
			if len(current) > 0 {
				links = append(links, current)
			}
			current = nil
		case continues:
			current = append(current, span)
			printed += withoutWhitespace(shown)
		default:
			if len(current) > 0 {
				links = append(links, current)
			}
			current = []linkSpan{span}
			printed = withoutWhitespace(shown)
		}
		closedAt = -1
		at = span.end
	}
	if len(current) > 0 {
		links = append(links, current)
	}
	return links
}

// decodeAcross decodes the lines of one link as one text, and gives each character back
// to the line that held most of its escapes — a character as a reader sees one, accents
// and joined emoji included.
func decodeAcross(texts []string) []string {
	joined := strings.Join(texts, "")
	line := make([]int, len(joined))
	for i, at := 0, 0; i < len(texts); i++ {
		for range len(texts[i]) {
			line[at] = i
			at++
		}
	}
	// decoded is the link's text as it will be shown, and lineOf the line each of its
	// bytes goes back on.
	var decoded strings.Builder
	var lineOf []int
	place := func(text string, onLine int) {
		decoded.WriteString(text)
		for range len(text) {
			lineOf = append(lineOf, onLine)
		}
	}
	last := 0
	for _, run := range encodedNonASCII.FindAllStringIndex(joined, -1) {
		for k := last; k < run[0]; k++ {
			place(joined[k:k+1], line[k])
		}
		last = run[1]
		at := run[0]
		for _, unit := range decodedUnits(joined[run[0]:run[1]]) {
			width := 3 * unit.escapes
			place(unit.text, line[at+width/2])
			at += width
		}
	}
	for k := last; k < len(joined); k++ {
		place(joined[k:k+1], line[k])
	}
	// A character is shown whole, on one line: a combining accent or the rest of a
	// joined emoji goes with what it joins to. It draws in the cells that took, so no
	// line grows.
	shown := decoded.String()
	lines := make([][]byte, len(texts))
	graphemes := uniseg.NewGraphemes(shown)
	for graphemes.Next() {
		from, to := graphemes.Positions()
		lines[lineOf[from]] = append(lines[lineOf[from]], shown[from:to]...)
	}
	texts = make([]string, len(lines))
	for i := range lines {
		texts[i] = string(lines[i])
	}
	return texts
}

func decodeNonASCII(s string) string {
	return encodedNonASCII.ReplaceAllStringFunc(s, func(run string) string {
		var b strings.Builder
		b.Grow(len(run))
		for _, unit := range decodedUnits(run) {
			b.WriteString(unit.text)
		}
		return b.String()
	})
}

// decodedUnit is one piece of a decoded run: a character and the escapes it was, or an
// escape left as it is.
type decodedUnit struct {
	text    string
	escapes int
}

// decodedUnits decodes a run of escapes a character at a time. A character starts at a
// leading byte written in lowercase hex — the form percentEncodeNonASCII writes, and
// which a URL's own escapes are moved out of — and is decoded when the escapes after it
// complete it and it is one a link may show. Anything else stays as it is: an escape
// such as %98, all digits, reads as lowercase but is never a leading byte, so a URL's
// own %E2%98%80 next to one of ours is kept rather than taking ours down with it.
func decodedUnits(run string) []decodedUnit {
	units := make([]decodedUnit, 0, len(run)/3)
	for i := 0; i+2 < len(run); {
		if run[i:i+3] == "%7c" {
			units = append(units, decodedUnit{"|", 1})
			i += 3
			continue
		}
		lead := unhex(run[i+1])<<4 | unhex(run[i+2])
		size := 0
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			size = 2
		case lead >= 0xe0 && lead <= 0xef:
			size = 3
		case lead >= 0xf0 && lead <= 0xf4:
			size = 4
		}
		if size > 0 && i+3*size <= len(run) && isLowerHex(run[i+1]) {
			encoded := make([]byte, size)
			for k := range size {
				encoded[k] = unhex(run[i+3*k+1])<<4 | unhex(run[i+3*k+2])
			}
			if r, n := utf8.DecodeRune(encoded); n == size && shownInLink(r) {
				units = append(units, decodedUnit{string(r), size})
				i += 3 * size
				continue
			}
		}
		units = append(units, decodedUnit{run[i : i+3], 1})
		i += 3
	}
	return units
}

func isLowerHex(c byte) bool {
	return c >= 'a' && c <= 'f'
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
// space among them — except the zero width joiner and non-joiner the sanitizer keeps.
func shownInLink(r rune) bool {
	return r >= utf8.RuneSelf && (r == '\u200c' || r == '\u200d' || !unicode.In(r, unicode.Z, unicode.C))
}

// bareLink is a link found in plain text: the bytes it spans, and what it links to.
type bareLink struct {
	start, end int
	target     string
}

// findBareLinks finds the URLs, www. addresses and email addresses in plain text, in
// order, and the @ of each address it declines to link. A URL, a www. address or a
// mailto: URI starts wherever a word could — anywhere not straight after an ASCII letter
// or digit — and an email address is matched wherever an @ sits between a local part
// and a host, as GFM matches one. An address inside a URL is part of the URL.
func findBareLinks(s string) (links []bareLink, declined []int) {
	run := 0 // where the run of text the last candidate sat in ends
	for i := 0; i < len(s); {
		if link, ok := urlAt(s, i, &run); ok {
			links = append(links, link)
			if link.target == "" {
				for at := strings.IndexByte(s[link.start:link.end], '@'); at >= 0; {
					declined = append(declined, link.start+at)
					next := strings.IndexByte(s[link.start+at+1:link.end], '@')
					if next < 0 {
						break
					}
					at += next + 1
				}
			}
			i = link.end
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	if !strings.Contains(s, "@") {
		return linked(links), declined
	}

	urls := links
	// claimed is every address, linked or declined, in order: a URL inside one —
	// devi*rao@www.example.org — is part of it, and goes with it.
	var addresses, claimed []bareLink
	next := 0    // the first URL that does not end before the address being checked
	settled := 0 // where the last address checked ends; nothing before it is looked at again
	for at := strings.IndexByte(s, '@'); at >= 0; at = nextAt(s, at) {
		if at < settled {
			continue
		}
		// The address is the whole run of address-shaped text around the @, and it is
		// linked only when GFM's pattern reads all of it: devi*rao@, éjane@,
		// jane@example.orgé and jane@dept.example.公司 are each declined as a whole
		// rather than linked as the different address the pattern could read.
		for next < len(urls) && urls[next].end <= at {
			next++
		}
		start, end := addressAround(s, at, settled)
		// An @ inside a URL is the URL's — in its path or query, or after its scheme
		// — and the URL is passed over whole rather than read again for every @ in it.
		// One in the host of a www. match, with nothing before it but the address —
		// www.jane@example.org — is an address.
		if next < len(urls) && urls[next].start <= at {
			url := urls[next]
			if url.start < start || strings.ContainsAny(s[url.start:at], "/?#") {
				settled = url.end
				continue
			}
		}
		// An @ with nothing before it is not an address, and claims nothing.
		if start == at {
			settled = at + 1
			continue
		}
		settled = end
		claimed = append(claimed, bareLink{start: start, end: end})
		if match := bareEmail.FindStringIndex(s[start:end]); match != nil && match[0] == 0 && match[1] == end-start {
			addresses = append(addresses, bareLink{start, end, s[start:end]})
			continue
		}
		for sign := start + strings.IndexByte(s[start:end], '@'); sign >= start; {
			declined = append(declined, sign)
			following := strings.IndexByte(s[sign+1:end], '@')
			if following < 0 {
				break
			}
			sign += following + 1
		}
	}
	// An address that starts before a URL holds it — jane@www.example.com is an
	// address at a www host — so the URL gives way to it. Both lists are in order, so
	// one pass over each finds the URLs an address overlaps.
	links = make([]bareLink, 0, len(urls)+len(addresses))
	next = 0
	for _, url := range urls {
		for next < len(claimed) && claimed[next].end <= url.start {
			next++
		}
		if url.target != "" && (next == len(claimed) || claimed[next].start >= url.end) {
			links = append(links, url)
		}
	}
	links = append(links, addresses...)
	sort.Slice(links, func(i, j int) bool { return links[i].start < links[j].start })
	return links, declined
}

// nextAt is the next @ in s after the one at at, or -1.
func nextAt(s string, at int) int {
	next := strings.IndexByte(s[at+1:], '@')
	if next < 0 {
		return -1
	}
	return at + 1 + next
}

// addressAround is the run of address-shaped text around the @ at at, looking no further
// back than from: a local part of letters, digits and marks in any script and what a
// local part may hold, and a host of letters, digits, marks, dots, - and _, less the
// dot a sentence ends with.
func addressAround(s string, at, from int) (start, end int) {
	start = at
	for start > from {
		r, size := utf8.DecodeLastRuneInString(s[from:start])
		if !unicode.In(r, unicode.L, unicode.M, unicode.N) && !strings.ContainsRune(".+_-"+otherLocalPart, r) {
			break
		}
		start -= size
	}
	if start == at {
		// Nothing before the @ is no address, and what follows is not read.
		return start, at + 1
	}
	end = at + 1
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if !unicode.In(r, unicode.L, unicode.M, unicode.N) && !strings.ContainsRune(".-_", r) && r != '@' {
			break
		}
		end += size
	}
	for end > at+1 && s[end-1] == '.' {
		end--
	}
	return start, end
}

// linked is links less those urlAt declined.
func linked(links []bareLink) []bareLink {
	kept := links[:0]
	for _, link := range links {
		if link.target != "" {
			kept = append(kept, link)
		}
	}
	return kept
}

// urlAt matches a URL, a www. address or a mailto: URI starting at s[i]. A link with
// no target is one it declines: the span it covers is linked by nothing.
func urlAt(s string, i int, run *int) (bareLink, bool) {
	switch s[i] {
	case 'h', 'H', 'f', 'F', 'w', 'W', 'm', 'M':
	default:
		return bareLink{}, false
	}
	// Only an ASCII letter or digit in front keeps a link from starting — xhttps:// is
	// not a scheme — since Chinese and Japanese put no space before one: 詳細はhttps://…
	if i > 0 && isAlphanumeric(s[i-1:i]) {
		return bareLink{}, false
	}
	// Whether a link starts here is settled within its first few hundred bytes — a host
	// is at most 256 characters and an address not much more — by the one pattern its
	// prefix calls for, before anything reads further.
	pattern := linkPattern(s[i:])
	if pattern == nil {
		return bareLink{}, false
	}
	probe := s[i:min(len(s), i+linkProbe)]
	if space := strings.IndexFunc(probe, unicode.IsSpace); space >= 0 {
		probe = probe[:space]
	}
	if !pattern.MatchString(probe) {
		return bareLink{}, false
	}
	// A link is matched within the run of text it sits in and no further: Chinese and
	// Japanese put no space after one, and a pattern let loose on the rest of the
	// paragraph would read it once for every link in it. The run is found once and kept
	// for the candidates after this one in it; from a later start it can only end at
	// the same place or earlier, at a bracket opened before that start, and a match is
	// cut at such a bracket anyway.
	if i >= *run {
		*run = i + linkTokenEnd(s[i:])
	}
	s = s[:*run]
	// A mailto: URI is the whole of what it opens — every recipient, and the query
	// that carries a subject and a body.
	if m := mailtoURI.FindString(s[i:]); m != "" {
		// The URI is linked when nothing but what a link's end sheds follows it in
		// the run of text it sits in — "mailto:jane@example.org!" — and declined when
		// more does: a recipient list the pattern could not read to its end, a second
		// recipient of o'brien@ or devi*rao@, would open without the rest of it. A
		// declined link comes back with no target, and its addresses are declined.
		url := trimLinkEnd(cutAtUnopenedBracket(m))
		token := cutAtUnopenedBracket(s[i:])
		if trimLinkEnd(token) == url {
			return bareLink{i, i + len(url), url}, true
		}
		return bareLink{start: i, end: i + len(token)}, true
	}
	if m := wholeHost(bareURL, s, i); m != "" {
		if url := trimLinkEnd(cutAtUnopenedBracket(m)); strings.Contains(url[strings.Index(url, "://")+3:], ".") {
			return bareLink{i, i + len(url), url}, true
		}
	}
	if m := wholeHost(bareWWW, s, i); m != "" {
		url := trimLinkEnd(cutAtUnopenedBracket(m))
		return bareLink{i, i + len(url), "http://" + url}, true
	}
	return bareLink{}, false
}

// linkProbe is how much of the text a candidate link is first matched in: enough for a
// scheme, a host of 256 characters four bytes each, and a port — or for mailto: and an
// address — and cut at the first space, so ordinary prose probes a word.
const linkProbe = 4*256 + 64

// linkPattern is the pattern for the link s starts the way of — a URL, a www. address
// or a mailto: URI, its prefix in any case — or nil.
func linkPattern(s string) *regexp.Regexp {
	hasPrefix := func(prefix string) bool {
		return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
	}
	switch {
	case hasPrefix("http://"), hasPrefix("https://"), hasPrefix("ftp://"):
		return bareURL
	case hasPrefix("www."):
		return bareWWW
	case hasPrefix("mailto:"):
		return mailtoURI
	default:
		return nil
	}
}

// linkTokenEnd is where the run of text a link sits in ends: at a space, at what ends a
// link in any form or a control, at punctuation outside ASCII — the 。 or 、 a sentence goes on after —
// or at a wide closing bracket the run did not open. It stops there
// rather than running to the end of the paragraph and being cut afterwards: Chinese and
// Japanese put no spaces between sentences, and a paragraph of many links would be
// scanned to its end once for each of them.
func linkTokenEnd(s string) int {
	var open [9]int
	for i, r := range s {
		switch {
		case unicode.IsSpace(r) || r < 0x20 || r == 0x7f || strings.ContainsRune(`<>"`, r):
			return i
		case r < utf8.RuneSelf:
		default:
			if pair := strings.IndexRune(wideOpeners, r); pair >= 0 {
				open[utf8.RuneCountInString(wideOpeners[:pair])]++
			} else if pair := strings.IndexRune(wideClosers, r); pair >= 0 {
				n := utf8.RuneCountInString(wideClosers[:pair])
				if open[n] == 0 {
					return i
				}
				open[n]--
			} else if unicode.IsPunct(r) {
				return i
			}
		}
	}
	return len(s)
}

// wholeHost matches pattern at s[i], answering nothing when the host it matched stops
// short of the host in the text — https://example.com2 is not a link to example.com,
// and linking it as one would open a different site.
func wholeHost(pattern *regexp.Regexp, s string, i int) string {
	m := pattern.FindStringSubmatch(s[i:])
	if m == nil {
		return ""
	}
	if rest := s[i+len(m[1]):]; m[2] == "" && (continuesWord(rest) || continuesNumber(rest)) {
		return ""
	}
	return m[0]
}

// continuesNumber reports whether s starts with a dot and a digit — more of an IPv4-shaped
// host than the address matched, 192.0.2.1 of 192.0.2.1.2.
func continuesNumber(s string) bool {
	return len(s) > 1 && s[0] == '.' && s[1] >= '0' && s[1] <= '9'
}

// continuesWord reports whether s starts with what would carry on the word before it: a
// letter, a digit, a mark, - or _.
func continuesWord(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return s != "" && (r == '-' || r == '_' || unicode.In(r, unicode.L, unicode.M, unicode.N))
}

// cutAtUnopenedBracket ends a link at the first non-ASCII closing bracket it did not
// open — the pairs in wideOpeners and wideClosers, which are all a path admits. Chinese
// and Japanese put no space after a link either, so the 」 that closes a quotation around
// one is followed by the sentence, which would otherwise run on into the link; a path's
// own （最終版） is opened in it and stays.
func cutAtUnopenedBracket(url string) string {
	if isASCII(url) {
		return url
	}
	var open [9]int
	for i, r := range url {
		if r < utf8.RuneSelf {
			continue
		}
		if pair := strings.IndexRune(wideOpeners, r); pair >= 0 {
			open[utf8.RuneCountInString(wideOpeners[:pair])]++
		} else if pair := strings.IndexRune(wideClosers, r); pair >= 0 {
			n := utf8.RuneCountInString(wideClosers[:pair])
			if open[n] == 0 {
				return url[:i]
			}
			open[n]--
		}
	}
	return url
}

// wideOpeners and wideClosers are the nine non-ASCII bracket pairs a path admits, in
// the same order.
const (
	wideOpeners = "（［｛「『【〔〈《"
	wideClosers = "）］｝」』】〕〉》"
)

// trimLinkEnd takes off the end of a link what GFM leaves outside one: the punctuation
// a sentence puts after a link (? ! . , : * _ ~ ' " and a backtick), a closing bracket
// — ) or a full-width ） among them — with no opening one in the link, an entity-shaped "&name;", a ";", and punctuation
// outside ASCII — a closing curly quote, a guillemet, an ellipsis. It is the one rule
// for both passes, so a link ends in the same place whatever stands in front of it.
func trimLinkEnd(url string) string {
	// The brackets are counted once, and a closer taken off is taken off the count, so
	// a link ending in a thousand ]s is trimmed in one pass rather than a thousand.
	var opened, closed map[rune]int
	for url != "" {
		last, size := utf8.DecodeLastRuneInString(url)
		switch {
		case strings.ContainsRune("?!.,:*_~'\"`", last):
			url = url[:len(url)-size]
		case last == ';':
			url = url[:len(url)-1]
			// The alphanumerics in front of it, back to an &, are an entity's name.
			name := len(url)
			for name > 0 && isAlphanumeric(url[name-1:name]) {
				name--
			}
			if name > 0 && name < len(url) && url[name-1] == '&' {
				url = url[:name-1]
			}
		case openingBracket[last] != "":
			if opened == nil {
				opened, closed = map[rune]int{}, map[rune]int{}
				for _, r := range url {
					if openingBracket[r] != "" {
						closed[r]++
					} else if strings.ContainsRune(openers, r) {
						opened[r]++
					}
				}
			}
			opener, _ := utf8.DecodeRuneInString(openingBracket[last])
			if opened[opener] >= closed[last] {
				return url
			}
			closed[last]--
			url = url[:len(url)-size]
		case last >= utf8.RuneSelf && unicode.IsPunct(last):
			url = url[:len(url)-size]
		default:
			return url
		}
	}
	return url
}

// openers is every bracket openingBracket pairs a closer with.
const openers = "([{" + wideOpeners

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
