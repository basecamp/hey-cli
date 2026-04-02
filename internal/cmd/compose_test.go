package cmd

import (
	"reflect"
	"testing"
)

func TestSplitAddresses(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"alice@hey.com", []string{"alice@hey.com"}},
		{"alice@hey.com,bob@hey.com", []string{"alice@hey.com", "bob@hey.com"}},
		{"alice@hey.com, bob@hey.com", []string{"alice@hey.com", "bob@hey.com"}},
		{" alice@hey.com , bob@hey.com , ", []string{"alice@hey.com", "bob@hey.com"}},
		{",,,", nil},
	}

	for _, tt := range tests {
		got := splitAddresses(tt.input)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitAddresses(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
