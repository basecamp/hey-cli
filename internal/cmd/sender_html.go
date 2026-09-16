package cmd

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Compare trees rather than rendered text: Trix changes paragraph tags and
// attachment serialization, but a changed link or missing emphasis is not an
// equivalent message. Unrecognized transformations leave the draft unsent.
func sameTrixMessageHTML(expected, actual string) bool {
	parse := func(content string) ([]*html.Node, error) {
		return html.ParseFragment(strings.NewReader(content), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	}
	left, err := parse(expected)
	if err != nil {
		return false
	}
	right, err := parse(actual)
	if err != nil {
		return false
	}
	return sameTrixNodes(left, right, false)
}

func sameTrixNodes(left, right []*html.Node, preserveWhitespace bool) bool {
	if !preserveWhitespace {
		left, right = withoutBlockSeparators(left), withoutBlockSeparators(right)
	}
	if len(left) != len(right) {
		return false
	}
	for index, a := range left {
		b := right[index]
		if a.Type != b.Type {
			return false
		}
		if a.Type == html.ElementNode && a.Data == "action-text-attachment" && b.Data == "figure" {
			if !sameTrixUpload(a, b) {
				return false
			}
			continue
		}
		paragraph := a.Type == html.ElementNode && a.Data == "p" && b.Data == "div"
		if a.Data != b.Data && !paragraph {
			return false
		}
		if a.Namespace != b.Namespace || !sameHTMLAttributes(a.Attr, b.Attr) || !sameTrixNodes(htmlChildren(a), htmlChildren(b), preserveWhitespace || a.Data == "pre") {
			return false
		}
	}
	return true
}

func htmlChildren(node *html.Node) []*html.Node {
	var children []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		children = append(children, child)
	}
	return children
}

func sameHTMLAttributes(left, right []html.Attribute) bool {
	if len(left) != len(right) {
		return false
	}
	// Attributes are an unordered set, but duplicates are not accepted.
	seen := make(map[html.Attribute]bool, len(left))
	for _, attr := range left {
		if seen[attr] || !slices.Contains(right, attr) {
			return false
		}
		seen[attr] = true
	}
	return true
}

// Markdown emits newlines between block elements. Do not drop whitespace
// between inline elements or inside preformatted content.
func withoutBlockSeparators(nodes []*html.Node) []*html.Node {
	var result []*html.Node
	for index, node := range nodes {
		if len(nodes) > 1 && node.Type == html.TextNode && strings.TrimSpace(node.Data) == "" && strings.Contains(node.Data, "\n") &&
			(index == 0 || trixBlock(nodes[index-1])) && (index == len(nodes)-1 || trixBlock(nodes[index+1])) {
			continue
		}
		result = append(result, node)
	}
	return result
}

func trixBlock(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	switch node.Data {
	case "p", "div", "blockquote", "ul", "ol", "li", "pre", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

func sameTrixUpload(expected, actual *html.Node) bool {
	if len(significantHTMLNodes(htmlChildren(expected))) != 0 || len(significantHTMLNodes(htmlChildren(actual))) != 0 {
		return false
	}
	attributes := map[string]string{}
	for _, attr := range expected.Attr {
		if attr.Namespace != "" {
			return false
		}
		if _, exists := attributes[attr.Key]; exists {
			return false
		}
		attributes[attr.Key] = attr.Val
	}
	// Only the upload markup emitted by appendUploadedAttachments is normalized.
	if len(attributes) != 4 || attributes["sgid"] == "" || attributes["filename"] == "" || attributes["content-type"] == "" {
		return false
	}
	size, err := strconv.ParseInt(attributes["filesize"], 10, 64)
	if err != nil || size < 0 {
		return false
	}
	var attachment struct {
		SGID         string `json:"sgid"`
		Filename     string `json:"filename"`
		ContentType  string `json:"contentType"`
		Filesize     *int64 `json:"filesize"`
		Content      string `json:"content"`
		Caption      string `json:"caption"`
		Presentation string `json:"presentation"`
	}
	found := false
	for _, attr := range actual.Attr {
		if attr.Namespace != "" {
			return false
		}
		switch attr.Key {
		case "data-trix-attachment":
			if found || json.Unmarshal([]byte(attr.Val), &attachment) != nil {
				return false
			}
			found = true
		case "data-trix-attributes":
			// A caption or a presentation override is authored content, not upload metadata.
			var extra map[string]any
			if json.Unmarshal([]byte(attr.Val), &extra) != nil || len(extra) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return found && attachment.SGID == attributes["sgid"] && attachment.Filename == attributes["filename"] &&
		attachment.ContentType == attributes["content-type"] && attachment.Filesize != nil && *attachment.Filesize == size &&
		attachment.Content == "" && attachment.Caption == "" && attachment.Presentation == ""
}
