package mail

import (
	netmail "net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// maxAddressSize is the longest address HEY delivers to, in characters of the address
// as it writes it out, name included (Contact::CertifiedMailAddress).
const maxAddressSize = 500

// encodedWord is an RFC 2047 encoded word. HEY decodes one in an address and drops the
// address when the decoded form parses differently, which it always does in the part
// before the @.
var encodedWord = regexp.MustCompile(`=\?[^?]*\?[bBqQ]\?[^?]*\?=`)

// InvalidAddress returns the first recipient HEY would certainly not deliver to, or ""
// when there is none.
//
// HEY does not refuse a bad recipient: it drops it. A message left with nobody is
// saved as a draft and answered with a redirect to it, and a message with somebody
// left goes to them alone. Either way the sender is not told, so an address is
// checked here against HEY's own rule (LenientMailFieldsParser): it has a part before
// the @ and a domain after it; the domain ends in a public suffix, or in localdomain;
// and it is no longer than HEY keeps.
//
// Refusing an address HEY would deliver to is worse than the silence this prevents,
// so the check leans towards letting an address through. HEY parses with Ruby's mail
// gem, which accepts more than net/mail does — quoted parts, comments, stray spaces
// — and an address net/mail cannot parse is judged on what it can still see.
func InvalidAddress(lists ...[]string) string {
	for _, list := range lists {
		for _, address := range list {
			if !deliverable(address) {
				return address
			}
		}
	}
	return ""
}

func deliverable(address string) bool {
	spec, size := addrSpec(address)
	if size > maxAddressSize {
		return false
	}
	at := strings.LastIndexByte(spec, '@')
	if at <= 0 || at == len(spec)-1 || encodedWord.MatchString(spec[:at]) {
		return false
	}
	return deliverableDomain(spec[at+1:])
}

// addrSpec returns the bare address and the fewest characters HEY could write the
// whole address out in, so that a size over the limit is over it for HEY too.
func addrSpec(address string) (spec string, size int) {
	if parsed, err := netmail.ParseAddress(address); err == nil {
		size = utf8.RuneCountInString(parsed.Address)
		if parsed.Name != "" {
			size += utf8.RuneCountInString(parsed.Name) + len(" <>")
		}
		return parsed.Address, size
	}
	spec = withoutComments(address)
	if open := strings.LastIndexByte(spec, '<'); open >= 0 {
		if end := strings.IndexByte(spec[open:], '>'); end > 0 {
			spec = spec[open+1 : open+end]
		}
	}
	spec = strings.Join(strings.Fields(spec), "")
	return spec, utf8.RuneCountInString(spec)
}

// withoutComments drops parenthesized comments outside quoted strings.
func withoutComments(s string) string {
	var b strings.Builder
	depth, quoted := 0, false
	for _, r := range s {
		switch {
		case r == '"' && depth == 0:
			quoted = !quoted
		case r == '(' && !quoted:
			depth++
			continue
		case r == ')' && !quoted && depth > 0:
			depth--
			continue
		}
		if depth == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// deliverableDomain asks whether a domain ends in a public suffix, the way HEY's
// PublicSuffix lookup does: ICANN rules only, wildcards included, private rules
// ignored. HEY's list spells an internationalized suffix in Unicode, so a label typed
// in punycode matches no rule there and is kept from matching one here.
func deliverableDomain(domain string) bool {
	domain = strings.ToLower(domain)
	if strings.HasSuffix(domain, "localdomain") {
		return true
	}
	labels := strings.Split(domain, ".")
	for i, label := range labels {
		switch {
		case label == "":
			return false
		case strings.HasPrefix(label, "xn--"):
			labels[i] = "punycode-label"
		default:
			ascii, err := idna.Punycode.ToASCII(label)
			if err != nil {
				return false
			}
			labels[i] = ascii
		}
	}
	if _, icann := publicsuffix.PublicSuffix(strings.Join(labels, ".")); icann {
		return true
	}
	// A private rule — blogspot.com — outranks the ICANN rule under it in the lookup,
	// and HEY ignores private rules, so the top-level domain is asked on its own.
	tld := labels[len(labels)-1]
	suffix, icann := publicsuffix.PublicSuffix(tld)
	return icann && suffix == tld
}
