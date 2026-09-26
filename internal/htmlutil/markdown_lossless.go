package htmlutil

import (
	"strings"

	"golang.org/x/net/html"
)

// markdownElements are the elements ToMarkdown writes as Markdown that FromMarkdown
// turns back into the same thing — or into what HEY shows the same way, a <p> for a
// <div>, <strong> for <b>. They are everything HEY's editor writes, attachments aside.
var markdownElements = map[string]bool{
	"html": true, "head": true, "body": true,
	"div": true, "p": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"strong": true, "b": true, "em": true, "i": true, "del": true, "s": true, "strike": true,
	"a": true, "blockquote": true, "ul": true, "ol": true, "li": true, "pre": true, "code": true,
}

// MarkdownIsLossless reports whether ToMarkdown keeps everything in s that FromMarkdown
// can write back. It is false when s holds something Markdown has no syntax for — a
// Trix attachment, an image, a table, underline, an attribute such as a colour or a
// list's starting number, formatting inside a code block, blank lines Markdown cannot
// place — or a link ToMarkdown will not write, because writing the Markdown back in
// place of s would lose it.
func MarkdownIsLossless(s string) bool {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return false
	}

	type visit struct {
		node        *html.Node
		inListItem  bool
		inCodeBlock bool
	}
	pending := []visit{{node: doc}}
	for len(pending) > 0 {
		v := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		n := v.node
		if n.Type == html.ElementNode {
			if !keptByMarkdown(n, v.inCodeBlock) {
				return false
			}
			if n.Data == "br" && !v.inCodeBlock && !breaksKeptByMarkdown(n, v.inListItem) {
				return false
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			pending = append(pending, visit{
				node:        child,
				inListItem:  v.inListItem || (n.Type == html.ElementNode && n.Data == "li"),
				inCodeBlock: v.inCodeBlock || (n.Type == html.ElementNode && n.Data == "pre"),
			})
		}
	}
	return true
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
	case "div":
		return len(n.Attr) == 0 || isTrixContentLayout(n)
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
// back from Markdown as it went. One break is a hard break and two a blank line, but a
// third blank line has no Markdown, and in a list item even the second one does not:
// ToMarkdown writes two breaks there as one rather than loosen the list.
func breaksKeptByMarkdown(br *html.Node, inListItem bool) bool {
	if previous := significantSibling(br, previousSibling); previous != nil && isBreak(previous) {
		return true // the run was judged at its first break
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
