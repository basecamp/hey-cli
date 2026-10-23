package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// What send_draft reads out of a draft's body (sending-safety D2): the text the
// confirmation shows, whether that text is everything the mail carries, its
// attachments, any reference a mail client would load by itself, and the hash the
// confirmation binds.
//
// The body is the Trix editing HTML GetMessageEdit serves. Its attachments are
// <figure data-trix-attachment="{json}"> (or <action-text-attachment>) elements whose
// JSON names the blob's sgid, filename, size and type beside a signed URL.

// reviewedBody is one draft body, analyzed.
type reviewedBody struct {
	// text is the plain-text rendering the confirmation shows, links with their targets.
	text string
	// lossless reports whether text shows everything the body transmits. Any tag or
	// attribute outside the plain set below, which may style text out of sight, makes it
	// false; so does any attachment, since a name and a size don't show a file.
	lossless bool
	// attachments are the body's attachments, in order.
	attachments []attachment
	// remote names the first reference a mail client would fetch on open, or "".
	remote string
	// unverifiable names the first attachment element that isn't a plain blob
	// attachment (no sgid or filename, embedded HTML content, or markup Trix doesn't
	// write around one), or "". Its contents can't be reduced to a blob's identity, so
	// such a draft isn't sent.
	unverifiable string
	// hash is the sha256 of the body with each attachment reduced to its identity and
	// the volatile attributes (signed URLs) dropped, so two fetches of an unchanged draft
	// hash alike and a changed attachment doesn't.
	hash string
}

type attachment struct {
	SGID        string `json:"sgid"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Caption     string `json:"caption,omitempty"`
}

// plainTags are the elements Trix writes for text, which the text rendering shows in
// full. Anything else is treated as able to hide content.
var plainTags = map[atom.Atom]bool{
	atom.Div: true, atom.P: true, atom.Br: true, atom.Strong: true, atom.B: true,
	atom.Em: true, atom.I: true, atom.Del: true, atom.S: true, atom.Strike: true,
	atom.A: true, atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Blockquote: true,
	atom.Pre: true, atom.H1: true, atom.Code: true,
	// html.Parse wraps a fragment in these.
	atom.Html: true, atom.Head: true, atom.Body: true,
}

// blockTags end a line in the text rendering.
var blockTags = map[atom.Atom]bool{
	atom.Div: true, atom.P: true, atom.Li: true, atom.Blockquote: true, atom.Pre: true, atom.H1: true,
	atom.Ul: true, atom.Ol: true,
}

// loadingTags fetch something when a mail client renders them, or can make it.
var loadingTags = map[atom.Atom]bool{
	atom.Img: true, atom.Image: true, atom.Iframe: true, atom.Frame: true, atom.Video: true,
	atom.Audio: true, atom.Source: true, atom.Track: true, atom.Embed: true, atom.Object: true,
	atom.Link: true, atom.Picture: true, atom.Svg: true, atom.Style: true, atom.Script: true,
	atom.Meta: true, atom.Base: true, atom.Input: true,
}

// loadingAttributes name a URL a client loads by itself.
var loadingAttributes = []string{"src", "srcset", "background", "poster", "lowsrc", "dynsrc", "data", "xlink:href", "action", "formaction"}

// analyzeBody reads a draft's raw content.
func analyzeBody(content string) (reviewedBody, error) {
	nodes, err := html.ParseFragment(strings.NewReader(content), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return reviewedBody{}, fmt.Errorf("read the draft's body: %w", err)
	}

	w := &bodyWalker{lossless: true}
	for _, n := range nodes {
		w.walk(n)
	}

	sum := sha256.Sum256([]byte(w.canonical.String()))
	return reviewedBody{
		text:         tidyText(w.text.String()),
		lossless:     w.lossless && len(w.attachments) == 0,
		attachments:  w.attachments,
		remote:       w.remote,
		unverifiable: w.unverifiable,
		hash:         hex.EncodeToString(sum[:]),
	}, nil
}

type bodyWalker struct {
	text, canonical strings.Builder
	lossless        bool
	attachments     []attachment
	remote          string
	unverifiable    string
}

func (w *bodyWalker) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text.WriteString(n.Data)
		w.canonical.WriteString(strconv.Quote(n.Data))
		return
	case html.CommentNode:
		w.lossless = false
		w.canonical.WriteString("<!--" + strconv.Quote(n.Data) + "-->")
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.walk(c)
		}
		return
	}

	a, isAttachment, problem := attachmentOf(n)
	if problem != "" && w.unverifiable == "" {
		w.unverifiable = problem
	}
	if isAttachment && problem == "" {
		// The attachment's own markup (the preview image on a signed URL, the caption
		// Trix derives) is HEY's, not the sender's: reduce it to the blob's identity.
		w.attachments = append(w.attachments, a)
		identity, _ := json.Marshal(a)
		w.canonical.WriteString("<attachment " + string(identity) + ">")
		w.text.WriteString("\n[attachment: " + a.Filename + "]\n")
		return
	}

	if !plainTags[n.DataAtom] {
		w.lossless = false
	}
	if loadingTags[n.DataAtom] && w.remote == "" {
		w.remote = "<" + n.Data + ">"
	}

	attrs := slices.Clone(n.Attr)
	slices.SortFunc(attrs, func(a, b html.Attribute) int { return strings.Compare(a.Key, b.Key) })
	w.canonical.WriteString("<" + n.Data)
	for _, attr := range attrs {
		w.canonical.WriteString(" " + attr.Key + "=" + strconv.Quote(attr.Val))
		if n.DataAtom != atom.A || attr.Key != "href" {
			w.lossless = false
		}
		if w.remote == "" && loadsByItself(attr) {
			w.remote = attr.Key + " on <" + n.Data + ">"
		}
	}
	w.canonical.WriteString(">")

	if n.DataAtom == atom.Br {
		w.text.WriteString("\n")
	}
	if n.DataAtom == atom.Li {
		w.text.WriteString("- ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
	if n.DataAtom == atom.A {
		if href := attrValue(n, "href"); href != "" {
			// A link doesn't load itself, so it's seen content once its target shows.
			w.text.WriteString(" <" + href + ">")
		}
	}
	if blockTags[n.DataAtom] {
		w.text.WriteString("\n")
	}
	w.canonical.WriteString("</" + n.Data + ">")
}

// loadsByItself reports whether an attribute makes a mail client fetch a URL on open:
// a loading attribute, or a style that names one.
func loadsByItself(attr html.Attribute) bool {
	key := strings.ToLower(attr.Key)
	if slices.Contains(loadingAttributes, key) {
		return true
	}
	if key == "style" {
		value := strings.ToLower(attr.Val)
		return strings.Contains(value, "url(") || strings.Contains(value, "image-set(") || strings.Contains(value, "@import")
	}
	return false
}

// attachmentOf reads a Trix attachment element: a figure carrying data-trix-attachment,
// or an action-text-attachment. It answers whether n is one, and a problem when it is
// one whose contents can't be reduced to a blob's identity: no sgid or filename (Trix
// carries an embedded email body as a filename-less text/html attachment whose markup
// is in its JSON), embedded content, an attribute Trix doesn't write on one, or
// anything inside but its preview image and caption.
func attachmentOf(n *html.Node) (attachment, bool, string) {
	var a attachment
	var allowed map[string]bool
	var embedded bool
	switch {
	case n.DataAtom == atom.Figure && hasAttr(n, "data-trix-attachment"):
		var data map[string]any
		if json.Unmarshal([]byte(attrValue(n, "data-trix-attachment")), &data) != nil {
			return a, true, "a figure whose attachment data doesn't parse"
		}
		var attributes map[string]any
		_ = json.Unmarshal([]byte(attrValue(n, "data-trix-attributes")), &attributes)
		_, embedded = data["content"]
		a = attachment{SGID: asText(data["sgid"]), Filename: asText(data["filename"]), Size: sizeOf(data["filesize"]), ContentType: asText(data["contentType"]), Caption: asText(attributes["caption"])}
		allowed = map[string]bool{"data-trix-attachment": true, "data-trix-attributes": true, "data-trix-content-type": true, "class": true}
	case n.Data == "action-text-attachment":
		size, _ := strconv.ParseInt(attrValue(n, "filesize"), 10, 64)
		embedded = hasAttr(n, "content")
		a = attachment{SGID: attrValue(n, "sgid"), Filename: attrValue(n, "filename"), Size: size, ContentType: attrValue(n, "content-type"), Caption: attrValue(n, "caption")}
		allowed = map[string]bool{"sgid": true, "filename": true, "filesize": true, "content-type": true, "caption": true, "url": true, "presentation": true, "previewable": true, "width": true, "height": true}
	default:
		return a, false, ""
	}

	name := a.Filename
	if name == "" {
		name = "an unnamed attachment"
	}
	switch {
	case embedded:
		return a, true, name + " carries embedded content"
	case a.SGID == "" || a.Filename == "":
		return a, true, name + " has no blob identity"
	}
	for _, attr := range n.Attr {
		if !allowed[attr.Key] {
			return a, true, name + " carries " + attr.Key
		}
	}
	if !plainAttachmentChildren(n) {
		return a, true, name + " wraps markup of its own"
	}
	return a, true, ""
}

// plainAttachmentChildren reports whether an attachment holds only what Trix renders
// in one: whitespace, a preview image, and a caption of plain text.
func plainAttachmentChildren(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		case c.Type == html.ElementNode && c.DataAtom == atom.Img && c.FirstChild == nil:
		case c.Type == html.ElementNode && c.DataAtom == atom.Figcaption:
			for t := c.FirstChild; t != nil; t = t.NextSibling {
				if t.Type != html.TextNode {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func hasAttr(n *html.Node, key string) bool {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return true
		}
	}
	return false
}

func asText(v any) string {
	s, _ := v.(string)
	return s
}

func sizeOf(v any) int64 {
	switch size := v.(type) {
	case float64:
		return int64(size)
	case string:
		n, _ := strconv.ParseInt(size, 10, 64)
		return n
	default:
		return 0
	}
}

func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// tidyText trims trailing space from each line and collapses runs of blank lines.
func tidyText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}
