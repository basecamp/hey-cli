// Package harness detects and checks AI coding agent integration health.
package harness

// StatusCheck represents a single agent integration health check result.
type StatusCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "pass", "warn", "fail"
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}
