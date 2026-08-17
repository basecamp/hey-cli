package htmlutil

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	// collectionAnchorRe matches an <a> anchor linking to a topic, capturing the
	// attributes before the href, the topic id, the attributes after the href,
	// and the inner HTML. Attribute order varies, so class/title are inspected
	// from the combined attribute text rather than assumed adjacent to href.
	collectionAnchorRe = regexp.MustCompile(`(?s)<a\b([^>]*?)href="/topics/(\d+)[^"]*"([^>]*)>(.*?)</a>`)
	titleAttrRe        = regexp.MustCompile(`title="([^"]*)"`)
	anyTagRe           = regexp.MustCompile(`<[^>]+>`)
	// collectionNextPageRe finds the timeline's "next page" pagination link.
	collectionNextPageRe = regexp.MustCompile(`data-pagination-target="nextPageLink"[^>]*href="([^"]+)"`)
)

// CollectionTopic is a single topic (thread) listed within a collection.
type CollectionTopic struct {
	TopicID int64  `json:"topic_id"`
	Title   string `json:"title"`
}

// ParseCollectionTopicsHTML extracts the threads on one page of a collection.
// Threads render either as card__link anchors (the recent items shown as cards)
// or entry__collection-topic anchors (older items further down the timeline).
// Other /topics/ links on the page — such as attachment "show message" links —
// are ignored. Results preserve document order and are de-duplicated by topic ID
// within the page. The collection detail endpoint has no JSON representation, so
// this HTML-based extraction is required.
func ParseCollectionTopicsHTML(page string) []CollectionTopic {
	var topics []CollectionTopic
	seen := map[int64]bool{}
	for _, m := range collectionAnchorRe.FindAllStringSubmatch(page, -1) {
		attrs := m[1] + m[3]
		if !strings.Contains(attrs, "card__link") && !strings.Contains(attrs, "entry__collection-topic") {
			continue
		}
		id, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true

		// Prefer the title attribute (a clean subject); fall back to inner text.
		var title string
		if tm := titleAttrRe.FindStringSubmatch(attrs); tm != nil {
			title = tm[1]
		} else {
			title = anyTagRe.ReplaceAllString(m[4], "")
		}
		title = strings.TrimSpace(html.UnescapeString(title))

		topics = append(topics, CollectionTopic{TopicID: id, Title: title})
	}
	return topics
}

// ParseCollectionNextPage returns the path of the next page of a collection's
// timeline, or "" when there is no further page. The path is used to walk the
// whole collection, since a single page only renders the first ~50 threads.
func ParseCollectionNextPage(page string) string {
	m := collectionNextPageRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}
