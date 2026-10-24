package mcpserver

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// The confirmation the person reads (sending-safety D2). Every recipient field is shown,
// Bcc included and "—" when empty. Every dynamic field is rendered inert: the subject,
// names, attachment names and body were chosen by a model or a sender, and the host shows
// the prompt as plain text, so a newline in a display name could forge a Bcc line and a
// bidi override could reorder an address. Header fields are single lines, capped, and
// come first in a fixed order; every body line is indented beneath "Body:". This is
// presentation only: the fingerprint covers the raw values.

func (s *sender) prompt(snap *draftSnapshot, outcome *reviewOutcome) string {
	var b strings.Builder
	if outcome.exposed {
		fmt.Fprintf(&b, "This conversation read mail that may be from an unscreened sender (%s) in the last few minutes. Check that you asked for this email.\n\n", inertLine(outcome.exposedBy))
	}
	fmt.Fprintf(&b, "Send this email from %s?\n", recipientLine(snap.from, nil))
	b.WriteString("  When:     Now (undo window applies)\n")
	fmt.Fprintf(&b, "  To:       %s\n", recipientList(snap.to, outcome.newAddresses))
	fmt.Fprintf(&b, "  Cc:       %s\n", recipientList(snap.cc, outcome.newAddresses))
	fmt.Fprintf(&b, "  Bcc:      %s\n", recipientList(snap.bcc, outcome.newAddresses))
	// Delivered text is shown whole: only display names, which aren't sent, are capped.
	fmt.Fprintf(&b, "  Subject:  %s\n", inertWhole(snap.subject))
	fmt.Fprintf(&b, "  Attached: %s\n", attachmentList(snap.body.attachments))
	b.WriteString("  Body:\n")

	text := snap.body.text
	if outcome.plain {
		text = snap.body.plainText
	}
	body := inertBody(text)
	total := utf8.RuneCountInString(body)
	if total > bodyPromptCap {
		body = string([]rune(body)[:bodyPromptCap]) + " …"
	}
	for line := range strings.SplitSeq(body, "\n") {
		b.WriteString("    " + line + "\n")
	}
	if total > bodyPromptCap {
		fmt.Fprintf(&b, "    [first %s characters of %s · open the full draft in HEY: %s]\n", thousands(bodyPromptCap), thousands(total), s.editURL(snap.id))
	}
	switch {
	case outcome.plain:
		b.WriteString("  Goes as plain text: the draft's formatting isn't sent, so what's above is all of it.\n")
	case !snap.body.lossless:
		fmt.Fprintf(&b, "  Some of this draft can't be shown here; open it in HEY to see all of it: %s\n", s.editURL(snap.id))
	}
	if len(outcome.newAddresses) > 0 {
		b.WriteString("\nRecipients marked (new) haven't been emailed from this account before hey mcp was signed in.\n")
	}
	return b.String()
}

// recipientLine shows a recipient as "Name" <address>, so the address the mail goes to is
// always visible beside whatever name the draft carries.
func recipientLine(c generated.Contact, fresh map[string]bool) string {
	// The address is escaped but never truncated: the person has to see all of where
	// the mail goes.
	address := "<" + escapeInvisible(c.EmailAddress, false) + ">"
	line := address
	if name := strings.TrimSpace(c.Name); name != "" && !strings.EqualFold(name, c.EmailAddress) {
		line = `"` + strings.ReplaceAll(inertLine(name), `"`, `\"`) + `" ` + address
	}
	if fresh[normalizedAddress(c.EmailAddress)] {
		line += " (new: never emailed before)"
	}
	return line
}

func recipientList(contacts []generated.Contact, fresh map[string]bool) string {
	if len(contacts) == 0 {
		return "—"
	}
	lines := make([]string, 0, len(contacts))
	for _, c := range contacts {
		lines = append(lines, recipientLine(c, fresh))
	}
	return strings.Join(lines, ", ")
}

func attachmentList(attachments []attachment) string {
	if len(attachments) == 0 {
		return "—"
	}
	names := make([]string, 0, len(attachments))
	for _, a := range attachments {
		name := fmt.Sprintf("%s (%s)", inertWhole(a.Filename), humanSize(a.Size))
		if a.Caption != "" {
			name += `, captioned "` + inertWhole(a.Caption) + `"`
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// inertLine renders a header field: one line, every control and invisible character
// escaped, capped at headerCap characters.
func inertLine(s string) string {
	out := escapeInvisible(s, false)
	if utf8.RuneCountInString(out) > headerCap {
		out = string([]rune(out)[:headerCap]) + "…"
	}
	return out
}

// inertWhole renders a delivered header field (the subject, an attachment's name and
// caption): one line, every control and invisible character escaped, never truncated,
// since what's cut off would go out unseen.
func inertWhole(s string) string {
	return escapeInvisible(s, false)
}

// inertBody renders the body: plain newlines kept, everything else escaped.
func inertBody(s string) string {
	return escapeInvisible(s, true)
}

// escapeInvisible replaces C0 and C1 controls, bidi embeddings, overrides, isolates and
// marks, zero-width characters and the Unicode line and paragraph separators with a
// visible \u{…} escape. keepNewlines keeps "\n" as a line break.
func escapeInvisible(s string, keepNewlines bool) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' && keepNewlines {
			b.WriteRune(r)
			continue
		}
		if invisible(r) {
			fmt.Fprintf(&b, `\u{%04X}`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// invisible reports a rune that shows as nothing or reorders what's around it: every
// format character (Unicode Cf: bidi controls, zero-width characters, tag characters,
// the soft hyphen), variation selectors, private-use characters, the rest of Unicode's
// default-ignorable set (the combining grapheme joiner, Hangul fillers), and the controls and
// separators below. A class, not a list, so the next invisible character is caught too.
func invisible(r rune) bool {
	if unicode.In(r, unicode.Cf, unicode.Co, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point) {
		return true
	}
	switch {
	case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0x200E, r == 0x200F, r == 0x061C:
		return true
	case r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF, r == 0x00AD, r == 0x180E:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r == utf8.RuneError:
		return true
	}
	return false
}
