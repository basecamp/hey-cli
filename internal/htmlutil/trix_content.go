package htmlutil

import (
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlSpace is the whitespace HTML collapses. A non-breaking space is not in it: it
// shows, so taking one off would change the note.
const htmlSpace = " \t\n\f\r"

// UnwrapTrixContent takes off the <div class="trix-content"> that HEY puts around rich
// text it serves for editing — a contact note's note_html, a journal entry's
// content_html. That div is Action Text's layout rather than part of what was written,
// and HEY adds it again on every read, so HTML read from HEY and written back as it is
// sinks one div deeper each time.
//
// Only the layout is taken off: a div carrying exactly class="trix-content", which is
// all the layout writes, standing first in the HTML — with or without what was added
// after it — and again at the start of what it held, which is where earlier round trips
// nested it. A div like that anywhere else, or one with any other attribute or class,
// is the author's and is kept. HTML with nothing to take off is returned as it came.
func UnwrapTrixContent(s string) string {
	if !strings.Contains(s, "trix-content") {
		return s
	}
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), context)
	if err != nil {
		return s
	}
	unwrapped, changed := unwrapLeadingTrixContent(nodes)
	if !changed {
		return s
	}

	var b strings.Builder
	for _, node := range unwrapped {
		if err := html.Render(&b, node); err != nil {
			return s
		}
	}
	return strings.Trim(b.String(), htmlSpace)
}

// unwrapLeadingTrixContent replaces the first node that is not whitespace with its
// children for as long as that node is HEY's layout.
func unwrapLeadingTrixContent(nodes []*html.Node) ([]*html.Node, bool) {
	changed := false
	for {
		first := slices.IndexFunc(nodes, func(n *html.Node) bool { return !isWhitespace(n) })
		if first < 0 || !isTrixContentLayout(nodes[first]) {
			return nodes, changed
		}
		changed = true
		layout := nodes[first]
		var children []*html.Node
		for child := layout.FirstChild; child != nil; child = layout.FirstChild {
			layout.RemoveChild(child)
			children = append(children, child)
		}
		nodes = slices.Concat(children, nodes[first+1:])
	}
}

func isWhitespace(n *html.Node) bool {
	return n.Type == html.TextNode && strings.Trim(n.Data, htmlSpace) == ""
}

// isTrixContentLayout matches the div Action Text's layout writes
// (layouts/action_text/contents/_content.html.erb): class="trix-content" and nothing
// else.
func isTrixContentLayout(n *html.Node) bool {
	return n.Type == html.ElementNode && n.DataAtom == atom.Div &&
		len(n.Attr) == 1 && n.Attr[0].Namespace == "" && n.Attr[0].Key == "class" &&
		strings.Trim(n.Attr[0].Val, htmlSpace) == "trix-content"
}
