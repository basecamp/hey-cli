package mail

import (
	netmail "net/mail"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// maxAddressSize is the longest address HEY delivers to (Contact::CertifiedMailAddress).
const maxAddressSize = 500

// InvalidAddress returns the first recipient HEY would not deliver to, or "" when
// every one is deliverable.
//
// HEY does not refuse a bad recipient: it drops it. A message left with nobody is
// saved as a draft and answered with a redirect to it, and a message with somebody
// left goes to them alone. Either way the sender is not told, so an address is
// checked here, by HEY's own rule (LenientMailFieldsParser), before anything is sent:
// it parses as an address, bare or with a name; it has a local part and a domain; the
// domain ends in a top-level domain on the public suffix list, or in localdomain; and
// it is no longer than HEY keeps.
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
	parsed, err := netmail.ParseAddress(address)
	if err != nil || len(parsed.Address) > maxAddressSize {
		return false
	}
	at := strings.LastIndexByte(parsed.Address, '@')
	if at <= 0 || at == len(parsed.Address)-1 {
		return false
	}
	domain := strings.ToLower(parsed.Address[at+1:])
	if strings.HasSuffix(domain, "localdomain") {
		return true
	}
	tld, err := idna.Lookup.ToASCII(domain[strings.LastIndexByte(domain, '.')+1:])
	if err != nil || tld == "" {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(tld)
	return icann && suffix == tld
}
