package cmd

import (
	"testing"
)

func TestNormalizeConfigKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"default-sender", "default_sender"},
		{"default_sender", "default_sender"},
		{"base-url", "base_url"},
		{"base_url", "base_url"},
		{"no-hyphens", "no_hyphens"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeConfigKey(tt.input)
			if got != tt.want {
				t.Errorf("normalizeConfigKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
