package markdown

import (
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"github.com/basecamp/hey-cli/internal/terminal"
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
// is the source with its control characters stripped, which is what a fallback shows
// when glamour cannot render; the second is that source with entity decoding
// neutralized, which only glamour should see — its rewrite is glamour's quirk, and
// shown directly it reads "&amp;" where the source read "&". Newlines and tabs stay,
// since Markdown is made of them.
//
// deep reports a document nested past maxNestingDepth — quotes in quotes, lists in
// lists, in any spelling — which glamour must not be given at all: its cost doubles
// every few levels of quote nesting, and a line of a hundred ">" is a hang. Such a
// document is shown as its source instead. The depth is measured on goldmark's own
// tree, so a marker written with a tab, an indent or a list inside a quote counts the
// way glamour would count it.
func prepareSource(md string) (safe, forGlamour string, deep bool) {
	safe = stripControls(md)
	forGlamour, depth := neutralizeEntities(safe)
	return safe, forGlamour, depth > maxNestingDepth
}

// maxNestingDepth is how deep quotes and lists may nest before the document is shown
// unrendered. ToMarkdown never nests past sixteen; at twenty glamour still renders in
// milliseconds.
const maxNestingDepth = 20

// stripControls removes escape sequences, C0 and C1 controls, the bidirectional
// controls and the confusables terminal.Sanitize describes, keeping newlines and tabs.
// A body is prose, and the Trojan-source class — a right-to-left override that shows
// one thing and means another — is no more welcome in it than in a subject line; the
// JSON keeps the original.
//
// It runs over the whole source, link destinations included, on purpose: what a link
// shows and where it goes are then the same text, and a destination that differs from
// the one shown only by a zero-width character is exactly the confusable the policy
// removes. Sparing destinations would link a URL the reader cannot see to read.
func stripControls(s string) string {
	return terminal.Sanitize(s)
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

// neutralizeEntities rewrites the source for glamour and reports how deeply the
// document nests, both from one parse.
func neutralizeEntities(md string) (string, int) {
	source := []byte(md)
	spans, depth := textSpans(sourceParser.Parser().Parse(text.NewReader(source)))
	if !strings.Contains(md, "&") {
		return md, depth
	}
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
	return b.String(), depth
}

// textSpans finds the spans glamour will decode, and measures how deep quotes and
// lists nest on the way.
func textSpans(doc ast.Node) (spans []sourceSpan, maxDepth int) {
	depth := 0
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n.(type) {
		case *ast.Blockquote, *ast.List:
			if entering {
				depth++
				maxDepth = max(maxDepth, depth)
			} else {
				depth--
			}
		}
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
	return spans, maxDepth
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
