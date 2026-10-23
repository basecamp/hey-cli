package mcpserver

import (
	"bytes"
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
// attachments, any reference a mail client would load by itself, the body it delivers,
// and the hash the confirmation binds.
//
// The body is the Trix editing HTML GetMessageEdit serves. Its attachments are
// <figure data-trix-attachment="{json}"> (or <action-text-attachment>) elements whose
// JSON names the blob's sgid, filename, size and type beside a signed URL, wrapped
// around a preview image and a caption HEY renders from the blob.
//
// An attachment is delivered as its identity alone: the element is rebuilt from the
// sgid, filename, size, type and caption, with nothing inside it, which is what HEY
// keeps of one anyway (Action Text resolves an attachment from those attributes and
// renders its markup afresh). Nothing in the wrapper (a preview URL, a caption's
// markup, an attribute) is sent, so none of it has to be checked, shown or bound. The
// hash is over exactly the body that is delivered.

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
	// unverifiable names the first attachment that isn't a plain blob (no sgid or
	// filename, or embedded HTML content, as Trix carries an embedded email body), or
	// "". It can't be delivered as an identity, so such a draft isn't sent.
	unverifiable string
	// delivered is the body send_draft sends: the draft's, with each attachment rebuilt
	// from its identity.
	delivered string
	// hash is the sha256 of delivered. Two fetches of an unchanged draft hash alike
	// (signed URLs aren't in it), and a changed attachment doesn't.
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

// loadingTags fetch something when a mail client renders them, can make it, or hold
// markup a client may parse differently (noscript, template and the raw-text
// fallbacks), which is refused rather than second-guessed.
var loadingTags = map[atom.Atom]bool{
	atom.Img: true, atom.Image: true, atom.Iframe: true, atom.Frame: true, atom.Video: true,
	atom.Audio: true, atom.Source: true, atom.Track: true, atom.Embed: true, atom.Object: true,
	atom.Link: true, atom.Picture: true, atom.Svg: true, atom.Style: true, atom.Script: true,
	atom.Meta: true, atom.Base: true, atom.Input: true, atom.Noscript: true, atom.Template: true,
	atom.Noembed: true, atom.Noframes: true, atom.Xmp: true, atom.Plaintext: true, atom.Math: true,
}

// loadingAttributes name a URL a client loads by itself.
var loadingAttributes = []string{"src", "srcset", "background", "poster", "lowsrc", "dynsrc", "data", "xlink:href", "action", "formaction"}

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

	w := &bodyWalker{lossless: true}
	for _, n := range nodes {
		w.walk(n)
	}

	var delivered bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&delivered, n); err != nil {
			return reviewedBody{}, fmt.Errorf("render the draft's body: %w", err)
		}
	}
	sum := sha256.Sum256(delivered.Bytes())
	return reviewedBody{
		text:         tidyText(w.text.String()),
		lossless:     w.lossless && len(w.attachments) == 0,
		attachments:  w.attachments,
		remote:       w.remote,
		unverifiable: w.unverifiable,
		delivered:    delivered.String(),
		hash:         hex.EncodeToString(sum[:]),
	}, nil
}

type bodyWalker struct {
	text         strings.Builder
	lossless     bool
	attachments  []attachment
	remote       string
	unverifiable string
}

func (w *bodyWalker) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text.WriteString(n.Data)
		return
	case html.CommentNode:
		w.lossless = false
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.walk(c)
		}
		return
	}

	if a, isAttachment, problem := attachmentOf(n); isAttachment {
		if problem != "" {
			if w.unverifiable == "" {
				w.unverifiable = problem
			}
			return
		}
		rebuildAttachment(n, a)
		w.attachments = append(w.attachments, a)
		w.text.WriteString("\n[attachment: " + a.Filename + "]\n")
		return
	}

	if !plainTags[n.DataAtom] {
		w.lossless = false
	}
	if loadingTags[n.DataAtom] && w.remote == "" {
		w.remote = "<" + n.Data + ">"
	}
	for _, attr := range n.Attr {
		if n.DataAtom != atom.A || attr.Key != "href" {
			w.lossless = false
		}
		if w.remote == "" && loadsByItself(attr) {
			w.remote = attr.Key + " on <" + n.Data + ">"
		}
	}

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
}

// loadsByItself reports whether an attribute makes a mail client fetch a URL on open:
// a loading attribute, or a style that names one.
func loadsByItself(attr html.Attribute) bool {
	key := strings.ToLower(attr.Key)
	if slices.Contains(loadingAttributes, key) {
		return true
	}
	if key == "style" {
		// CSS escapes spell url( without its letters (u\72l(...)), so a style with an
		// escape, or any function call at all, counts as loading. Trix writes no style.
		value := strings.ToLower(attr.Val)
		return strings.ContainsAny(value, "\\(") || strings.Contains(value, "@import")
	}
	return false
}

// attachmentOf reads a Trix attachment element: a figure carrying data-trix-attachment,
// or an action-text-attachment. It answers whether n is one, and a problem when it is
// one that isn't a plain blob: no sgid or filename, or embedded content (Trix carries
// an embedded email body as a filename-less text/html attachment whose markup is in
// its JSON).
func attachmentOf(n *html.Node) (attachment, bool, string) {
	var a attachment
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
	case n.Data == "action-text-attachment":
		size, _ := strconv.ParseInt(attrValue(n, "filesize"), 10, 64)
		embedded = hasAttr(n, "content")
		a = attachment{SGID: attrValue(n, "sgid"), Filename: attrValue(n, "filename"), Size: size, ContentType: attrValue(n, "content-type"), Caption: attrValue(n, "caption")}
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
	return a, true, ""
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
