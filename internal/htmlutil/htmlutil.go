package htmlutil

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
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
	walkNode(&b, doc)
	// Collapse runs of 3+ newlines into 2
	result := b.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

// PrependText adds a plain-text note before existing HTML content.
func PrependText(content, note string) string {
	note = strings.TrimSpace(strings.ReplaceAll(note, "\r\n", "\n"))
	if note == "" {
		return content
	}
	escaped := stdhtml.EscapeString(note)
	escaped = strings.ReplaceAll(escaped, "\n", "<br>")
	return "<div>" + escaped + "</div><br>" + content
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
	findAttachments(doc, &attachments)
	return attachments
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
	Filesize    *int64 `json:"filesize"`
	SGID        string `json:"sgid"`
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

func findAttachments(n *html.Node, attachments *[]Attachment) {
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
			if trix := parseTrixAttachment(n); trix != nil && trix.URL != "" {
				*attachments = append(*attachments, Attachment{
					URL:         trix.URL,
					Filename:    trix.Filename,
					ContentType: trix.ContentType,
					ByteSize:    nonnegativeAttachmentByteSize(trix.Filesize),
					SGID:        trix.SGID,
				})
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		findAttachments(child, attachments)
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

func findImages(n *html.Node, urls *[]string) {
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
			if att := parseTrixAttachment(n); att != nil && att.URL != "" && isImageContentType(att.ContentType) {
				*urls = append(*urls, att.URL)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findImages(c, urls)
	}
}
