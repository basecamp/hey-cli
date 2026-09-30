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
		"annie@photos.blogspot.com":          true,
		// Whole country domains are wildcard rules, which only a full lookup matches.
		"annie@example.np": true,
		"annie@www.ck":     true,
		"annie@example.jm": true,
		// Ruby's parser takes these, and HEY keeps and prefills them; net/mail does not.
		`a."b"@example.com`:          true,
		`"a"."b"@example.com`:        true,
		"(comment)annie@example.com": true,
		"annie @example.com":         true,
		// Length is HEY's: characters, not bytes.
		strings.Repeat("é", 245) + "@example.com": true,

		"a":                     false,
		"annie":                 false,
		"annie@":                false,
		"@example.com":          false,
		"annie@example":         false,
		"annie@np":              false,
		"annie@example.notatld": false,
		"annie@example.com.":    false,
		// HEY's list spells suffixes in Unicode, so punycode and fullwidth forms match nothing.
		"annie@xn--e1afmkfd.xn--p1ai": false,
		"annie@example.ｃｏｍ":           false,
		// An encoded word decodes to a different address, which HEY drops.
		"=?utf-8?q?annie=40example.org?=@example.com": false,
		// Length counts the name HEY writes out with the address.
		strings.Repeat("a", 490) + "@example.com":         false,
		strings.Repeat("A", 490) + " <annie@example.com>": false,
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
