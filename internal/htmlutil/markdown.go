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
// quotes, code blocks and tables.
func ToMarkdown(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	m := &markdownizer{}
	m.walk(doc)
	return m.String()
}

type markdownizer struct {
	lines         []string
	line          string
	prefix        string
	pendingPrefix string
	lists         []*listLevel
	preformatted  bool
	breaking      bool
	depth         int
}

type listLevel struct {
	ordered bool
	number  int
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
		m.emphasis(n, "`")
	case "a":
		m.link(n)
	case "img":
		m.image(n)
	case "figure":
		m.figure(n)
	case "action-text-attachment":
		m.attachment(getAttr(n, "filename"), getAttr(n, "url"), getAttr(n, "content-type"))
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

func (m *markdownizer) blockquote(n *html.Node) {
	outer := m.prefix
	m.flushLine()
	m.blank()
	m.prefix = outer + "> "
	start := len(m.lines)
	m.children(n)
	m.flushLine()
	m.trimBlankLines(start)
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
	level := &listLevel{ordered: n.Data == "ol"}
	m.lists = append(m.lists, level)
	m.flushLine()
	if len(m.lists) == 1 {
		m.blank()
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "li" {
			m.listItem(child, level)
		} else {
			m.walk(child)
		}
	}
	m.flushLine()
	m.lists = m.lists[:len(m.lists)-1]
	if len(m.lists) == 0 {
		m.blank()
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
	m.pendingPrefix = outer + marker
	m.prefix = outer + strings.Repeat(" ", len(marker))
	m.children(n)
	m.flushLine()
	m.pendingPrefix = ""
	m.prefix = outer
}

func (m *markdownizer) codeBlock(n *html.Node) {
	m.flushLine()
	m.blank()
	m.write("```" + codeLanguage(n))
	m.flushLine()

	m.preformatted = true
	m.children(n)
	m.preformatted = false
	m.flushLine()

	m.write("```")
	m.flushLine()
	m.blank()
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

func (m *markdownizer) table(n *html.Node) {
	rows := tableRows(n)
	if len(rows) == 0 {
		return
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

func tableRows(n *html.Node) [][]string {
	var rows [][]string
	if n.Type == html.ElementNode && n.Data == "tr" {
		var cells []string
		for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
				cells = append(cells, strings.ReplaceAll(inlineMarkdown(cell), "|", "\\|"))
			}
		}
		if len(cells) > 0 {
			return [][]string{cells}
		}
		return nil
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rows = append(rows, tableRows(child)...)
	}
	return rows
}

func (m *markdownizer) emphasis(n *html.Node, delimiter string) {
	if m.preformatted {
		m.children(n)
		return
	}

	m.inline(n, func(inner string) string {
		if inner == "" {
			return ""
		}
		return delimiter + inner + delimiter
	})
}

func (m *markdownizer) link(n *html.Node) {
	href := getAttr(n, "href")
	m.inline(n, func(text string) string {
		switch {
		case href == "":
			return text
		case text == "" || text == href:
			return "<" + href + ">"
		default:
			return "[" + text + "](" + href + ")"
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

func (m *markdownizer) image(n *html.Node) {
	src := getAttr(n, "src")
	if src == "" {
		return
	}
	m.write("![" + getAttr(n, "alt") + "](" + src + ")")
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
	if filename == "" {
		filename = "attachment"
	}
	m.block(func() {
		if isImageContentType(contentType) && url != "" {
			m.write("![" + filename + "](" + url + ")")
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
	m.line += s
}

// writeSpace keeps the single space that separates inline runs without letting
// the whitespace HTML sprinkles between tags pile up.
func (m *markdownizer) writeSpace() {
	if m.line != "" && !strings.HasSuffix(m.line, " ") {
		m.write(" ")
	}
}

func (m *markdownizer) writeText(s string) {
	if m.preformatted {
		for i, chunk := range strings.Split(s, "\n") {
			if i > 0 {
				m.flushLine()
			}
			m.write(chunk)
		}
		return
	}

	collapsed := strings.Join(strings.Fields(s), " ")
	if collapsed == "" || startsWithSpace(s) {
		m.writeSpace()
	}
	if collapsed == "" {
		return
	}

	m.write(collapsed)
	if endsWithSpace(s) {
		m.writeSpace()
	}
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
	m.breaking = m.line != ""
	m.flushLine()
}

func (m *markdownizer) flushLine() {
	content := strings.TrimRight(m.line, " \t")
	breaking := m.breaking
	m.line = ""
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
