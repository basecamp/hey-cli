package models

// SearchResult is a topic returned by HEY's HTML advanced-search view.
type SearchResult struct {
	ID       int64  `json:"id"`
	Subject  string `json:"subject"`
	Summary  string `json:"summary,omitempty"`
	ActiveAt string `json:"active_at,omitempty"`
	AppURL   string `json:"app_url"`
}
