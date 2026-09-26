package htmlutil

import (
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// UnwrapTrixContent takes off the <div class="trix-content"> that HEY puts around rich
// text it serves for editing — a contact note's note_html, a journal entry's
// content_html. That div is Action Text's layout rather than part of what was written,
// and HEY adds it again on every read, so HTML read from HEY and written back as it is
// sinks one div deeper each time. Wrappers are taken off wherever they stand at the top
// level, however deeply they have already nested; HTML without one is returned as it
// came.
func UnwrapTrixContent(s string) string {
	if !strings.Contains(s, "trix-content") {
		return s
	}
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), context)
	if err != nil {
		return s
	}
	unwrapped, changed := unwrapTrixContentNodes(nodes)
	if !changed {
		return s
	}

	var b strings.Builder
	for _, node := range unwrapped {
		if err := html.Render(&b, node); err != nil {
			return s
		}
	}
	return strings.TrimSpace(b.String())
}

func unwrapTrixContentNodes(nodes []*html.Node) ([]*html.Node, bool) {
	unwrapped := make([]*html.Node, 0, len(nodes))
	changed := false
	for len(nodes) > 0 {
		node := nodes[0]
		nodes = nodes[1:]
		if !isTrixContentWrapper(node) {
			unwrapped = append(unwrapped, node)
			continue
		}
		changed = true
		var children []*html.Node
		for child := node.FirstChild; child != nil; child = node.FirstChild {
			node.RemoveChild(child)
			children = append(children, child)
		}
		// The wrapper's children stand at the top level now, so a wrapper among them is
		// looked at next rather than recursed into.
		nodes = append(children, nodes...)
	}
	return unwrapped, changed
}

func isTrixContentWrapper(n *html.Node) bool {
	return n.Type == html.ElementNode && n.DataAtom == atom.Div &&
		slices.Contains(strings.Fields(getAttr(n, "class")), "trix-content")
}
