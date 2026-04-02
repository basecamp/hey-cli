package htmlutil

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// ExtractTrixContent extracts the journal entry body from the HTML edit page.
// The JSON API returns 204 for journal entries, so this HTML-based extraction
// is used as a fallback to get the full content from the Trix editor input.
func ExtractTrixContent(data []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return findTrixInput(doc), nil
}

func findTrixInput(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "input" {
		isTarget := false
		value := ""
		for _, a := range n.Attr {
			if a.Key == "id" && strings.Contains(a.Val, "journal") && strings.HasSuffix(a.Val, "trix_input") {
				isTarget = true
			}
			if a.Key == "value" {
				value = a.Val
			}
		}
		if isTarget && value != "" {
			return value
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if v := findTrixInput(c); v != "" {
			return v
		}
	}
	return ""
}
