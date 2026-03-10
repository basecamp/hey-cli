package models

import (
	"strconv"
	"strings"
)

type Box struct {
	ID                int    `json:"id"`
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	AppURL            string `json:"app_url"`
	URL               string `json:"url"`
	PostingChangesURL string `json:"posting_changes_url"`
}

type Posting struct {
	ID                  int     `json:"id"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
	ObservedAt          string  `json:"observed_at"`
	Kind                string  `json:"kind"`
	Seen                bool    `json:"seen"`
	Bundled             bool    `json:"bundled"`
	Muted               bool    `json:"muted"`
	Summary             string  `json:"summary"`
	EntryKind           string  `json:"entry_kind"`
	IncludesAttachments bool    `json:"includes_attachments"`
	BubbledUp           bool    `json:"bubbled_up"`
	AppURL              string  `json:"app_url"`
	Creator             Contact `json:"creator"`
	TopicID             int     `json:"topic_id"`
	Topic               *Topic  `json:"topic,omitempty"`
}

// ResolveTopicID returns the topic ID for this posting, preferring
// the embedded topic_id/topic fields, then parsing from app_url.
// Returns 0 if no topic ID can be determined (e.g. bundles).
func (p *Posting) ResolveTopicID() int {
	if p.Topic != nil && p.Topic.ID != 0 {
		return p.Topic.ID
	}
	if p.TopicID != 0 {
		return p.TopicID
	}
	// Parse from app_url like "https://app.hey.com/topics/1943585351"
	if i := strings.LastIndex(p.AppURL, "/topics/"); i >= 0 {
		segment := p.AppURL[i+len("/topics/"):]
		// Strip trailing path components or query strings
		if j := strings.IndexAny(segment, "/?#"); j >= 0 {
			segment = segment[:j]
		}
		if id, err := strconv.Atoi(segment); err == nil {
			return id
		}
	}
	return 0
}

type Contact struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
	Avatar       string `json:"avatar,omitempty"`
}
