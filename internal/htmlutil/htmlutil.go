package htmlutil

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	walkNode(&b, doc, 0)
	// Collapse runs of 3+ newlines into 2
	result := b.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

// MessageSourceText returns the source-backed text carried by HTML message content.
func MessageSourceText(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return ""
	}
	var b strings.Builder
	walkMessageSourceNode(&b, doc, 0)
	return strings.TrimSpace(b.String())
}

// PrependHTML adds an HTML note before existing HTML content.
func PrependHTML(content, note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return content
	}
	return note + "<br>" + content
}

// ExtractImageURLs finds image URLs from <img src> tags and
// <figure data-trix-attachment> elements.
func ExtractImageURLs(s string) []string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil
	}
	var urls []string
	findImages(doc, &urls, 0)
	return urls
}

// Attachment describes a downloadable file embedded in rich-text content.
type Attachment struct {
	URL         string
	Filename    string
	ContentType string
	ByteSize    *int64
	SGID        string
}

// ExtractAttachments returns downloadable files in their document order.
func ExtractAttachments(s string) []Attachment {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil
	}
	var attachments []Attachment
	findAttachments(doc, &attachments, 0)
	return attachments
}

func walkNode(b *strings.Builder, n *html.Node, depth int) {
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
				if att.Filename != "" {
					fmt.Fprintf(b, "\n[%s]\n", att.Filename)
					return
				}
				// HEY wraps pasted rich HTML in text/html trix attachments
				// whose markup sits in the JSON "content" field. Render it.
				if att.Content != "" {
					if doc := parseEmbeddedContent(att.Content, depth); doc != nil {
						walkNode(b, doc, depth+1)
						return
					}
				}
			}
		case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
			b.WriteString("\n")
			walkChildren(b, n, depth)
			b.WriteString("\n")
			return
		case "li":
			b.WriteString("\n  • ")
			walkChildren(b, n, depth)
			return
		case "ul", "ol":
			b.WriteString("\n")
			walkChildren(b, n, depth)
			b.WriteString("\n")
			return
		case "hr":
			b.WriteString("\n───\n")
			return
		}
	}
	walkChildren(b, n, depth)
}

type trixAttachment struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Filesize    *int64 `json:"filesize"`
	SGID        string `json:"sgid"`
	Content     string `json:"content"`
}

func walkMessageSourceNode(b *strings.Builder, n *html.Node, depth int) {
	switch n.Type { //nolint:exhaustive // only text and element nodes need handling
	case html.TextNode:
		b.WriteString(n.Data)
	case html.ElementNode:
		if !elementProvidesMessageSourceText(n) {
			return
		}
		switch n.Data {
		case "br":
			b.WriteByte('\n')
			return
		case "hr":
			writeMessageSourceBoundary(b)
			return
		case "template":
			if depth == 0 || n.Parent == nil || n.Parent.Data != "shadow-content" {
				return
			}
		case "details":
			if !hasAttr(n, "open") {
				walkClosedDetailsSummary(b, n, depth)
				return
			}
		case "figure":
			if att := parseTrixAttachment(n); att != nil && att.Filename == "" && att.Content != "" {
				if doc := parseEmbeddedContent(att.Content, depth); doc != nil {
					writeMessageSourceBoundary(b)
					walkMessageSourceNode(b, doc, depth+1)
					writeMessageSourceBoundary(b)
					return
				}
			}
		}
		if messageBlockElement(n) {
			writeMessageSourceBoundary(b)
			walkMessageSourceChildren(b, n, depth)
			writeMessageSourceBoundary(b)
			return
		}
	}
	walkMessageSourceChildren(b, n, depth)
}

func elementProvidesMessageSourceText(n *html.Node) bool {
	if hasAttr(n, "hidden") || hasAttr(n, "inert") || inlineStyleHidesText(getAttr(n, "style")) {
		return false
	}
	switch n.Data {
	case "script", "style", "noscript", "head", "action-text-attachment":
		return false
	case "dialog":
		return hasAttr(n, "open")
	default:
		return true
	}
}

func inlineStyleHidesText(style string) bool {
	display, _ := inlineStyleProperty(style, "display")
	visibility, _ := inlineStyleProperty(style, "visibility")
	contentVisibility, _ := inlineStyleProperty(style, "content-visibility")
	userSelect, _ := inlineStyleProperty(style, "user-select", "-webkit-user-select")
	return display == "none" || visibility == "hidden" || visibility == "collapse" ||
		contentVisibility == "hidden" || userSelect == "none"
}

func inlineStyleProperty(style string, names ...string) (string, bool) {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	var selected string
	selectedImportant := false
	found := false
	for declaration := range strings.SplitSeq(strings.ToLower(style), ";") {
		property, value, ok := strings.Cut(declaration, ":")
		if !ok || !wanted[strings.TrimSpace(property)] {
			continue
		}
		value = strings.TrimSpace(value)
		important := strings.HasSuffix(value, "!important")
		if important {
			value = strings.TrimSpace(strings.TrimSuffix(value, "!important"))
		}
		if !found || important || !selectedImportant {
			selected = value
			selectedImportant = important
			found = true
		}
	}
	return selected, found
}

func messageBlockElement(n *html.Node) bool {
	if display, ok := inlineStyleProperty(getAttr(n, "style"), "display"); ok {
		kind := display
		if fields := strings.Fields(display); len(fields) > 0 {
			kind = fields[0]
		}
		switch kind {
		case "inline", "inline-block", "inline-flex", "inline-grid", "inline-table", "contents":
			return false
		case "block", "flow-root", "flex", "grid", "list-item", "table", "table-caption", "table-cell", "table-footer-group", "table-header-group", "table-row", "table-row-group":
			return true
		}
	}
	switch n.Data {
	case "address", "article", "aside", "blockquote", "caption", "dd", "details", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "li", "main", "menu", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func walkClosedDetailsSummary(b *strings.Builder, details *html.Node, depth int) {
	for child := details.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "summary" {
			walkMessageSourceNode(b, child, depth)
			return
		}
	}
}

func walkMessageSourceChildren(b *strings.Builder, n *html.Node, depth int) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkMessageSourceNode(b, child, depth)
	}
}

func writeMessageSourceBoundary(b *strings.Builder) {
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
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

func hasAttr(n *html.Node, key string) bool {
	for _, attribute := range n.Attr {
		if attribute.Key == key {
			return true
		}
	}
	return false
}

func walkChildren(b *strings.Builder, n *html.Node, depth int) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(b, c, depth)
	}
}

// embeddedContentDepthLimit stops a pathological chain of attachments that
// each embed another from recursing without end.
const embeddedContentDepthLimit = 4

// parseEmbeddedContent parses the markup inside a text/html Trix attachment,
// returning nil once the nesting passes embeddedContentDepthLimit.
func parseEmbeddedContent(content string, depth int) *html.Node {
	if depth >= embeddedContentDepthLimit {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return nil
	}
	return doc
}

func findAttachments(n *html.Node, attachments *[]Attachment, depth int) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "action-text-attachment":
			byteSize := parseAttachmentByteSize(getAttr(n, "filesize"))
			attachment := Attachment{
				URL:         getAttr(n, "url"),
				Filename:    getAttr(n, "filename"),
				ContentType: getAttr(n, "content-type"),
				ByteSize:    byteSize,
				SGID:        getAttr(n, "sgid"),
			}
			if attachment.URL != "" && attachment.Filename != "" {
				*attachments = append(*attachments, attachment)
			}
		case "figure":
			trix := parseTrixAttachment(n)
			switch {
			case trix == nil:
			case trix.URL != "" && trix.Filename != "":
				*attachments = append(*attachments, Attachment{
					URL:         trix.URL,
					Filename:    trix.Filename,
					ContentType: trix.ContentType,
					ByteSize:    nonnegativeAttachmentByteSize(trix.Filesize),
					SGID:        trix.SGID,
				})
			case trix.Content != "":
				// An inbound email's files are inside the embedded markup, not
				// on the figure that wraps it. The wrapper itself is not listed:
				// an embedded body is not a downloadable file.
				if doc := parseEmbeddedContent(trix.Content, depth); doc != nil {
					findAttachments(doc, attachments, depth+1)
				}
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		findAttachments(child, attachments, depth)
	}
}

func isImageContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return contentType == "image" || strings.HasPrefix(contentType, "image/")
}

func parseAttachmentByteSize(value string) *int64 {
	if value == "" {
		return nil
	}
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || size < 0 {
		return nil
	}
	return &size
}

func nonnegativeAttachmentByteSize(size *int64) *int64 {
	if size == nil || *size < 0 {
		return nil
	}
	return size
}

func findImages(n *html.Node, urls *[]string, depth int) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "img":
			for _, a := range n.Attr {
				if a.Key == "src" && a.Val != "" {
					*urls = append(*urls, a.Val)
				}
			}
		case "action-text-attachment":
			if imageURL := getAttr(n, "url"); isImageContentType(getAttr(n, "content-type")) && imageURL != "" {
				*urls = append(*urls, imageURL)
			}
		case "figure":
			att := parseTrixAttachment(n)
			switch {
			case att == nil:
			case att.Filename != "" && att.URL != "" && isImageContentType(att.ContentType):
				*urls = append(*urls, att.URL)
			case att.Content != "":
				// An inbound email's inline images are inside the embedded
				// markup, not on the figure that wraps it.
				if doc := parseEmbeddedContent(att.Content, depth); doc != nil {
					findImages(doc, urls, depth+1)
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findImages(c, urls, depth)
	}
}
