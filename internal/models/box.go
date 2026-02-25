package models

import "encoding/json"

type Box struct {
	ID                int    `json:"id"`
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	AppURL            string `json:"app_url"`
	URL               string `json:"url"`
	PostingChangesURL string `json:"posting_changes_url"`
}

type BoxShowResponse struct {
	Box                    Box               `json:"box"`
	Postings               []json.RawMessage `json:"postings"`
	NextHistoryURL         string            `json:"next_history_url"`
	NextIncrementalSyncURL string            `json:"next_incremental_sync_url"`
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
	Creator             Contact `json:"creator"`
	Topic               *Topic  `json:"topic,omitempty"`
}

type Contact struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
	Avatar       string `json:"avatar,omitempty"`
}
