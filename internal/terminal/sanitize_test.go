package terminal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var sanitizeTests = []struct {
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
	{"a right-to-left override cannot reverse a filename", "invoice\u202efdp.exe", "invoicefdp.exe"},
	{"every bidi control goes", "\u061ca\u200eb\u200fc\u202ad\u202be\u202cf\u202dg\u2066h\u2067i\u2068j\u2069k", "abcdefghijk"},
	{"right-to-left text itself survives", "שלום", "שלום"},

	// Invisible format characters draw nothing and are stripped.
	{"a zero width space cannot split a domain", "pay\u200bpal@example.com", "paypal@example.com"},
	{"a soft hyphen cannot hide a filename's extension", "invoice\u00adpdf.exe", "invoicepdf.exe"},
	{"a byte order mark goes", "\ufeffReport", "Report"},
	{"a word joiner goes", "Q3\u2060 planning", "Q3 planning"},
	{"every invisible format character goes", "a\u00adb\u034fc\u180ed\u200be\u2060f\u2061g\u2062h\u2063i\u2064j\u206ak\u206fl\ufeffm", "abcdefghijklm"},

	// Joiners stay where they join and go where they cannot.
	{"an emoji family keeps its joiners", "👨\u200d👩\u200d👧", "👨\u200d👩\u200d👧"},
	{"a heart on fire keeps its selector and joiner", "❤\ufe0f\u200d🔥", "❤\ufe0f\u200d🔥"},
	{"a rainbow flag keeps its joiner", "🏳\ufe0f\u200d🌈", "🏳\ufe0f\u200d🌈"},
	{"a skin tone before a joiner keeps it", "👩🏽\u200d💻", "👩🏽\u200d💻"},
	{"Persian keeps its non-joiner", "می\u200cخواهم", "می\u200cخواهم"},
	{"Devanagari keeps a joiner between letters", "क्\u200dष", "क्\u200dष"},
	{"a joiner between ASCII letters goes", "pay\u200dpal", "paypal"},
	{"a non-joiner between ASCII letters goes", "pay\u200cpal", "paypal"},
	{"a joiner at either end goes", "\u200dRyan\u200d", "Ryan"},
	{"a joiner beside a space goes", "é\u200d é", "é é"},
	{"a run of joiners collapses to one", "é\u200d\u200d\u200dé", "é\u200dé"},
	{"a joiner after a stripped invisible is judged by what was kept", "a\u200b\u200dé", "aé"},
	{"a joiner at the end of non-ASCII text goes", "é\u200d", "é"},
	{"a joiner whose right-hand side is stripped goes", "é\u200d\u200b", "é"},
	{"a joiner whose right-hand side is a dropped mark goes", "e" + strings.Repeat("\u0301", 8) + "\u200d\u0301", "e" + strings.Repeat("\u0301", 8)},
	{"a run of joiners before a space goes", "é\u200d\u200d é", "é é"},
	{"a joiner after a virama joins", "क\u094d\u200dष", "क\u094d\u200dष"},
	{"a joiner after an emoji presentation selector joins", "❤\ufe0f\u200d🔥", "❤\ufe0f\u200d🔥"},
	{"a joiner before a mark goes", "é\u200d\u0301", "é\u0301"},
	{"a mark on an ASCII base does not make it joinable", "e\u0301\u200dé", "e\u0301é"},
	{"a joiner before a tag character goes", "é\u200d\U000e0020", "é\U000e0020"},
	{"a tag character is not a base for a joiner", "é\U000e0020\u200dé", "é\U000e0020é"},

	// Combining marks are capped at eight on a base.
	{"a precomposed accent survives", "Café", "Café"},
	{"a decomposed accent survives", "Cafe\u0301", "Cafe\u0301"},
	{"decomposed Vietnamese survives", "Vie\u0323\u0302t Nam", "Vie\u0323\u0302t Nam"},
	{"Hindi survives", "हिन्दी", "हिन्दी"},
	{"a nukta and virama survive", "क\u093c\u094dष", "क\u093c\u094dष"},
	{"Hebrew with four marks survives", "ש\u05c1\u05bc\u05b7\u05ab", "ש\u05c1\u05bc\u05b7\u05ab"},
	{"fully pointed Hebrew with five marks survives", "ש\u05c1\u05bc\u05b8\u05bd\u0591", "ש\u05c1\u05bc\u05b8\u05bd\u0591"},
	{"a Tibetan stack survives", "བསྒྲུབས", "བསྒྲུབས"},
	{"a keycap survives", "1\ufe0f\u20e3", "1\ufe0f\u20e3"},
	{"an emoji presentation selector survives", "❤\ufe0f", "❤\ufe0f"},
	{"Zalgo is cut at eight marks", "Z" + strings.Repeat("\u0336", 50) + "algo", "Z" + strings.Repeat("\u0336", 8) + "algo"},
	{"the cap resets on each base", "a" + strings.Repeat("\u0301", 9) + "b" + strings.Repeat("\u0301", 9), "a" + strings.Repeat("\u0301", 8) + "b" + strings.Repeat("\u0301", 8)},
	{"the cap resets on a newline", "a" + strings.Repeat("\u0301", 8) + "\n" + strings.Repeat("\u0301", 9), "a" + strings.Repeat("\u0301", 8) + "\n" + strings.Repeat("\u0301", 8)},
	{"a kept format character is not a base", "Z" + strings.Repeat("\u0336", 8) + "\U000e0020" + strings.Repeat("\u0336", 8), "Z" + strings.Repeat("\u0336", 8) + "\U000e0020"},
	{"a subdivision flag survives", "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f", "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f"},

	// A byte that is not UTF-8 is dropped rather than reaching the terminal raw.
	{"an invalid byte goes", "caf\xe9.txt", "caf.txt"},
	{"a raw C1 byte goes", "a\x85b", "ab"},
	{"a replacement character the text carried survives", "a\ufffdb", "a\ufffdb"},
	{"a joiner whose right-hand side is an invalid byte goes", "é\u200d\xe9", "é"},

	// Spaces that are not U+0020 are left alone.
	{"a no-break space survives", "Jean\u00a0Dupont", "Jean\u00a0Dupont"},
	{"an ideographic space survives", "山田\u3000太郎", "山田\u3000太郎"},
	{"a narrow no-break space survives", "12\u202f000", "12\u202f000"},

	// Homoglyphs are not detected.
	{"a Cyrillic letter in a Latin word is kept", "p\u0430ypal", "p\u0430ypal"},
}

func TestSanitize(t *testing.T) {
	for _, test := range sanitizeTests {
		t.Run(test.name, func(t *testing.T) {
			if got := Sanitize(test.value); got != test.want {
				t.Errorf("Sanitize(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

// Sanitizing a second time changes nothing, and sanitizing never lengthens: every
// decision is made on what was kept, so the output is its own fixed point.
func TestSanitizeIsIdempotentAndNeverLonger(t *testing.T) {
	for _, test := range sanitizeTests {
		once := Sanitize(test.value)
		if twice := Sanitize(once); twice != once {
			t.Errorf("%s: Sanitize(Sanitize(%q)) = %q, want %q", test.name, test.value, twice, once)
		}
		if len(once) > len(test.value) {
			t.Errorf("%s: Sanitize(%q) = %q is longer than its input", test.name, test.value, once)
		}
	}
}

// Text that needs nothing removed is returned as it is: Sanitize allocates nothing
// beyond what ansi.Strip already does.
func TestSanitizeCleanTextDoesNotAllocate(t *testing.T) {
	value := "Ryan Singer — Q3 planning ☕ Vie\u0323\u0302t Nam 👨\u200d👩\u200d👧"
	stripping := testing.AllocsPerRun(100, func() { ansi.Strip(value) })
	if allocs := testing.AllocsPerRun(100, func() { Sanitize(value) }); allocs > stripping {
		t.Errorf("Sanitize(%q) allocates %v times, want %v (ansi.Strip alone)", value, allocs, stripping)
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
		{"an invisible goes and a newline becomes a space", "pay\u200bpal\nRyan", "paypal Ryan"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SanitizeLine(test.value); got != test.want {
				t.Errorf("SanitizeLine(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
