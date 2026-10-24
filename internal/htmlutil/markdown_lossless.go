package htmlutil

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// markdownElements are the elements ToMarkdown writes as Markdown that FromMarkdown
// turns back into the same thing — or into what HEY shows the same way, a <p> for a
// <div>, <strong> for <b>. They are everything HEY's editor writes, attachments aside.
var markdownElements = map[string]bool{
	"body": true, "div": true, "p": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"strong": true, "b": true, "em": true, "i": true, "del": true, "s": true, "strike": true,
	"a": true, "blockquote": true, "ul": true, "ol": true, "li": true, "pre": true, "code": true,
	// A span with no attributes shows nothing of its own; one with a style or class does
	// and fails on its attributes.
	"span": true,
}

// markdownBlocks are the elements a line of Markdown cannot run across: a break's
// neighbours count only inside the same one.
var markdownBlocks = map[string]bool{
	"div": true, "p": true, "li": true, "blockquote": true, "pre": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// losslessContext is where in the note an element stands, as far as ToMarkdown cares.
type losslessContext struct {
	listDepth   int
	quoteDepth  int
	inListItem  bool
	inHeading   bool
	inCodeBlock bool
	inCode      bool
}

// MarkdownIsLossless reports whether ToMarkdown keeps everything in s that FromMarkdown
// can write back. It is false when s holds something Markdown has no syntax for — a
// Trix attachment, an image, a table, underline, an attribute such as a colour or a
// list's starting number, formatting inside code, nesting deeper than ToMarkdown
// renders, line breaks Markdown cannot place — or a link ToMarkdown will not write,
// because writing the Markdown back in place of s would lose it.
func MarkdownIsLossless(s string) bool {
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), context)
	if err != nil {
		return false
	}
	// HEY's own wrapper is taken off, as --note-html does; any other trix-content div
	// is the author's and has to pass like any other div.
	nodes, _ = unwrapLeadingTrixContent(nodes)
	// The fragment's nodes come back detached; they are put back under one body so a
	// break at the top level can see what stands beside it.
	for _, node := range nodes {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
		context.AppendChild(node)
	}

	type visit struct {
		node    *html.Node
		context losslessContext
	}
	pending := []visit{{node: context}}
	for len(pending) > 0 {
		v := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		n, ctx := v.node, v.context
		switch n.Type { //nolint:exhaustive // only text and element nodes carry content
		case html.ElementNode:
			if !elementKeptByMarkdown(n, ctx) {
				return false
			}
			ctx = ctx.inside(n)
		case html.TextNode:
			if !textKeptByMarkdown(n.Data, ctx) {
				return false
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			pending = append(pending, visit{node: child, context: ctx})
		}
	}
	// The checks above name what Markdown cannot carry at all; the round trip catches
	// what it carries differently — a list item outside a list, bold inside bold, a
	// link label ToMarkdown rewrites.
	return roundTripsThroughMarkdown(s)
}

// inside is the context for an element's children.
func (c losslessContext) inside(n *html.Node) losslessContext {
	switch n.Data {
	case "ul", "ol":
		c.listDepth++
	case "li":
		c.inListItem = true
	case "blockquote":
		c.quoteDepth++
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.inHeading = true
	case "pre":
		c.inCodeBlock = true
	case "code":
		if !c.inCodeBlock {
			c.inCode = true
		}
	}
	return c
}

func elementKeptByMarkdown(n *html.Node, ctx losslessContext) bool {
	switch {
	case ctx.inCode:
		// Inline code is written as its text alone.
		return false
	case !keptByMarkdown(n, ctx.inCodeBlock):
		return false
	}
	switch n.Data {
	case "ul", "ol":
		// ToMarkdown renders lists and quotes nested past maxNestingDepth at the same
		// level, so their structure would not come back.
		return ctx.listDepth < maxNestingDepth
	case "blockquote":
		return ctx.quoteDepth < maxNestingDepth
	case "br":
		if ctx.inCodeBlock {
			return true
		}
		return !ctx.inHeading && breaksKeptByMarkdown(n, ctx.inListItem)
	}
	return true
}

// textKeptByMarkdown reports whether ToMarkdown writes text as it stands. Code keeps
// everything but control characters. Prose goes through terminal.Sanitize, which drops
// what draws nothing — a soft hyphen, a zero width space, a control — and has its
// whitespace folded, which only matters where a non-breaking space makes a run of
// spaces show.
func textKeptByMarkdown(text string, ctx losslessContext) bool {
	if ctx.inCodeBlock || ctx.inCode {
		return stripControls(text) == text
	}
	return terminal.Sanitize(text) == text && !hasVisibleSpaceRun(text)
}

// hasVisibleSpaceRun reports two or more whitespace characters in a row with a
// non-breaking space among them: HTML shows that run as it is, and ToMarkdown folds it
// to one space.
func hasVisibleSpaceRun(text string) bool {
	run, nonBreaking := 0, false
	for _, r := range text {
		if !unicode.IsSpace(r) {
			run, nonBreaking = 0, false
			continue
		}
		run++
		nonBreaking = nonBreaking || r == '\u00a0'
		if run > 1 && nonBreaking {
			return true
		}
	}
	return false
}

// keptByMarkdown reports whether an element and its attributes survive a trip through
// Markdown. Inside a code block only line breaks and a bare <code> do: the block is
// written verbatim, so any formatting in it would be dropped.
func keptByMarkdown(n *html.Node, inCodeBlock bool) bool {
	if !markdownElements[n.Data] {
		return false
	}
	if inCodeBlock {
		return (n.Data == "br" || n.Data == "code") && len(n.Attr) == 0
	}
	switch n.Data {
	case "a":
		if len(n.Attr) != 1 || n.Attr[0].Key != "href" || n.Attr[0].Namespace != "" {
			return false
		}
		_, linkable := destination(n.Attr[0].Val)
		return linkable
	case "pre":
		if len(n.Attr) == 0 {
			return true
		}
		// FromMarkdown writes back the languages HEY's editor highlights, in the form it
		// stores them, and drops any other.
		language := n.Attr[0].Val
		return len(n.Attr) == 1 && n.Attr[0].Key == "language" && n.Attr[0].Namespace == "" &&
			trixLanguages[language] == language
	default:
		return len(n.Attr) == 0
	}
}

// breaksKeptByMarkdown reports whether the run of line breaks starting at a <br> comes
// back from Markdown as it went. A break needs text before it on its line: ToMarkdown
// writes nothing for one that starts a block. After that, one break is a hard break and
// two a blank line, but a third blank line has no Markdown, and in a list item even the
// second one does not: ToMarkdown writes two breaks there as one rather than loosen the
// list. A run that ends a block is the block's end, which Markdown keeps.
func breaksKeptByMarkdown(br *html.Node, inListItem bool) bool {
	if previous := significantSibling(br, previousSibling); previous != nil && isBreak(previous) {
		return true // the run was judged at its first break
	}
	if !contentBefore(br) {
		return false
	}
	run := 1
	for next := significantSibling(br, nextSibling); next != nil && isBreak(next); next = significantSibling(next, nextSibling) {
		run++
	}
	if inListItem {
		return run < 2
	}
	return run < 3
}

// contentBefore reports whether anything but whitespace and breaks stands before n in
// the block it is in, climbing out of inline elements such as <strong> to look.
func contentBefore(n *html.Node) bool {
	for ; n != nil; n = n.Parent {
		for sibling := n.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
			if !isWhitespace(sibling) && !isBreak(sibling) {
				return true
			}
		}
		if n.Parent == nil || markdownBlocks[n.Parent.Data] {
			return false
		}
	}
	return false
}

func significantSibling(n *html.Node, step func(*html.Node) *html.Node) *html.Node {
	for sibling := step(n); sibling != nil; sibling = step(sibling) {
		if !isWhitespace(sibling) {
			return sibling
		}
	}
	return nil
}

func isBreak(n *html.Node) bool {
	return n.Type == html.ElementNode && n.Data == "br"
}

func previousSibling(n *html.Node) *html.Node { return n.PrevSibling }

func nextSibling(n *html.Node) *html.Node { return n.NextSibling }
