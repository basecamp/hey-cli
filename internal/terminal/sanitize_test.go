package terminal

import "testing"

func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"plain text is untouched", "Ryan Singer", "Ryan Singer"},
		{"a colour sequence goes with its payload", "\x1b[31mURGENT\x1b[0m", "URGENT"},
		{"a window title sequence leaves nothing behind", "\x1b]0;pwned\x07report.pdf", "report.pdf"},
		{"a stray escape takes the byte it would have escaped", "sub\x1bject", "subect"},
		{"a carriage return cannot rewrite the line", "Invoice\rPAID", "InvoicePAID"},
		{"a C1 control is removed", "caf" + string(rune(0x85)) + "e", "cafe"},
		{"DEL is removed", "note\x7f", "note"},
		{"newlines and tabs survive", "line one\nline\ttwo", "line one\nline\ttwo"},
		{"emoji and accents survive", "Café ☕", "Café ☕"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Sanitize(test.value); got != test.want {
				t.Errorf("Sanitize(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestSanitizeLine(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"a newline becomes a space", "Ryan\nSinger", "Ryan Singer"},
		{"a tab becomes a space", "Ryan\tSinger", "Ryan Singer"},
		{"an escape sequence still goes", "\x1b[2JLabel", "Label"},
		{"plain text is untouched", "Q3 planning", "Q3 planning"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SanitizeLine(test.value); got != test.want {
				t.Errorf("SanitizeLine(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
