package htmlutil

import (
	"bytes"
	stdhtml "html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// fromMarkdown converts what the author typed for one of HEY's rich-text fields.
// Hard wraps keep a message breaking where it was typed, and raw HTML passes
// through because the author could send it verbatim without this conversion.
var fromMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough),
	goldmark.WithRendererOptions(
		htmlrenderer.WithHardWraps(),
		htmlrenderer.WithUnsafe(),
		renderer.WithNodeRenderers(util.Prioritized(&trixCodeBlockRenderer{}, 100)),
	),
)

// trixLanguages maps a fence's info string to the language names HEY's own code
// blocks carry, which is the set its server-side highlighter accepts.
var trixLanguages = map[string]string{
	"html": "html", "css": "css",
	"javascript": "javascript", "js": "javascript",
	"typescript": "typescript", "ts": "typescript",
	"python": "python", "py": "python",
	"ruby": "ruby", "rb": "ruby",
	"java": "java", "php": "php", "swift": "swift",
	"csharp": "csharp", "c#": "csharp", "cs": "csharp",
	"go": "go", "golang": "go",
	"rust": "rust", "rs": "rust",
	"cpp": "cpp", "c++": "cpp",
}

// trixCodeBlockRenderer writes a fenced code block the way HEY's editor stores one:
// a <pre> carrying the language as its own attribute, which is what the web app's
// syntax highlighting reads.
type trixCodeBlockRenderer struct{}

func (r *trixCodeBlockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
}

func (r *trixCodeBlockRenderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code></pre>\n")
		return ast.WalkContinue, nil
	}
	block, ok := node.(*ast.FencedCodeBlock)
	if !ok {
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString("<pre")
	if language, ok := trixLanguages[strings.ToLower(string(block.Language(source)))]; ok {
		_, _ = w.WriteString(` language="` + language + `"`)
	}
	_, _ = w.WriteString("><code>")
	lines := block.Lines()
	for i := range lines.Len() {
		line := lines.At(i)
		_, _ = w.Write(util.EscapeHTML(line.Value(source)))
	}
	return ast.WalkContinue, nil
}

// FromMarkdown converts Markdown the user wrote into HTML for one of HEY's
// rich-text fields: a message body, a journal entry, a contact note, a snippet.
func FromMarkdown(md string) string {
	var buf bytes.Buffer
	if err := fromMarkdown.Convert([]byte(md), &buf); err != nil {
		return textAsHTML(md)
	}
	return strings.TrimSpace(buf.String())
}

func textAsHTML(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	escaped := stdhtml.EscapeString(text)
	return strings.ReplaceAll(escaped, "\n", "<br>")
}
