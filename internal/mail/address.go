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

// SplitAddresses splits a comma-separated recipient list, leaving a comma inside a
// quoted name, a comment or angle brackets where it is: "Bryan, Annie"
// <annie@example.com> is one recipient.
func SplitAddresses(s string) []string {
	var addresses []string
	start := 0
	for _, comma := range separators(s) {
		if address := strings.TrimSpace(s[start:comma]); address != "" {
			addresses = append(addresses, address)
		}
		start = comma + 1
	}
	if address := strings.TrimSpace(s[start:]); address != "" {
		addresses = append(addresses, address)
	}
	return addresses
}

// AddressAt returns the byte range of the recipient a cursor at byte offset pos is
// in, commas excluded, split the way SplitAddresses splits: the part of a list that
// someone typing at pos is writing.
func AddressAt(s string, pos int) (start, end int) {
	pos = min(max(pos, 0), len(s))
	end = len(s)
	for _, comma := range separators(s) {
		if comma < pos {
			start = comma + 1
			continue
		}
		end = comma
		break
	}
	return start, end
}

// separators returns the byte offsets of the commas that separate recipients, leaving
// out the ones inside a quoted name, a comment or angle brackets.
func separators(s string) []int {
	var commas []int
	quoted, depth, angled := false, 0, false
	escaped := false
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"' && depth == 0:
			quoted = !quoted
		case quoted:
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth > 0:
		case r == '<':
			angled = true
		case r == '>':
			angled = false
		case r == ',' && !angled:
			commas = append(commas, i)
		}
	}
	return commas
}

// FormatAddress writes a recipient the way a person would type it: the bare address
// when there is no name to add, and Name <address> otherwise, quoting a name that
// holds a character that would split or end it, as the mail gem HEY parses with does.
func FormatAddress(name, address string) string {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name == "" || strings.EqualFold(name, address) {
		return address
	}
	if strings.ContainsAny(name, `()<>[]:;@\,."`) {
		name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	return name + " <" + address + ">"
}

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
	spec, size, ok := addrSpec(address)
	if !ok || size > maxAddressSize {
		return false
	}
	at := strings.LastIndexByte(spec, '@')
	if at <= 0 || at == len(spec)-1 || encodedWord.MatchString(spec[:at]) {
		return false
	}
	return deliverableDomain(spec[at+1:])
}

// BareAddress returns the address a recipient names, without its display name,
// angle brackets or comments: "Jane Doe <jane@example.com> (work)" and
// "jane@example.com (Jane Doe)" are both jane@example.com. A recipient HEY's
// parser would refuse, or text with no address in it, is returned trimmed, as
// written.
func BareAddress(recipient string) string {
	if spec, _, ok := addrSpec(recipient); ok && strings.Contains(spec, "@") {
		return spec
	}
	return strings.TrimSpace(recipient)
}

// addrSpec returns the bare address and the fewest characters HEY could write the
// whole address out in, so that a size over the limit is over it for HEY too. ok is
// false for what HEY's parser refuses outright: an unclosed angle bracket, or a second
// @ outside quotes.
func addrSpec(address string) (spec string, size int, ok bool) {
	if parsed, err := netmail.ParseAddress(address); err == nil {
		size = utf8.RuneCountInString(parsed.Address)
		if parsed.Name != "" {
			size += utf8.RuneCountInString(parsed.Name) + len(" <>")
			// The mail gem writes a name holding one of these in quotes, escaping any
			// quote or backslash inside.
			if strings.ContainsAny(parsed.Name, `()<>[]:;@\,."`) {
				size += len(`""`) + strings.Count(parsed.Name, `"`) + strings.Count(parsed.Name, `\`)
			}
		}
		return parsed.Address, size, true
	}
	spec = withoutComments(address)
	name := ""
	if open := strings.LastIndexByte(spec, '<'); open >= 0 {
		end := strings.IndexByte(spec[open:], '>')
		if end < 0 {
			return "", 0, false
		}
		name = strings.Join(strings.Fields(spec[:open]), " ")
		spec = spec[open+1 : open+end]
	}
	spec = strings.Join(strings.Fields(spec), "")
	if at := strings.LastIndexByte(spec, '@'); at >= 0 && strings.Contains(withoutQuoted(spec[:at]), "@") {
		return "", 0, false
	}
	size = utf8.RuneCountInString(spec)
	if name != "" {
		size += utf8.RuneCountInString(name) + len(" <>")
	}
	return spec, size, true
}

// withoutQuoted drops quoted strings, where an @ belongs to the name it is in.
func withoutQuoted(s string) string {
	var b strings.Builder
	quoted := false
	for _, r := range s {
		if r == '"' {
			quoted = !quoted
		} else if !quoted {
			b.WriteRune(r)
		}
	}
	return b.String()
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

// deliverableDomain asks whether a domain ends in a top-level domain HEY's public
// suffix list knows, which is all HEY's lookup comes down to: some rule matches any
// domain under a known top-level domain, and none matches one under an unknown one.
// HEY's list spells an internationalized top-level domain in Unicode, so one typed in
// punycode matches nothing there, and x/net's list, spelled in punycode, is asked in
// punycode without the lookup's mapping, which would turn a fullwidth .ｃｏｍ into .com.
func deliverableDomain(domain string) bool {
	domain = strings.ToLower(domain)
	if strings.HasSuffix(domain, "localdomain") {
		return true
	}
	tld := domain[strings.LastIndexByte(domain, '.')+1:]
	if tld == "" || strings.HasPrefix(tld, "xn--") {
		return false
	}
	ascii, err := idna.Punycode.ToASCII(tld)
	if err != nil {
		return false
	}
	// Asked under a label, so a top-level domain with only a wildcard rule (*.np)
	// matches as well.
	_, icann := publicsuffix.PublicSuffix("x." + ascii)
	return icann
}
