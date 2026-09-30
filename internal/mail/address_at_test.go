package mail

import (
	"strings"
	"testing"
)

func TestAddressAtFindsTheRecipientUnderTheCursor(t *testing.T) {
	list := `Jane Doe <jane@example.com>, "Bryan, Annie" <annie@example.com>, ric`
	for _, tc := range []struct {
		pos  int
		want string
	}{
		{0, "Jane Doe <jane@example.com>"},
		{10, "Jane Doe <jane@example.com>"},
		// On the comma itself the cursor still belongs to the recipient before it.
		{strings.Index(list, ">,") + 1, "Jane Doe <jane@example.com>"},
		{strings.Index(list, "Annie"), ` "Bryan, Annie" <annie@example.com>`},
		{len(list), " ric"},
		{len(list) + 10, " ric"},
		{-3, "Jane Doe <jane@example.com>"},
	} {
		start, end := AddressAt(list, tc.pos)
		if got := list[start:end]; got != tc.want {
			t.Errorf("AddressAt(%d) = %q, want %q", tc.pos, got, tc.want)
		}
	}

	if start, end := AddressAt("", 0); start != 0 || end != 0 {
		t.Errorf("AddressAt on an empty list = %d, %d", start, end)
	}
	if start, end := AddressAt("jane@example.com, ", 18); start != 17 || end != 18 {
		t.Errorf("AddressAt after a trailing comma = %d, %d, want the empty recipient at the end", start, end)
	}
}

func TestFormatAddressWritesWhatHEYParsesBack(t *testing.T) {
	for _, tc := range []struct {
		name, address, want string
	}{
		{"", "jane@example.com", "jane@example.com"},
		{"jane@example.com", "jane@example.com", "jane@example.com"},
		{"JANE@example.com", "jane@example.com", "jane@example.com"},
		{"Jane Doe", "jane@example.com", "Jane Doe <jane@example.com>"},
		{"Bryan, Annie", "annie@example.com", `"Bryan, Annie" <annie@example.com>`},
		{`Rick "The Man" Sanchez`, "rick@example.org", `"Rick \"The Man\" Sanchez" <rick@example.org>`},
		{"J. Smith", "j.smith@example.org", `"J. Smith" <j.smith@example.org>`},
		{"Zoë Martín", "zoe@example.com", "Zoë Martín <zoe@example.com>"},
	} {
		got := FormatAddress(tc.name, tc.address)
		if got != tc.want {
			t.Errorf("FormatAddress(%q, %q) = %q, want %q", tc.name, tc.address, got, tc.want)
			continue
		}
		// Whatever is written has to survive the trip back through the list parsing
		// and HEY's delivery rule as one recipient.
		if split := SplitAddresses(got + ", someone@example.com"); len(split) != 2 || split[0] != got {
			t.Errorf("FormatAddress(%q, %q) split into %q", tc.name, tc.address, split)
		}
		if bad := InvalidAddress([]string{got}); bad != "" {
			t.Errorf("FormatAddress(%q, %q) = %q, which HEY would drop", tc.name, tc.address, got)
		}
	}
}
