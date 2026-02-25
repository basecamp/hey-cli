package models

type JournalEntry struct {
	ID        int    `json:"id"`
	Date      string `json:"date"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
