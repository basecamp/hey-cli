package tui

import "github.com/basecamp/hey-cli/internal/htmlutil"

func htmlToText(s string) string {
	return htmlutil.ToText(s)
}

func htmlToMarkdown(s string) htmlutil.Markdown {
	return htmlutil.ToMarkdown(s)
}

func extractImageURLs(s string) []string {
	return htmlutil.ExtractImageURLs(s)
}
