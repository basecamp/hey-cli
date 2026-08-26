package htmlutil

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// ToMarkdown converts HTML content to Markdown. Unlike ToText it keeps what a
// flattened email throws away: link URLs, emphasis, headings, list nesting,
// quotes, code blocks and tables. What it returns is the only Markdown
// markdown.Render accepts; see the Markdown type.
func ToMarkdown(s string) Markdown {
	return Markdown{text: toMarkdown(s)}
}

func toMarkdown(s string) string {
	m := &markdownizer{}
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		m.writeText(s)
	} else {
		m.walk(doc)
	}
	return m.String()
}

type markdownizer struct {
	lines         []string
	line          strings.Builder
	prefix        string
	pendingPrefix string
	lists         []*listLevel
	breaking      bool
	depth         int
	quoteDepth    int
}

// listLevel is one level of list nesting: its kind, its count so far, and the prefix
// its items' markers sit behind, which is the prefix in force where the list began.
type listLevel struct {
	ordered bool
	number  int
	prefix  string
}

func inlineMarkdown(n *html.Node) string {
	m := &markdownizer{}
	m.children(n)
	m.flushLine()
	return strings.TrimSpace(strings.Join(m.lines, " "))
}

func (m *markdownizer) String() string {
	m.flushLine()
	result := strings.Join(m.lines, "\n")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

func (m *markdownizer) walk(n *html.Node) {
	switch n.Type { //nolint:exhaustive // only text and element nodes carry content
	case html.TextNode:
		m.writeText(n.Data)
	case html.ElementNode:
		m.element(n)
	default:
		m.children(n)
	}
}

func (m *markdownizer) element(n *html.Node) {
	switch n.Data {
	case "script", "style", "head", "title", "noscript":
		return
	case "br":
		m.hardBreak()
	case "hr":
		m.block(func() { m.write("---") })
	case "h1", "h2", "h3", "h4", "h5", "h6":
		m.heading(n)
	case "p", "div", "section", "article", "header", "footer", "tbody", "thead":
		m.block(func() { m.children(n) })
	case "blockquote":
		m.blockquote(n)
	case "ul", "ol":
		m.list(n)
	case "li":
		m.children(n)
	case "pre":
		m.codeBlock(n)
	case "table":
		m.table(n)
	case "strong", "b":
		m.emphasis(n, "**")
	case "em", "i":
		m.emphasis(n, "*")
	case "del", "s", "strike":
		m.emphasis(n, "~~")
	case "code":
		m.code(n)
	case "a":
		m.link(n)
	case "img":
		m.image(n)
	case "figure":
		m.figure(n)
	case "action-text-attachment":
		m.renderActionTextAttachment(n)
	default:
		m.children(n)
	}
}

func (m *markdownizer) heading(n *html.Node) {
	depth := int(n.Data[1] - '0')
	m.block(func() {
		m.write(strings.Repeat("#", depth) + " ")
		m.children(n)
	})
}

// maxNestingDepth is how deep quotes and lists nest before a deeper one is rendered at
// the same level. glamour's cost grows exponentially with quote depth and faster than
// linearly with list depth, and a reply chain quoting itself a hundred deep is either
// an attack or unreadable.
const maxNestingDepth = 16

func (m *markdownizer) blockquote(n *html.Node) {
	if m.quoteDepth >= maxNestingDepth {
		m.block(func() { m.children(n) })
		return
	}

	outer := m.prefix
	m.flushLine()
	m.blank()
	m.prefix = outer + "> "
	m.quoteDepth++
	start := len(m.lines)
	m.children(n)
	m.flushLine()
	m.trimBlankLines(start)
	m.quoteDepth--
	m.prefix = outer
	m.blank()
}

func (m *markdownizer) trimBlankLines(start int) {
	blank := strings.TrimRight(m.prefix, " ")
	for len(m.lines) > start && m.lines[len(m.lines)-1] == blank {
		m.lines = m.lines[:len(m.lines)-1]
	}
	for len(m.lines) > start && m.lines[start] == blank {
		m.lines = append(m.lines[:start], m.lines[start+1:]...)
	}
}

func (m *markdownizer) list(n *html.Node) {
	if len(m.lists) >= maxNestingDepth {
		// A list nested past the cap is rendered at the current level: each of its
		// items is still an item, with its marker and its own line.
		m.listItems(n, m.lists[len(m.lists)-1])
		return
	}

	level := &listLevel{ordered: n.Data == "ol", prefix: m.prefix}
	m.lists = append(m.lists, level)
	m.flushLine()
	if len(m.lists) == 1 {
		m.blank()
	}
	m.listItems(n, level)
	m.flushLine()
	m.lists = m.lists[:len(m.lists)-1]
	if len(m.lists) == 0 {
		m.blank()
	}
}

func (m *markdownizer) listItems(n *html.Node, level *listLevel) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "li" {
			m.listItem(child, level)
		} else {
			m.walk(child)
		}
	}
}

func (m *markdownizer) listItem(n *html.Node, level *listLevel) {
	marker := "- "
	if level.ordered {
		level.number++
		marker = fmt.Sprintf("%d. ", level.number)
	}

	outer := m.prefix
	m.flushLine()
	m.pendingPrefix = level.prefix + marker
	m.prefix = level.prefix + strings.Repeat(" ", len(marker))
	m.children(n)
	m.flushLine()
	m.pendingPrefix = ""
	m.prefix = outer
}

// codeBlock writes preformatted text inside a fence no line of it can close. Backslashes
// mean nothing in a fence, so the text is carried verbatim once its controls are gone.
func (m *markdownizer) codeBlock(n *html.Node) {
	content := strings.Trim(stripControls(preformattedText(n)), "\n")
	fence := codeFence(content)

	m.flushLine()
	m.blank()
	m.write(fence + fenceInfo(codeLanguage(n)))
	m.flushLine()
	for _, line := range strings.Split(content, "\n") {
		m.rawLine(line)
	}
	m.write(fence)
	m.flushLine()
	m.blank()
}

// preformattedText is the text of a <pre>, with a <br> as the newline it stands for.
func preformattedText(n *html.Node) string {
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		switch n.Type { //nolint:exhaustive // only text and element nodes carry content
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.ElementNode:
			switch n.Data {
			case "script", "style":
				return
			case "br":
				b.WriteByte('\n')
				return
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return b.String()
}

func codeLanguage(n *html.Node) string {
	if language := getAttr(n, "language"); language != "" {
		return language
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "code" {
			for _, class := range strings.Fields(getAttr(child, "class")) {
				if language, found := strings.CutPrefix(class, "language-"); found {
					return language
				}
			}
		}
	}
	return ""
}

// table writes a data table as a pipe table. HTML email lays whole pages out in
// tables, and a pipe table cannot hold what those cells hold — headings, lists,
// nested tables, spanned cells — so a table that is scaffolding rather than data
// renders each cell as the block flow it contains instead.
func (m *markdownizer) table(n *html.Node) {
	cells := tableCells(n)
	if len(cells) == 0 {
		return
	}
	if isLayoutTable(n, cells) {
		m.layoutTable(n)
		return
	}

	rows := make([][]string, len(cells))
	for i, row := range cells {
		for _, cell := range row {
			rows[i] = append(rows[i], inlineMarkdown(cell))
		}
	}

	m.flushLine()
	m.blank()
	m.write("| " + strings.Join(rows[0], " | ") + " |")
	m.flushLine()
	m.write("|" + strings.Repeat(" --- |", len(rows[0])))
	m.flushLine()
	for _, row := range rows[1:] {
		for len(row) < len(rows[0]) {
			row = append(row, "")
		}
		m.write("| " + strings.Join(row[:len(rows[0])], " | ") + " |")
		m.flushLine()
	}
	m.blank()
}

func tableCells(n *html.Node) [][]*html.Node {
	if n.Type == html.ElementNode && n.Data == "tr" {
		var cells []*html.Node
		for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
				cells = append(cells, cell)
			}
		}
		if len(cells) > 0 {
			return [][]*html.Node{cells}
		}
		return nil
	}
	var rows [][]*html.Node
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rows = append(rows, tableCells(child)...)
	}
	return rows
}

func isLayoutTable(n *html.Node, cells [][]*html.Node) bool {
	if role := getAttr(n, "role"); role == "presentation" || role == "none" {
		return true
	}
	columns := 0
	for _, row := range cells {
		if len(row) > columns {
			columns = len(row)
		}
		for _, cell := range row {
			if getAttr(cell, "colspan") != "" || getAttr(cell, "rowspan") != "" || holdsBlockContent(cell) {
				return true
			}
		}
	}
	return columns < 2
}

// holdsBlockContent reports whether a cell holds content a pipe-table cell
// cannot: block elements that flattening to one line would destroy. A <p> or a
// <div> is not counted — a wrapped cell value flattens without loss.
func holdsBlockContent(n *html.Node) bool {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && (blockContentTags[child.Data] || holdsBlockContent(child)) {
			return true
		}
	}
	return false
}

var blockContentTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "table": true, "pre": true, "blockquote": true,
	"figure": true, "hr": true, "action-text-attachment": true,
}

func (m *markdownizer) layoutTable(n *html.Node) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			m.walk(child)
			continue
		}
		switch child.Data {
		case "td", "th":
			m.block(func() { m.children(child) })
		case "tbody", "thead", "tfoot", "tr":
			m.layoutTable(child)
		default:
			m.walk(child)
		}
	}
}

func (m *markdownizer) emphasis(n *html.Node, delimiter string) {
	m.inline(n, func(inner string) string {
		if inner == "" {
			return ""
		}
		return delimiter + inner + delimiter
	})
}

// code writes inline code. Its text is not prose: nothing in it is Markdown, so
// nothing in it is escaped, and the delimiters are sized around it.
func (m *markdownizer) code(n *html.Node) {
	leading, trailing := surroundingSpace(n)
	formatted := codeSpan(elementText(n))

	if leading {
		m.writeSpace()
	}
	m.write(formatted)
	if trailing {
		m.writeSpace()
	}
}

// link writes an anchor. A destination that cannot be linked leaves its text behind as
// prose, and a label that is itself a URL never hides the destination it points at: the
// destination is written out beside it, where a reader comparing the two can see both.
// Only a label that is its href, character for character, collapses into an autolink,
// so the visible host is the destination's host by construction — a label of
// "https://pаypal.com" with a Cyrillic а pointed at https://evil.example is written
// `[https://pаypal.com](https://evil.example)`, and no confusable table is needed to
// get there. Whether the label's host is itself a homoglyph of something is not
// detected; see the terminal package's policy.
func (m *markdownizer) link(n *html.Node) {
	href := getAttr(n, "href")
	dest, linkable := destination(href)
	m.inline(n, func(text string) string {
		switch {
		case !linkable:
			return text
		case text == "" || strings.TrimSpace(elementText(n)) == strings.TrimSpace(href):
			if absolute(dest) {
				return "<" + dest + ">"
			}
			return "[" + escapeText(dest, m.line.String()) + "](" + dest + ")"
		default:
			return "[" + text + "](" + dest + ")"
		}
	})
}

// inline writes one inline element, keeping any whitespace that sat at its
// edges outside the Markdown it produces. Markdown cannot carry that space
// inside the delimiters — "** bold **" is literal asterisks — and dropping it
// runs the element into the words either side of it.
func (m *markdownizer) inline(n *html.Node, format func(inner string) string) {
	leading, trailing := surroundingSpace(n)
	formatted := format(inlineMarkdown(n))

	if leading {
		m.writeSpace()
	}
	m.write(formatted)
	if trailing {
		m.writeSpace()
	}
}

func surroundingSpace(n *html.Node) (leading, trailing bool) {
	text := elementText(n)
	if text == "" {
		return false, false
	}
	return startsWithSpace(text), endsWithSpace(text)
}

func elementText(n *html.Node) string {
	var b strings.Builder
	collectText(n, &b)
	return b.String()
}

func collectText(n *html.Node, b *strings.Builder) {
	switch n.Type { //nolint:exhaustive // only text and element nodes carry content
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		if n.Data == "script" || n.Data == "style" {
			return
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		collectText(child, b)
	}
}

// image writes an image, or — when its source may not be linked — its alt text as the
// prose it then is, escaped against the line it lands on rather than as a label.
func (m *markdownizer) image(n *html.Node) {
	alt := strings.Join(strings.Fields(getAttr(n, "alt")), " ")
	src, linkable := destination(getAttr(n, "src"))
	switch {
	case linkable:
		m.write("![" + escapeText(alt, "alt") + "](" + src + ")")
	case alt != "":
		m.write(escapeText(alt, m.line.String()))
	}
}

// renderActionTextAttachment renders unnamed inline content as embedded HTML while
// preserving named elements as file attachments.
func (m *markdownizer) renderActionTextAttachment(n *html.Node) {
	if content := embeddedActionTextContent(n); content != "" {
		m.embedded(content)
		return
	}
	m.attachment(getAttr(n, "filename"), getAttr(n, "url"), getAttr(n, "content-type"))
}

func (m *markdownizer) figure(n *html.Node) {
	attachment := parseTrixAttachment(n)
	switch {
	case attachment == nil:
		m.children(n)
	case attachment.Filename != "":
		m.attachment(attachment.Filename, attachment.URL, attachment.ContentType)
	case attachment.Content != "":
		m.embedded(attachment.Content)
	default:
		m.children(n)
	}
}

// embedded renders the markup HEY tucks into a text/html Trix attachment. An
// inbound HTML email that Trix cannot represent arrives entirely this way, so
// skipping it leaves the body blank and only HEY's truncated summary showing.
func (m *markdownizer) embedded(content string) {
	doc := parseEmbeddedContent(content, m.depth)
	if doc == nil {
		return
	}
	m.depth++
	m.walk(doc)
	m.depth--
}

func (m *markdownizer) attachment(filename, url, contentType string) {
	filename = escapeText(strings.Join(strings.Fields(filename), " "), "📎 ")
	if filename == "" {
		filename = "attachment"
	}
	dest, linkable := destination(url)
	m.block(func() {
		if isImageContentType(contentType) && linkable {
			m.write("![" + filename + "](" + dest + ")")
		} else {
			m.write("📎 " + filename)
		}
	})
}

func (m *markdownizer) children(n *html.Node) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		m.walk(child)
	}
}

func (m *markdownizer) block(render func()) {
	m.flushLine()
	m.blank()
	render()
	m.flushLine()
	m.blank()
}

func (m *markdownizer) write(s string) {
	m.line.WriteString(s)
}

// writeSpace keeps the single space that separates inline runs without letting
// the whitespace HTML sprinkles between tags pile up.
func (m *markdownizer) writeSpace() {
	if m.line.Len() > 0 && !strings.HasSuffix(m.line.String(), " ") {
		m.write(" ")
	}
}

func (m *markdownizer) writeText(s string) {
	collapsed := strings.Join(strings.Fields(s), " ")
	if collapsed == "" || startsWithSpace(s) {
		m.writeSpace()
	}
	if collapsed == "" {
		return
	}

	m.write(escapeText(collapsed, m.line.String()))
	if endsWithSpace(s) {
		m.writeSpace()
	}
}

// rawLine writes one line of preformatted text as its own line of output, blank lines
// included: flushLine drops those, and inside a fence they are content.
func (m *markdownizer) rawLine(line string) {
	m.write(line)
	if strings.TrimSpace(line) == "" && m.pendingPrefix == "" {
		m.lines = append(m.lines, strings.TrimRight(m.prefix, " "))
		m.line.Reset()
		return
	}
	m.flushLine()
}

// startsWithSpace and endsWithSpace decode a rune rather than inspect a byte:
// email is full of &nbsp;, which is two bytes wide and which strings.Fields
// strips, so a byte-wise check drops the space without collapsing putting one
// back.
func startsWithSpace(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(first)
}

func endsWithSpace(s string) bool {
	last, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(last)
}

func (m *markdownizer) hardBreak() {
	m.breaking = m.line.Len() > 0
	m.flushLine()
}

func (m *markdownizer) flushLine() {
	// A joiner kept at the end of a prose run for the run after it (sanitizeProse) has
	// nothing to join once the line ends.
	content := strings.TrimRight(m.line.String(), " \t\u200c\u200d")
	breaking := m.breaking
	m.line.Reset()
	m.breaking = false
	if content == "" && m.pendingPrefix == "" {
		return
	}
	if breaking {
		content += "  "
	}

	prefix := m.prefix
	if m.pendingPrefix != "" {
		prefix = m.pendingPrefix
		m.pendingPrefix = ""
	}
	if breaking {
		m.lines = append(m.lines, prefix+content)
	} else {
		m.lines = append(m.lines, strings.TrimRight(prefix+content, " "))
	}
}

func (m *markdownizer) blank() {
	if len(m.lists) > 0 || len(m.lines) == 0 {
		return
	}
	if last := m.lines[len(m.lines)-1]; last == strings.TrimRight(m.prefix, " ") {
		return
	}
	m.lines = append(m.lines, strings.TrimRight(m.prefix, " "))
}
