package markdown

import (
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// glamour reads a Markdown document's text straight out of the source and runs most of
// it through html.UnescapeString before styling it — prose, code spans, raw HTML — so
// "&#27;[31m" in the source reaches the terminal as a live escape byte however the
// Markdown spelled it. CommonMark decodes entities too, but not in code, and a
// backslash-escaped "\&" is a literal ampersand to CommonMark where to glamour it is
// still the start of an entity.
//
// The source is therefore rewritten before glamour sees it so that its extra decode is
// the identity: every `&` in the text it decodes becomes `&amp;`, which decodes back to
// `&`, and a "\&" in prose becomes `&amp;` as well, since glamour would otherwise show
// the backslash. What glamour reads verbatim is left verbatim — code blocks, an image's
// alt text, link destinations and autolinks — so that `&amp;` never shows, and a `%26`
// where a query string had `&` never becomes a different URL.
//
// The rewrite is parsed with the same goldmark configuration glamour uses, so the
// segments it finds are the segments glamour will read.

var sourceParser = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList))

// prepareSource makes md safe to show and safe to hand to glamour. The first result
// is the source with its control characters stripped and its quote nesting bounded,
// which is what a fallback shows when glamour cannot render; the second is that source
// with entity decoding neutralized, which only glamour should see — its rewrite is
// glamour's quirk, and shown directly it reads "&amp;" where the source read "&".
// Newlines and tabs stay, since Markdown is made of them.
func prepareSource(md string) (safe, forGlamour string) {
	safe = boundQuoteDepth(stripControls(md))
	return safe, neutralizeEntities(safe)
}

// maxQuoteDepth is how many block quote markers a line keeps. glamour's rendering cost
// doubles every few levels of quote nesting, and a line of a hundred `>` is a hang.
const maxQuoteDepth = 16

func boundQuoteDepth(md string) string {
	if strings.Count(md, ">") <= maxQuoteDepth {
		return md
	}
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		lines[i] = boundedQuoteLine(line)
	}
	return strings.Join(lines, "\n")
}

// boundedQuoteLine collapses the markers past maxQuoteDepth on one line. A marker is a
// `>` after up to three spaces of indentation, optionally followed by one space.
func boundedQuoteLine(line string) string {
	rest := line
	depth := 0
	for {
		trimmed := strings.TrimLeft(rest, " ")
		if len(rest)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, ">") {
			break
		}
		depth++
		rest = strings.TrimPrefix(trimmed[1:], " ")
	}
	if depth <= maxQuoteDepth {
		return line
	}
	return strings.Repeat("> ", maxQuoteDepth) + rest
}

func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !isControl(r) {
			return r
		}
		return -1
	}, s)
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// sourceSpan is a byte range of the source that glamour will read as text, and how it
// reads it: prose is decoded with backslash escapes honored, code is decoded verbatim,
// and an image's alt text is not decoded at all.
type sourceSpan struct {
	start, stop int
	kind        spanKind
}

type spanKind int

const (
	proseSpan spanKind = iota
	codeSpan
	altSpan
)

func neutralizeEntities(md string) string {
	if !strings.Contains(md, "&") {
		return md
	}

	source := []byte(md)
	spans := textSpans(sourceParser.Parser().Parse(text.NewReader(source)))
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var b strings.Builder
	b.Grow(len(md) + 8)
	last := 0
	for _, span := range spans {
		if span.start < last || span.stop > len(source) {
			continue
		}
		b.Write(source[last:span.start])
		b.WriteString(neutralized(string(source[span.start:span.stop]), span.kind))
		last = span.stop
	}
	b.Write(source[last:])
	return b.String()
}

func textSpans(doc ast.Node) []sourceSpan {
	var spans []sourceSpan
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			kind := proseSpan
			if _, code := n.Parent().(*ast.CodeSpan); code {
				kind = codeSpan
			}
			if underImage(n) {
				kind = altSpan
			}
			spans = append(spans, sourceSpan{n.Segment.Start, n.Segment.Stop, kind})
		case *ast.HTMLBlock:
			lines := n.Lines()
			for i := range lines.Len() {
				line := lines.At(i)
				spans = append(spans, sourceSpan{line.Start, line.Stop, codeSpan})
			}
		case *ast.RawHTML:
			for i := range n.Segments.Len() {
				segment := n.Segments.At(i)
				spans = append(spans, sourceSpan{segment.Start, segment.Stop, codeSpan})
			}
		}
		return ast.WalkContinue, nil
	})
	return spans
}

func underImage(n ast.Node) bool {
	for parent := n.Parent(); parent != nil; parent = parent.Parent() {
		if _, image := parent.(*ast.Image); image {
			return true
		}
	}
	return false
}

// In prose an "&amp;" is left as it is: that is how ToMarkdown writes every ampersand,
// and glamour's one decode turns it back into the "&" it stands for. Any other "&" —
// one that begins an entity the source spelled out, or a bare one from a caller that
// did not come through ToMarkdown — is encoded so that the decode shows it literally.
// Code is verbatim, so every "&" there is encoded; an "&amp;" in a code sample is the
// five characters it reads as. Alt text glamour shows as written, so the one decode
// ToMarkdown's "&amp;" needs is done here, and only that one: nothing else in alt text
// is touched, so an entity the email spelled out stays the characters it was.
var (
	codeAmpersands = strings.NewReplacer("&", "&amp;")
	textAmpersands = strings.NewReplacer("&amp;", "&amp;", `\&`, "&amp;", "&", "&amp;")
	altAmpersands  = strings.NewReplacer("&amp;", "&")
)

func neutralized(s string, kind spanKind) string {
	switch kind {
	case codeSpan:
		return codeAmpersands.Replace(s)
	case altSpan:
		return altAmpersands.Replace(s)
	default:
		return textAmpersands.Replace(s)
	}
}
