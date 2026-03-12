package smoke_test

import (
	"fmt"
	"strings"
)

// intStr converts an int to string for use as a CLI argument.
func intStr(n int) string {
	return fmt.Sprintf("%d", n)
}

// extractTopicID extracts the topic ID from an app_url like
// "http://host/topics/12345" or "http://host/topics/12345/...".
func extractTopicID(appURL string) string {
	const marker = "/topics/"
	idx := strings.LastIndex(appURL, marker)
	if idx < 0 {
		return ""
	}
	segment := appURL[idx+len(marker):]
	// Strip trailing path components or query strings.
	if j := strings.IndexAny(segment, "/?#"); j >= 0 {
		segment = segment[:j]
	}
	return segment
}
