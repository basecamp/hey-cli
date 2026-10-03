package markdown

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
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
// ends at the é.
//
// Each bare link is therefore found here in the text as CommonMark reads it, by linkify
// with that pattern widened to letters, marks and digits from any script, and handed to
// glamour as an angle-bracket autolink, which it reads verbatim. A link glamour would
// find whole by itself — plain ASCII with no escape in it — is left as it is.

var (
	// proseParser is glamour's configuration without linkify, so that prose linkify
	// would have cut into pieces arrives here as one run of text.
	proseParser = goldmark.New(goldmark.WithExtensions(
		extension.Table, extension.Strikethrough, extension.TaskList, extension.DefinitionList,
	)).Parser()

	// linkFinder runs linkify, and nothing else, over a run of plain text.
	linkFinder = parser.NewParser(
		parser.WithBlockParsers(util.Prioritized(parser.NewParagraphParser(), 100)),
		parser.WithInlineParsers(util.Prioritized(extension.NewLinkifyParser(
			extension.WithLinkifyURLRegexp(bareURL),
			extension.WithLinkifyWWWRegexp(bareWWW),
		), 100)),
	)

	// bareURL and bareWWW are goldmark's linkify patterns with non-ASCII added to the
	// host and the path, so that an address in any script — and a path ending in € or
	// ☕ — is matched to its end, and with * added to the path, which GFM allows there
	// and linkify already trims from a link's end. Non-ASCII spaces, punctuation and
	// controls are left out: a curly quote, a dash or a no-break space still ends a
	// link, as punctuation after one should.
	bareURL = regexp.MustCompile(`^(?:http|https|ftp)://` + bareHost + `(?::\d+)?(?:[/#?]` + barePath + `)?`)
	bareWWW = regexp.MustCompile(`^www\.` + bareHost + `(?:[/#?]` + barePath + `)?`)
)

const (
	nonASCIIInLink = `[^\x00-\x7f\p{Z}\p{P}\p{C}]`
	bareHost       = `(?:[-a-zA-Z0-9@:%._\+~#=]|` + nonASCIIInLink + `){1,256}\.[a-z\p{L}]+`
	barePath       = `(?:[-a-zA-Z0-9@:%_+*.~#$!?&/=\(\);,'">\^{}\[\]` + "`" + `]|` + nonASCIIInLink + `)*`
)

// replacement swaps the source between start and stop for text.
type replacement struct {
	start, stop int
	text        string
}

// wholeBareLinks returns md with every bare link glamour would cut short written as an
// autolink.
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
			// linkify finds nothing in a link's label, an image's alt text or code.
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
		b.WriteString(md[last:r.start])
		b.WriteString(r.text)
		last = r.stop
	}
	b.WriteString(md[last:])
	return b.String()
}

// mayHoldLink is the cheap test for whether there is anything for linkify to find.
func mayHoldLink(s string) bool {
	return strings.Contains(s, "://") || strings.Contains(s, "www.") || strings.Contains(s, "@")
}

// bareLinksUnder finds the bare links in the prose directly under parent. Text sits in
// runs — goldmark splits it wherever an inline parser looked — and a run is the span of
// adjacent text nodes on one line, which is what linkify sees.
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
	cursor := 0
	_ = ast.Walk(linkFinder.Parse(text.NewReader(plain)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			cursor = n.Segment.Stop
		case *ast.AutoLink:
			label := n.Label(plain)
			begin := bytes.Index(plain[cursor:], label)
			if begin < 0 {
				return ast.WalkContinue, nil
			}
			begin += cursor
			end := begin + len(label)
			cursor = end
			if bytes.Equal(run[at[begin]:at[end]], label) && isASCII(label) {
				return ast.WalkContinue, nil
			}
			target := string(n.URL(plain))
			if n.AutoLinkType == ast.AutoLinkEmail {
				target = string(label)
			}
			if autolinks(target) {
				found = append(found, replacement{start + at[begin], start + at[end], "<" + target + ">"})
			}
		}
		return ast.WalkContinue, nil
	})
	return found
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

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
