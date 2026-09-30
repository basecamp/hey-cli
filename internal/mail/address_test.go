package mail

import (
	"strings"
	"testing"
)

func TestInvalidAddressFollowsHEYsRule(t *testing.T) {
	for address, deliverable := range map[string]bool{
		"annie@example.com":                       true,
		"Annie Bryan <annie@example.com>":         true,
		`"Bryan, Annie" <annie@example.com>`:      true,
		"J. Smith <j.smith@example.org>":          true,
		"annie+newsletters@example.co.uk":         true,
		"annie@пример.рф":                         true,
		"annie@build.localdomain":                 true,
		"a":                                       false,
		"annie":                                   false,
		"annie@":                                  false,
		"@example.com":                            false,
		"annie@example":                           false,
		"annie@example.notatld":                   false,
		"annie@@example.com":                      false,
		"annie@example.com>":                      false,
		strings.Repeat("a", 490) + "@example.com": false,
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
