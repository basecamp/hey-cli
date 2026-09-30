package mail

import (
	"strings"
	"testing"
)

func TestInvalidAddressFollowsHEYsRule(t *testing.T) {
	for address, deliverable := range map[string]bool{
		"annie@example.com":                  true,
		"ANNIE@EXAMPLE.COM":                  true,
		"Annie Bryan <annie@example.com>":    true,
		`"Bryan, Annie" <annie@example.com>`: true,
		"J. Smith <j.smith@example.org>":     true,
		"annie+newsletters@example.co.uk":    true,
		"annie@пример.рф":                    true,
		"annie@build.localdomain":            true,
		// HEY's rule is /localdomain$/i, and the dev server delivers to this.
		"annie@notlocaldomain":      true,
		"annie@photos.blogspot.com": true,
		// Whole country domains are wildcard rules, which only a full lookup matches.
		"annie@example.np": true,
		"annie@www.ck":     true,
		"annie@example.jm": true,
		// Checked against the dev server: HEY asks only whether the top-level domain is known.
		"annie@np":           true,
		"annie@example..com": true,
		// Ruby's parser takes these, and HEY keeps and prefills them; net/mail does not.
		`a."b"@example.com`:          true,
		`"a"."b"@example.com`:        true,
		"(comment)annie@example.com": true,
		"annie @example.com":         true,
		// Length is HEY's: characters, not bytes.
		strings.Repeat("é", 245) + "@example.com": true,

		"a":                        false,
		"annie":                    false,
		"annie@":                   false,
		"@example.com":             false,
		"annie@example":            false,
		"annie@example.notatld":    false,
		"annie@example.com.":       false,
		"annie@@example.com":       false,
		"Annie <annie@example.com": false,
		// HEY's list spells suffixes in Unicode, so punycode and fullwidth forms match nothing.
		"annie@xn--e1afmkfd.xn--p1ai": false,
		"annie@example.ｃｏｍ":           false,
		// An encoded word decodes to a different address, which HEY drops.
		"=?utf-8?q?annie=40example.org?=@example.com": false,
		// Length counts the name HEY writes out with the address.
		strings.Repeat("a", 490) + "@example.com":         false,
		strings.Repeat("A", 490) + " <annie@example.com>": false,
		// Even where net/mail cannot parse the address, the name counts.
		strings.Repeat("A", 490) + ` <a."b"@example.com>`: false,
		strings.Repeat("A", 470) + ` <a."b"@example.com>`: true,
		// A name the mail gem has to quote is two characters longer written out.
		`"` + strings.Repeat("A", 472) + `, Annie" <annie@example.com>`: false,
		strings.Repeat("A", 474) + ` Annie <annie@example.com>`:         true,
	} {
		got := InvalidAddress([]string{address}) == ""
		if got != deliverable {
			t.Errorf("deliverable(%q) = %v, want %v", address, got, deliverable)
		}
	}
}

func TestInvalidAddressNamesTheFirstBadRecipientInAnyList(t *testing.T) {
	to := []string{"annie@example.com"}
	cc := []string{"frank.castillo@example.org", "frank"}
	bcc := []string{"a"}
	if got := InvalidAddress(to, cc, bcc); got != "frank" {
		t.Errorf("InvalidAddress = %q, want the first bad recipient, frank", got)
	}
	if got := InvalidAddress(to, nil, nil); got != "" {
		t.Errorf("InvalidAddress of good recipients = %q, want none", got)
	}
}

func TestSplitAddressesKeepsCommasInsideAnAddress(t *testing.T) {
	for input, want := range map[string][]string{
		"annie@example.com, frank@example.org":                         {"annie@example.com", "frank@example.org"},
		` "Bryan, Annie" <annie@example.com> ,frank@example.org,`:      {`"Bryan, Annie" <annie@example.com>`, "frank@example.org"},
		`"Castillo, \"Frank\"" <frank@example.org>, annie@example.com`: {`"Castillo, \"Frank\"" <frank@example.org>`, "annie@example.com"},
		"annie@example.com (Bryan, Annie), frank@example.org":          {"annie@example.com (Bryan, Annie)", "frank@example.org"},
		"":    nil,
		" , ": nil,
	} {
		got := SplitAddresses(input)
		if len(got) != len(want) {
			t.Errorf("SplitAddresses(%q) = %q, want %q", input, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("SplitAddresses(%q) = %q, want %q", input, got, want)
				break
			}
		}
	}
}
