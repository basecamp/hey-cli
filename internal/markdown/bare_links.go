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

	nonASCIIInLink = `[^\x00-\x7f\p{Z}\p{P}\p{C}]`
	bareHost       = `(?:[-a-zA-Z0-9@:%._\+~#=]|` + nonASCIIInLink + `){1,256}\.(?:[a-zA-Z]|\p{L})+`
	barePath       = `(?:[-a-zA-Z0-9@:%_+*.~#$!?&/=\(\);,'\^{}\[\]` + "`" + `]|` + nonASCIIInLink + `)*`
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
	return strings.Contains(s, "://") || strings.Contains(s, "@") ||
		strings.Contains(s, "www.") || strings.Contains(s, "WWW.")
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
		if autolinks(link.target) {
			found = append(found, replacement{start + at[link.start], start + at[link.end], "<" + link.target + ">"})
		}
	}
	return found
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
	if before, _ := utf8.DecodeLastRuneInString(s[:i]); i > 0 && (unicode.IsLetter(before) || unicode.IsDigit(before)) {
		return bareLink{}, false
	}
	if m := bareURL.FindString(s[i:]); m != "" {
		if url := trimLinkEnd(m); strings.Contains(url[strings.Index(url, "://")+3:], ".") {
			return bareLink{i, i + len(url), url}, true
		}
	}
	if m := bareWWW.FindString(s[i:]); m != "" {
		url := trimLinkEnd(m)
		return bareLink{i, i + len(url), "http://" + url}, true
	}
	return bareLink{}, false
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
// with no opening one in the link, an entity-shaped "&name;", a ";", and punctuation
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
		case last == ')' || last == ']' || last == '}':
			open := map[rune]string{')': "(", ']': "[", '}': "{"}[last]
			if strings.Count(url, open) >= strings.Count(url, string(last)) {
				return url
			}
			url = url[:len(url)-1]
		case last >= utf8.RuneSelf && unicode.IsPunct(last):
			url = url[:len(url)-size]
		default:
			return url
		}
	}
	return url
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
