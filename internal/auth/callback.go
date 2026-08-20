package auth

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"text/template"
)

//go:embed *.html
var callbackFS embed.FS

var (
	callbackSuccess = mustRenderCallback("callback_success.html")
	callbackError   = mustRenderCallback("callback_error.html")
	callbackDenied  = mustRenderCallback("callback_denied.html")
	callbackInvalid = mustRenderCallback("callback_invalid.html")
)

func mustRenderCallback(filename string) string {
	page, err := renderCallback(callbackFS, filename)
	if err != nil {
		panic(err)
	}
	return page
}

// renderCallback renders one callback screen: all templates are parsed
// together so {{template "hey_logo.html"}} references resolve, the named
// content page is rendered, then wrapped in the outer shell (callback.html).
// Taking fs.FS lets the preview server render from disk.
func renderCallback(fsys fs.FS, filename string) (string, error) {
	tmpl, err := template.ParseFS(fsys, "*.html")
	if err != nil {
		return "", fmt.Errorf("parsing callback templates: %w", err)
	}
	var content bytes.Buffer
	if err := tmpl.ExecuteTemplate(&content, filename, nil); err != nil {
		return "", fmt.Errorf("rendering callback content %s: %w", filename, err)
	}
	var page bytes.Buffer
	if err := tmpl.ExecuteTemplate(&page, "callback.html", content.String()); err != nil {
		return "", fmt.Errorf("rendering callback shell: %w", err)
	}
	return page.String(), nil
}
