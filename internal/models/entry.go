package models

type Entry struct {
	ID                    int64   `json:"id"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
	Creator               Contact `json:"creator"`
	AlternativeSenderName string  `json:"alternative_sender_name"`
	Summary               string  `json:"summary"`
	Kind                  string  `json:"kind"`
	AppURL                string  `json:"app_url"`
	Body                  string  `json:"body,omitempty"`
	BodyHTML              string  `json:"-"`
}
