package models

type Calendar struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Owned     bool   `json:"owned"`
	Personal  bool   `json:"personal"`
	External  bool   `json:"external"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Recording struct {
	ID               int64     `json:"id"`
	Title            string    `json:"title"`
	AllDay           bool      `json:"all_day"`
	Recurring        bool      `json:"recurring"`
	StartsAt         string    `json:"starts_at"`
	EndsAt           string    `json:"ends_at"`
	StartsAtTimeZone string    `json:"starts_at_time_zone"`
	EndsAtTimeZone   string    `json:"ends_at_time_zone"`
	CreatedAt        string    `json:"created_at"`
	UpdatedAt        string    `json:"updated_at"`
	Type             string    `json:"type"`
	CompletedAt      string    `json:"completed_at,omitempty"`
	Label            string    `json:"label,omitempty"`
	Icon             string    `json:"icon,omitempty"`
	Color            string    `json:"color,omitempty"`
	Days             []int32   `json:"days,omitempty"`
	Calendar         *Calendar `json:"calendar,omitempty"`
	RemindersLabel   string    `json:"reminders_label,omitempty"`
}
