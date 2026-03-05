package htmlutil

import (
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// ToText converts HTML content to plain text, preserving basic structure.
func ToText(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	var b strings.Builder
	walkNode(&b, doc)
	// Collapse runs of 3+ newlines into 2
	result := b.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

// ExtractImageURLs finds image URLs from <img src> tags and
// <figure data-trix-attachment> elements.
func ExtractImageURLs(s string) []string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil
	}
	var urls []string
	findImages(doc, &urls)
	return urls
}

func walkNode(b *strings.Builder, n *html.Node) {
	switch n.Type { //nolint:exhaustive // only text and element nodes need handling
	case html.TextNode:
		b.WriteString(n.Data)
	case html.ElementNode:
		switch n.Data {
		case "script", "style":
			return
		case "br":
			b.WriteString("\n")
		case "img":
			alt := getAttr(n, "alt")
			if alt != "" {
				fmt.Fprintf(b, "[%s]", alt)
			} else {
				b.WriteString("[image]")
			}
			return
		case "action-text-attachment":
			filename := getAttr(n, "filename")
			if filename != "" {
				fmt.Fprintf(b, "\n[%s]\n", filename)
			}
			return
		case "figure":
			if att := parseTrixAttachment(n); att != nil {
				fmt.Fprintf(b, "\n[%s]\n", att.Filename)
				return
			}
		case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
			b.WriteString("\n")
			walkChildren(b, n)
			b.WriteString("\n")
			return
		case "li":
			b.WriteString("\n  • ")
			walkChildren(b, n)
			return
		case "ul", "ol":
			b.WriteString("\n")
			walkChildren(b, n)
			b.WriteString("\n")
			return
		case "hr":
			b.WriteString("\n───\n")
			return
		}
	}
	walkChildren(b, n)
}

type trixAttachment struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
}

func parseTrixAttachment(n *html.Node) *trixAttachment {
	raw := getAttr(n, "data-trix-attachment")
	if raw == "" {
		return nil
	}
	var att trixAttachment
	if err := json.Unmarshal([]byte(raw), &att); err != nil {
		return nil
	}
	if att.Filename == "" {
		return nil
	}
	return &att
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func walkChildren(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(b, c)
	}
}

func findImages(n *html.Node, urls *[]string) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "img":
			for _, a := range n.Attr {
				if a.Key == "src" && a.Val != "" {
					*urls = append(*urls, a.Val)
				}
			}
		case "figure":
			if att := parseTrixAttachment(n); att != nil && att.URL != "" {
				*urls = append(*urls, att.URL)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findImages(c, urls)
	}
}
