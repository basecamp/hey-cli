package cmd

import (
	"testing"
)

func TestParseAddresses(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single address",
			input: "alice@example.com",
			want:  []string{"alice@example.com"},
		},
		{
			name:  "multiple addresses",
			input: "alice@example.com,bob@example.com",
			want:  []string{"alice@example.com", "bob@example.com"},
		},
		{
			name:  "addresses with whitespace",
			input: " alice@example.com , bob@example.com , carol@example.org ",
			want:  []string{"alice@example.com", "bob@example.com", "carol@example.org"},
		},
		{
			name:  "trailing comma",
			input: "alice@example.com,",
			want:  []string{"alice@example.com"},
		},
		{
			name:  "empty entries between commas",
			input: "alice@example.com,,bob@example.com",
			want:  []string{"alice@example.com", "bob@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAddresses(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAddresses(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseAddresses(%q)[%d] = %q, want %q",
						tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
