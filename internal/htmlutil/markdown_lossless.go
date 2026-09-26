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
// Trix attachment, an image, a table, underline or colour — or a link ToMarkdown will
// not write, because writing the Markdown back in place of s would lose it.
func MarkdownIsLossless(s string) bool {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return false
	}
	pending := []*html.Node{doc}
	for len(pending) > 0 {
		n := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if n.Type == html.ElementNode && !keptByMarkdown(n) {
			return false
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			pending = append(pending, child)
		}
	}
	return true
}

func keptByMarkdown(n *html.Node) bool {
	if !markdownElements[n.Data] {
		return false
	}
	if n.Data == "a" {
		_, linkable := destination(getAttr(n, "href"))
		return linkable
	}
	return true
}
