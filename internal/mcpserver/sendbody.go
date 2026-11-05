package mcpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// What send_draft reads out of a draft's body (sending-safety D2): the text the
// confirmation shows, what that text leaves out, its attachments, links and images, the
// body it delivers, and the hash the confirmation binds.
//
// The body is the Trix editing HTML GetMessageEdit serves. Its attachments are
// <figure data-trix-attachment="{json}"> (or <action-text-attachment>) elements whose
// JSON names the blob's sgid, filename, size and type beside a signed URL, wrapped
// around a preview image and a caption HEY renders from the blob.
//
// A blob attachment is delivered as its identity alone: the element is rebuilt from the
// sgid, filename, size, type and caption, with nothing inside it, which is what HEY
// keeps of one anyway (Action Text resolves an attachment from those attributes and
// renders its markup afresh). So two fetches of an unchanged draft hash alike, whatever
// signed URLs its wrapper carries, and nothing in the wrapper is sent. An attachment
// that isn't a plain blob is delivered as it is and named in the prompt as one it can't
// name. The hash is over exactly the body that is delivered.
//
// Nothing here refuses a draft: what the text can't show is named on the prompt's
// "Not shown here" line, beside the draft's link in HEY.

// reviewedBody is one draft body, analyzed.
type reviewedBody struct {
	// text is the plain-text rendering the confirmation shows, links with their targets.
	text string
	// attachments are the body's blob attachments, in order.
	attachments []attachment
	// unnamed counts attachments that aren't plain blobs (no sgid or filename, or
	// embedded content, as Trix carries an embedded email body). They go as they are.
	unnamed int
	// images are the targets of the body's images, in order.
	images []string
	// unshown names the first markup the text rendering doesn't show, or "".
	unshown string
	// delivered is the body send_draft sends: the draft's, comments dropped, each blob
	// attachment rebuilt from its identity.
	delivered string
	// hash is the sha256 of delivered.
	hash string
}

type attachment struct {
	SGID        string `json:"sgid"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Caption     string `json:"caption,omitempty"`
}

// shownTags are the elements the text rendering shows whole: text structure and links,
// nothing that loads, styles or hides. Anything else is named on the prompt's "Not
// shown here" line. It is an allowlist on purpose: it says what the rendering covers,
// so whatever a mail client does with the rest is named without one more selector.
var shownTags = map[atom.Atom]bool{
	atom.Div: true, atom.P: true, atom.Br: true, atom.Span: true, atom.Strong: true, atom.B: true,
	atom.Em: true, atom.I: true, atom.U: true, atom.Del: true, atom.S: true, atom.Strike: true,
	atom.A: true, atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Blockquote: true,
	atom.Pre: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Hr: true, atom.Code: true, atom.Listing: true,
	// html.Parse wraps a fragment in these.
	atom.Html: true, atom.Head: true, atom.Body: true,
}

// blockTags end a line in the text rendering.
var blockTags = map[atom.Atom]bool{
	atom.Div: true, atom.P: true, atom.Li: true, atom.Blockquote: true, atom.Pre: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Ul: true, atom.Ol: true, atom.Hr: true, atom.Listing: true,
}

// rawTextTags hold text a mail client doesn't show as text (a stylesheet, a script),
// so the rendering leaves it out rather than showing it as the body's. Elements whose
// text a client may show (textarea, xmp, plaintext, the fallbacks) are walked like any
// other, so their text is in the prompt, and named as formatting it leaves out.
var rawTextTags = map[atom.Atom]bool{
	atom.Style: true, atom.Script: true, atom.Template: true, atom.Title: true,
}

// analyzeBody reads a draft's raw content.
func analyzeBody(content string) (reviewedBody, error) {
	// Parsed with scripting off, as a mail client parses it, so a noscript's contents
	// are markup here too.
	nodes, err := html.ParseFragmentWithOptions(strings.NewReader(content),
		&html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body},
		html.ParseOptionEnableScripting(false))
	if err != nil {
		return reviewedBody{}, fmt.Errorf("read the draft's body: %w", err)
	}

	w := &bodyWalker{}
	for _, n := range nodes {
		w.walk(n)
	}

	// A comment is never shown, so it isn't sent: a conditional comment is markup
	// Outlook renders (<!--[if mso]><img src=...><![endif]-->).
	for _, c := range w.comments {
		if c.Parent != nil {
			c.Parent.RemoveChild(c)
		}
	}
	var delivered bytes.Buffer
	for _, n := range nodes {
		if n.Type == html.CommentNode {
			continue
		}
		if err := html.Render(&delivered, n); err != nil {
			return reviewedBody{}, fmt.Errorf("render the draft's body: %w", err)
		}
	}
	sum := sha256.Sum256(delivered.Bytes())
	return reviewedBody{
		text:        strings.TrimSuffix(strings.ReplaceAll(w.text.String(), "\r\n", "\n"), "\n"),
		attachments: w.attachments,
		unnamed:     w.unnamed,
		images:      w.images,
		unshown:     w.unshown,
		delivered:   delivered.String(),
		hash:        hex.EncodeToString(sum[:]),
	}, nil
}

type bodyWalker struct {
	comments    []*html.Node
	text        strings.Builder
	attachments []attachment
	unnamed     int
	images      []string
	unshown     string
	// pre counts the whitespace-preserving elements the walk is inside.
	pre int
}

// flow writes text as HTML lays it out outside <pre>: each run of ASCII whitespace is
// one space, across text nodes too (a run split by inline markup or a comment is still
// one run), and none follows a line break.
func (w *bodyWalker) flow(s string) {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			if !w.spaced() {
				w.text.WriteByte(' ')
			}
			continue
		}
		w.text.WriteRune(r)
	}
}

// spaced reports whether the text so far ends in a space or a line break, or is empty.
func (w *bodyWalker) spaced() bool {
	t := w.text.String()
	return t == "" || strings.HasSuffix(t, " ") || strings.HasSuffix(t, "\n")
}

// note keeps the first markup the rendering doesn't show.
func (w *bodyWalker) note(what string) {
	if w.unshown == "" {
		w.unshown = what
	}
}

func (w *bodyWalker) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		// Shown as a mail client renders it: outside <pre>, a run of whitespace is one
		// space.
		if w.pre > 0 {
			w.text.WriteString(n.Data)
		} else {
			w.flow(n.Data)
		}
		return
	case html.CommentNode:
		w.comments = append(w.comments, n)
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.walk(c)
		}
		return
	}

	if a, isAttachment, plainBlob := attachmentOf(n); isAttachment {
		if !plainBlob {
			w.unnamed++
			w.text.WriteString("\n[attachment]\n")
			return
		}
		rebuildAttachment(n, a)
		w.attachments = append(w.attachments, a)
		w.text.WriteString("\n[attachment: " + a.Filename + "]\n")
		return
	}

	switch {
	case n.DataAtom == atom.Img:
		w.images = append(w.images, strings.TrimSpace(attrValue(n, "src")))
	case !shownTags[n.DataAtom]:
		w.note(elementName(n))
	}
	for _, attr := range n.Attr {
		shown := (n.DataAtom == atom.A && attr.Key == "href") || (n.DataAtom == atom.Img && attr.Key == "src")
		if !shown {
			w.note(attributeName(attr) + " on " + elementName(n))
		}
	}
	if rawTextTags[n.DataAtom] {
		return
	}
	if n.DataAtom == atom.Pre || n.DataAtom == atom.Listing || n.DataAtom == atom.Textarea || n.DataAtom == atom.Xmp || n.DataAtom == atom.Plaintext {
		w.pre++
		defer func() { w.pre-- }()
	}

	if n.DataAtom == atom.Br {
		w.text.WriteString("\n")
	}
	if n.DataAtom == atom.Hr {
		w.text.WriteString("\n---")
	}
	if n.DataAtom == atom.Li {
		w.text.WriteString("- ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
	if n.DataAtom == atom.A {
		if href := attrValue(n, "href"); href != "" {
			w.text.WriteString(" <" + href + ">")
		}
	}
	if blockTags[n.DataAtom] {
		w.text.WriteString("\n")
	}
}

// elementName names n for the prompt. Only an element HTML knows is named: a custom
// element's name is the draft's own text (<ignore-previous-instructions>), and the
// prompt's own lines must carry none of it.
func elementName(n *html.Node) string {
	if n.DataAtom == 0 {
		return "a custom element"
	}
	return "<" + n.DataAtom.String() + ">"
}

// attributeName names an attribute HTML knows by its key, and any other as "an
// attribute", for the same reason.
func attributeName(attr html.Attribute) string {
	if attr.Namespace != "" || atom.Lookup([]byte(attr.Key)) == 0 {
		return "an attribute"
	}
	return attr.Key
}

// attachmentOf reads a Trix attachment element: a figure carrying data-trix-attachment,
// or an action-text-attachment. It answers whether n is one, and whether it is a plain
// blob that can be delivered as its identity: one with an sgid and a filename and no
// embedded content (Trix carries an embedded email body as a filename-less text/html
// attachment whose markup is in its JSON).
func attachmentOf(n *html.Node) (attachment, bool, bool) {
	var a attachment
	var embedded bool
	switch {
	case n.DataAtom == atom.Figure && hasAttr(n, "data-trix-attachment"):
		var data map[string]any
		if json.Unmarshal([]byte(attrValue(n, "data-trix-attachment")), &data) != nil {
			return a, true, false
		}
		var attributes map[string]any
		_ = json.Unmarshal([]byte(attrValue(n, "data-trix-attributes")), &attributes)
		_, embedded = data["content"]
		a = attachment{SGID: asText(data["sgid"]), Filename: asText(data["filename"]), Size: sizeOf(data["filesize"]), ContentType: asText(data["contentType"]), Caption: asText(attributes["caption"])}
	case n.Data == "action-text-attachment":
		size, _ := strconv.ParseInt(attrValue(n, "filesize"), 10, 64)
		embedded = hasAttr(n, "content")
		a = attachment{SGID: attrValue(n, "sgid"), Filename: attrValue(n, "filename"), Size: size, ContentType: attrValue(n, "content-type"), Caption: attrValue(n, "caption")}
	default:
		return a, false, false
	}
	return a, true, !embedded && a.SGID != "" && a.Filename != ""
}

// rebuildAttachment replaces an attachment element's attributes with its identity and
// drops everything inside it, so it's delivered as Trix submits a fresh one.
func rebuildAttachment(n *html.Node, a attachment) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	if n.DataAtom == atom.Figure {
		data, _ := json.Marshal(struct {
			ContentType string `json:"contentType,omitempty"`
			Filename    string `json:"filename"`
			Filesize    int64  `json:"filesize,omitempty"`
			SGID        string `json:"sgid"`
		}{a.ContentType, a.Filename, a.Size, a.SGID})
		n.Attr = []html.Attribute{{Key: "data-trix-attachment", Val: string(data)}}
		if a.Caption != "" {
			attributes, _ := json.Marshal(map[string]string{"caption": a.Caption})
			n.Attr = append(n.Attr, html.Attribute{Key: "data-trix-attributes", Val: string(attributes)})
		}
		return
	}
	n.Attr = []html.Attribute{{Key: "sgid", Val: a.SGID}, {Key: "filename", Val: a.Filename}}
	if a.ContentType != "" {
		n.Attr = append(n.Attr, html.Attribute{Key: "content-type", Val: a.ContentType})
	}
	if a.Size > 0 {
		n.Attr = append(n.Attr, html.Attribute{Key: "filesize", Val: strconv.FormatInt(a.Size, 10)})
	}
	if a.Caption != "" {
		n.Attr = append(n.Attr, html.Attribute{Key: "caption", Val: a.Caption})
	}
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
