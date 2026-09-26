package htmlutil

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// roundTripsThroughMarkdown reports whether s, converted to Markdown and back, shows
// the same thing: the same text in the same blocks — paragraphs, headings, quotes, list
// items, code — with the same bold, italics, strikethrough, code and links. It compares
// what a reader sees rather than the markup, so HEY's editor writing a <div> with two
// <br>s where FromMarkdown writes two <p>s is no difference, and a label ToMarkdown
// rewrites, a list item it cannot place or formatting it cannot nest is.
func roundTripsThroughMarkdown(s string) bool {
	back := FromMarkdown(ToMarkdown(s).String())
	return slices.Equal(canonicalBlocks(s), canonicalBlocks(back))
}

// canonicalBlocks describes HTML as the blocks it shows, one string each: where the
// block stands, then its text in runs of one style.
func canonicalBlocks(s string) []string {
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), context)
	if err != nil {
		return nil
	}
	c := &canonicalizer{}
	for _, node := range nodes {
		c.walk(node, canonicalPlace{}, "")
	}
	c.endBlock()
	return c.blocks
}

// canonicalPlace is where a block stands: in which quotes, list items and heading, and
// whether it is a code block.
type canonicalPlace struct {
	quotes  int
	items   string
	heading string
	pre     bool
}

func (p canonicalPlace) String() string {
	return fmt.Sprintf("q%d %s %s pre=%v", p.quotes, p.items, p.heading, p.pre)
}

type canonicalToken struct {
	text  rune
	style string
	brk   bool
}

type canonicalizer struct {
	blocks  []string
	place   canonicalPlace
	tokens  []canonicalToken
	spacing bool
	// spaceRun and nonBreaking describe the whitespace since the last thing written,
	// across element boundaries: a run a non-breaking space holds open shows wider
	// than one space.
	spaceRun    int
	nonBreaking bool
	// blank records a break or a non-breaking space in the block, which makes a block
	// with no text in it a line that shows.
	blank bool
}

func (c *canonicalizer) walk(n *html.Node, place canonicalPlace, style string) {
	switch n.Type { //nolint:exhaustive // only text and element nodes carry content
	case html.TextNode:
		c.text(n.Data, place, style)
		return
	case html.ElementNode:
	default:
		c.children(n, place, style)
		return
	}

	switch n.Data {
	case "br":
		if place.pre {
			c.emit('\n', "", place)
		} else {
			c.emitBreak(place)
		}
	case "hr":
		c.endBlock()
		c.place = place
		c.tokens = append(c.tokens, canonicalToken{text: '—', style: "hr"})
		c.endBlock()
	case "p", "div":
		c.block(n, place, style)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		place.heading = n.Data
		c.block(n, place, style)
	case "blockquote":
		place.quotes++
		c.block(n, place, style)
	case "pre":
		place.pre = true
		c.block(n, place, "")
	case "ul", "ol":
		// Every list counts, not only the items: a list straight inside a list indents
		// its items a level that Markdown can only write inside an item.
		place.items += "(" + n.Data + ")"
		c.block(n, place, style)
	case "li":
		place.items += listItemMark(n)
		c.block(n, place, style)
	case "strong", "b":
		c.children(n, place, withStyle(style, "b"))
	case "em", "i":
		c.children(n, place, withStyle(style, "i"))
	case "del", "s", "strike":
		c.children(n, place, withStyle(style, "s"))
	case "code":
		if place.pre {
			c.children(n, place, style)
		} else {
			c.children(n, place, withStyle(style, "c"))
		}
	case "a":
		c.children(n, place, withStyle(style, "a="+getAttr(n, "href")))
	default:
		c.children(n, place, style)
	}
}

func (c *canonicalizer) children(n *html.Node, place canonicalPlace, style string) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.walk(child, place, style)
	}
}

func (c *canonicalizer) block(n *html.Node, place canonicalPlace, style string) {
	c.endBlock()
	c.children(n, place, style)
	c.endBlock()
}

// listItemMark names a list item by its list's kind and its position, so two items
// never read as one and an item outside any list reads as neither kind.
func listItemMark(n *html.Node) string {
	kind := "-"
	if n.Parent != nil && (n.Parent.Data == "ul" || n.Parent.Data == "ol") {
		kind = n.Parent.Data
	}
	position := 0
	for sibling := n.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
		if sibling.Type == html.ElementNode && sibling.Data == "li" {
			position++
		}
	}
	return fmt.Sprintf("[%s%d]", kind, position)
}

func withStyle(style, add string) string {
	styles := strings.Split(style, "\x00")
	if slices.Contains(styles, add) {
		return style
	}
	styles = append(styles, add)
	slices.Sort(styles)
	return strings.Trim(strings.Join(styles, "\x00"), "\x00")
}

func (c *canonicalizer) text(s string, place canonicalPlace, style string) {
	for _, r := range s {
		// A non-breaking space on its own shows as a space, which is what ToMarkdown
		// writes for one; a run it holds open shows wider, and a block of nothing else
		// is a blank line.
		if !place.pre && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\u00a0') {
			if r == '\u00a0' {
				c.startBlock(place)
				c.nonBreaking = true
				c.blank = true
			}
			c.spacing = true
			c.spaceRun++
			continue
		}
		c.emit(r, style, place)
	}
}

func (c *canonicalizer) emit(r rune, style string, place canonicalPlace) {
	c.startBlock(place)
	if c.spacing && len(c.tokens) > 0 && !c.tokens[len(c.tokens)-1].brk {
		// A space belongs to no style: "**A** B" and "**A B**" show the same.
		space := ' '
		if c.spaceRun > 1 && c.nonBreaking {
			space = '\u00a0'
		}
		c.tokens = append(c.tokens, canonicalToken{text: space})
	}
	c.resetSpacing()
	c.tokens = append(c.tokens, canonicalToken{text: r, style: style})
}

func (c *canonicalizer) emitBreak(place canonicalPlace) {
	c.startBlock(place)
	c.resetSpacing()
	c.blank = true
	c.tokens = append(c.tokens, canonicalToken{brk: true})
}

func (c *canonicalizer) resetSpacing() {
	c.spacing = false
	c.spaceRun = 0
	c.nonBreaking = false
}

// startBlock opens a block where the first thing in it is written; a block whose
// place changes without a block element around it — text after a list, say — starts
// a new one.
func (c *canonicalizer) startBlock(place canonicalPlace) {
	if len(c.tokens) > 0 && c.place != place {
		c.endBlock()
	}
	if len(c.tokens) == 0 {
		c.place = place
	}
}

// endBlock writes the block out. Two breaks in a row end a paragraph and start another;
// a third is a blank line more, kept as a line of its own; one break is a line break;
// breaks that end the block show nothing and are dropped.
func (c *canonicalizer) endBlock() {
	tokens := c.tokens
	blank := c.blank
	c.tokens = nil
	c.blank = false
	c.resetSpacing()
	for len(tokens) > 0 && tokens[len(tokens)-1].brk {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		if blank {
			// A block of breaks or non-breaking spaces and no text is a blank line.
			c.blocks = append(c.blocks, c.place.String()+" blank")
		}
		return
	}

	var paragraph []canonicalToken
	for i := 0; i < len(tokens); {
		if !tokens[i].brk {
			paragraph = append(paragraph, tokens[i])
			i++
			continue
		}
		run := 0
		for i < len(tokens) && tokens[i].brk {
			run++
			i++
		}
		if run == 1 || len(paragraph) == 0 {
			// A break with nothing before it keeps its lines: Markdown cannot write one.
			for range run {
				paragraph = append(paragraph, canonicalToken{text: '\n'})
			}
			continue
		}
		c.writeParagraph(paragraph)
		paragraph = nil
		for range run - 2 {
			paragraph = append(paragraph, canonicalToken{text: '\n'})
		}
	}
	c.writeParagraph(paragraph)
}

func (c *canonicalizer) writeParagraph(tokens []canonicalToken) {
	// A space against a line break shows nothing, outside a code block.
	var kept []canonicalToken
	for i, token := range tokens {
		if !c.place.pre && token.text == ' ' && token.style == "" &&
			(i == 0 || i == len(tokens)-1 || tokens[i-1].text == '\n' || tokens[i+1].text == '\n') {
			continue
		}
		kept = append(kept, token)
	}
	if c.place.pre {
		// A code block's last newline is where it ends, not a line in it.
		if n := len(kept); n > 0 && kept[n-1].text == '\n' {
			kept = kept[:n-1]
		}
	}
	if len(kept) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString(c.place.String())
	style := "\x01"
	for _, token := range kept {
		if token.style != style && (token.text != ' ' || c.place.pre) {
			style = token.style
			b.WriteString("\x02" + style + "\x03")
		}
		b.WriteRune(token.text)
	}
	c.blocks = append(c.blocks, b.String())
}
