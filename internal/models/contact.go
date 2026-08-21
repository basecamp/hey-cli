package models

type Contact struct {
	ID           int64     `json:"id"`
	AccountID    int64     `json:"account_id,omitempty"`
	Name         string    `json:"name"`
	EmailAddress string    `json:"email_address"`
	Avatar       string    `json:"avatar,omitempty"`
	UpdatedAt    string    `json:"updated_at,omitempty"`
	Aliases      []Contact `json:"aliases,omitempty"`
}
