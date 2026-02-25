package models

type CalendarsResponse struct {
	Calendars         []Calendar `json:"calendars"`
	CalendarChangesURL string    `json:"calendar_changes_url"`
}

type Calendar struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Owned          bool   `json:"owned"`
	Personal       bool   `json:"personal"`
	External       bool   `json:"external"`
	URL            string `json:"url"`
	RecordingsURL  string `json:"recordings_url"`
	OccurrencesURL string `json:"occurrences_url"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type Recording struct {
	ID                 int       `json:"id"`
	ParentID           *int      `json:"parent_id"`
	Title              string    `json:"title"`
	AllDay             bool      `json:"all_day"`
	Recurring          bool      `json:"recurring"`
	StartsAt           string    `json:"starts_at"`
	EndsAt             string    `json:"ends_at"`
	StartsAtTimeZone   string    `json:"starts_at_time_zone"`
	EndsAtTimeZone     string    `json:"ends_at_time_zone"`
	CreatedAt          string    `json:"created_at"`
	UpdatedAt          string    `json:"updated_at"`
	Type               string    `json:"type"`
	CompletedAt        string    `json:"completed_at,omitempty"`
	Calendar           *Calendar `json:"calendar,omitempty"`
	RemindersLabel     string    `json:"reminders_label,omitempty"`
	OccurrencesURL     string    `json:"occurrences_url,omitempty"`
}

type RecordingsResponse map[string][]Recording

type Todo struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	StartsAt    string `json:"starts_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type TimeTrack struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	StartsAt  string `json:"starts_at"`
	EndsAt    string `json:"ends_at,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
