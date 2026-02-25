package models

type Topic struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	AccountID   int       `json:"account_id"`
	AppURL      string    `json:"app_url"`
	Creator     Contact   `json:"creator"`
	Contacts    []Contact `json:"contacts"`
	LatestEntry *Entry    `json:"latest_entry,omitempty"`
}

type Entry struct {
	ID                    int       `json:"id"`
	CreatedAt             string    `json:"created_at"`
	UpdatedAt             string    `json:"updated_at"`
	Creator               Contact   `json:"creator"`
	AlternativeSenderName string    `json:"alternative_sender_name"`
	Summary               string    `json:"summary"`
	Kind                  string    `json:"kind"`
	AppURL                string    `json:"app_url"`
	Body                  string    `json:"body,omitempty"`
	Recipients            []Contact `json:"recipients,omitempty"`
}

type EntriesResponse struct {
	Entries     []Entry `json:"entries"`
	NextPageURL string  `json:"next_page_url,omitempty"`
}

type Draft struct {
	ID        int     `json:"id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	Summary   string  `json:"summary"`
	Kind      string  `json:"kind"`
	Creator   Contact `json:"creator"`
	Topic     *Topic  `json:"topic,omitempty"`
}
