package htmlutil

import (
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/basecamp/hey-cli/internal/models"
)

// SearchPage is a parsed page from HEY's HTML advanced-search view.
type SearchPage struct {
	Results  []models.SearchResult
	NextPage int
}

// ParseSearchResultsHTML extracts topic results and pagination from
// /advanced_search. HEY's former /search.json route returns HTTP 406 on the
// live service, so search currently uses the HTML view.
func ParseSearchResultsHTML(source string) SearchPage {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return SearchPage{}
	}

	page := SearchPage{}
	walkElements(doc, func(node *html.Node) {
		switch {
		case node.Data == "article" && hasClass(node, "search-result"):
			if result, ok := parseSearchResult(node); ok {
				page.Results = append(page.Results, result)
			}
		case node.Data == "a" && hasClass(node, "pagination-link"):
			if next := pageNumber(getAttr(node, "href")); next > page.NextPage {
				page.NextPage = next
			}
		}
	})
	return page
}

func parseSearchResult(article *html.Node) (models.SearchResult, bool) {
	anchor := findElement(article, func(node *html.Node) bool {
		return node.Data == "a" && topicID(getAttr(node, "href")) > 0
	})
	if anchor == nil {
		return models.SearchResult{}, false
	}

	href := getAttr(anchor, "href")
	result := models.SearchResult{
		ID:     topicID(href),
		AppURL: href,
	}
	if subject := findElement(anchor, func(node *html.Node) bool {
		return hasClass(node, "search-topic__title")
	}); subject != nil {
		result.Subject = visibleText(subject)
	}
	if summary := findElement(article, func(node *html.Node) bool {
		return hasClass(node, "search-result__summary")
	}); summary != nil {
		result.Summary = visibleText(summary)
	}
	if timestamp := findElement(article, func(node *html.Node) bool {
		return node.Data == "time" && hasClass(node, "search-result__timestamp")
	}); timestamp != nil {
		result.ActiveAt = getAttr(timestamp, "datetime")
	}

	return result, true
}

func walkElements(node *html.Node, visit func(*html.Node)) {
	if node.Type == html.ElementNode {
		visit(node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkElements(child, visit)
	}
}

func findElement(node *html.Node, match func(*html.Node) bool) *html.Node {
	if node.Type == html.ElementNode && match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, match); found != nil {
			return found
		}
	}
	return nil
}

func hasClass(node *html.Node, target string) bool {
	for _, className := range strings.Fields(getAttr(node, "class")) {
		if className == target {
			return true
		}
	}
	return false
}

func visibleText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode {
			if hasClass(current, "u-for-screen-reader") || current.Data == "script" || current.Data == "style" {
				return
			}
		}
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func topicID(rawURL string) int64 {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	const marker = "/topics/"
	index := strings.LastIndex(parsed.Path, marker)
	if index < 0 {
		return 0
	}
	segment := parsed.Path[index+len(marker):]
	if slash := strings.IndexByte(segment, '/'); slash >= 0 {
		segment = segment[:slash]
	}
	id, err := strconv.ParseInt(segment, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func pageNumber(rawURL string) int {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	page, err := strconv.Atoi(parsed.Query().Get("page"))
	if err != nil || page < 1 {
		return 0
	}
	return page
}
