package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// maxRecipientSuggestions is how many people the list under a recipient field
// offers at once. Everyone else is a few more letters away, and a short list is
// one the eye can take in without scrolling.
const maxRecipientSuggestions = 6

// recipientSuggestion is one row of the list HEY's own composer suggests
// recipients from: a person, a contact group, or everyone at an account.
type recipientSuggestion struct {
	value  string // one address, or a comma-separated list for a group
	label  string // the name to show
	detail string // what a group or an account stands for; empty for a person

	// name and address are label and value folded to lower case once, when the
	// list arrives, so matching a keystroke against thousands of rows compares
	// strings without allocating any.
	name    string
	address string

	// members is a group's addresses, lower-cased, so that a group already on
	// the line can be left out the way a person is.
	members []string
}

// chosen reports whether the line already holds the row: a person's address,
// or every one of a group's.
func (s *recipientSuggestion) chosen(on map[string]bool) bool {
	if len(s.members) == 0 {
		return on[s.address]
	}
	for _, member := range s.members {
		if !on[member] {
			return false
		}
	}
	return true
}

// group reports whether picking the row adds more than one recipient.
func (s recipientSuggestion) group() bool {
	return s.detail != "" || strings.Contains(s.value, ",")
}

// recipientsLoadedMsg carries HEY's recipient list, already read into the
// shape the popover filters, so none of that work happens on the UI's loop.
type recipientsLoadedMsg struct {
	suggestions []recipientSuggestion
	err         error
}

// newRecipientSuggestions reads HEY's rows. Names and details are somebody
// else's text, so they are sanitized here, once, before anything draws them.
// HEY's order is kept: the people you wrote to most recently come first.
func newRecipientSuggestions(rows []hey.AddressableRecipient) []recipientSuggestion {
	suggestions := make([]recipientSuggestion, 0, len(rows))
	for _, row := range rows {
		// The value is where mail goes, so sanitizing it may not change it: an
		// address holding a control or an invisible format character would be
		// rewritten into a different mailbox. Such a row is left out rather
		// than offered under an address it doesn't have.
		value := strings.TrimSpace(row.Value)
		if value == "" || terminal.SanitizeLine(value) != value {
			continue
		}
		label := strings.TrimSpace(terminal.SanitizeLine(row.Label))
		if label == "" {
			label = value
		}
		suggestion := recipientSuggestion{
			value:  value,
			label:  label,
			detail: strings.TrimSpace(terminal.SanitizeLine(row.Detail)),
			name:   strings.ToLower(label),
		}
		// A group is found by its name. Its members are people of their own, and
		// matching their addresses would offer "Everyone at …" to anyone typing
		// the first letters of one colleague.
		if suggestion.group() {
			for _, member := range mail.SplitAddresses(value) {
				suggestion.members = append(suggestion.members, strings.ToLower(member))
			}
		} else {
			suggestion.address = strings.ToLower(value)
		}
		suggestions = append(suggestions, suggestion)
	}
	return suggestions
}

// matchRecipients answers the rows worth offering for what has been typed of a
// recipient, best first, leaving out anyone the field already names.
//
// A row whose name has a word starting with the query, or whose address starts
// with it, beats one that only contains it somewhere: "an" finds Annie Bryan
// before Joanna. Within each of those HEY's order stands. The scan stops as soon
// as the better kind has filled the list, so a long address book costs a
// keystroke only as much of itself as it takes to find a few matches.
func matchRecipients(all []recipientSuggestion, query string, chosen map[string]bool) []recipientSuggestion {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	wordStart := " " + query
	var best, rest []recipientSuggestion
	for i := range all {
		row := &all[i]
		if row.chosen(chosen) {
			continue
		}
		switch {
		case strings.HasPrefix(row.name, query) || strings.HasPrefix(row.address, query) ||
			strings.Contains(row.name, wordStart):
			best = append(best, *row)
			if len(best) == maxRecipientSuggestions {
				return best
			}
		case len(rest) < maxRecipientSuggestions &&
			(strings.Contains(row.name, query) || strings.Contains(row.address, query)):
			rest = append(rest, *row)
		}
	}
	return append(best, rest[:min(len(rest), maxRecipientSuggestions-len(best))]...)
}

// chosenAddresses answers the bare addresses a recipient list already holds,
// lower-cased, leaving out the one being typed.
func chosenAddresses(list string, start, end int) map[string]bool {
	chosen := map[string]bool{}
	for _, recipient := range mail.SplitAddresses(list[:start] + list[end:]) {
		address := recipient
		if open := strings.LastIndexByte(recipient, '<'); open >= 0 {
			address = strings.TrimSuffix(recipient[open+1:], ">")
		}
		chosen[strings.ToLower(strings.TrimSpace(address))] = true
	}
	return chosen
}

// recipientText is what picking a row writes into the field: a person as
// Name <address>, and a group as the addresses it stands for, because HEY takes
// addresses rather than the name of a group.
func recipientText(s recipientSuggestion) string {
	if s.group() {
		return strings.Join(mail.SplitAddresses(s.value), ", ")
	}
	return mail.FormatAddress(s.label, s.value)
}

// recipientPopover is the list open under a recipient field. It belongs to the
// field it was opened for and to the recipient being typed there, whose byte
// range in the field it keeps so that picking a row replaces that recipient and
// nothing else.
type recipientPopover struct {
	field   composeField
	matches []recipientSuggestion
	cursor  int
	start   int
	end     int
}

func (p *recipientPopover) selected() recipientSuggestion {
	return p.matches[p.cursor]
}

func (p *recipientPopover) move(delta int) {
	p.cursor = min(max(p.cursor+delta, 0), len(p.matches)-1)
}

// isRecipientField reports whether a form field takes addresses.
func isRecipientField(field int) bool {
	return field == int(fieldTo) || field == int(fieldCc) || field == int(fieldBcc)
}

// refreshSuggestions opens, narrows or closes the list for the recipient under
// the cursor in the focused field.
func (f *composeForm) refreshSuggestions() {
	f.suggest = nil
	if !isRecipientField(f.focus) || len(f.recipients) == 0 {
		return
	}
	input := &f.inputs[f.focus]
	value := input.Value()
	cursor := byteOffset(value, input.Position())
	start, end := mail.AddressAt(value, cursor)
	query := value[start:cursor]
	matches := matchRecipients(f.recipients, query, chosenAddresses(value, start, end))
	if len(matches) == 0 {
		return
	}
	// A recipient typed out in full needs nothing suggested for it.
	if len(matches) == 1 && strings.EqualFold(strings.TrimSpace(value[start:end]), matches[0].value) {
		return
	}
	f.suggest = &recipientPopover{field: composeField(f.focus), matches: matches, start: start, end: end}
}

// acceptSuggestion writes the highlighted row in place of the recipient being
// typed, and leaves the cursor ready for the next one.
func (f *composeForm) acceptSuggestion() {
	p := f.suggest
	f.suggest = nil
	f.typing = false
	if p == nil || int(p.field) != f.focus {
		return
	}
	input := &f.inputs[p.field]
	value := input.Value()
	if p.end > len(value) {
		return
	}
	before := strings.TrimRight(value[:p.start], " ")
	if before != "" {
		before += " "
	}
	text := before + recipientText(p.selected())
	after := value[p.end:]
	if strings.TrimSpace(after) == "" {
		text += ", "
		after = ""
	}
	input.SetValue(text + after)
	input.SetCursor(len([]rune(text)))
}

// deleteRecipientBefore answers the list with the recipient just before a cursor at
// byte offset pos taken out whole, and where the cursor goes, when that recipient is
// written Name <address> — the form the picker writes. Backspace then takes it in one
// press, the way HEY's composer takes a recipient token, with its comma. A bare
// address is left to delete a letter at a time, since that is how a typo in one is
// fixed.
func deleteRecipientBefore(value string, pos int) (string, int, bool) {
	before := strings.TrimRight(value[:pos], " ")
	before = strings.TrimRight(strings.TrimSuffix(before, ","), " ")
	if !strings.HasSuffix(before, ">") {
		return "", 0, false
	}
	start, end := mail.AddressAt(value, len(before)-1)
	// The > has to be the recipient's last character. One inside a quoted name,
	// "Jane > Doe" <jane@example.com>, is a character like any other.
	if start+len(strings.TrimRight(value[start:end], " ")) != len(before) {
		return "", 0, false
	}
	recipient := strings.TrimSpace(value[start:end])
	open := strings.LastIndexByte(recipient, '<')
	if open <= 0 || strings.TrimSpace(recipient[:open]) == "" {
		return "", 0, false
	}

	head := strings.TrimRight(value[:start], " ")
	tail := strings.TrimLeft(value[pos:], " ")
	tail = strings.TrimLeft(strings.TrimPrefix(tail, ","), " ")
	if head == "" {
		return tail, 0, true
	}
	head += " "
	return head + tail, len(head), true
}

// handleSuggestionKey answers the keys the list takes while it is open: moving
// through it, picking from it and closing it. Everything else goes on to the
// field, which is what narrows the list.
func (f *composeForm) handleSuggestionKey(msg tea.KeyPressMsg) bool {
	if f.suggest == nil || int(f.suggest.field) != f.focus {
		return false
	}
	switch msg.String() {
	case "up", "ctrl+p":
		f.suggest.move(-1)
	case "down", "ctrl+n":
		f.suggest.move(1)
	case "tab", "enter":
		f.acceptSuggestion()
	case "esc":
		f.suggest = nil
		f.typing = false
	default:
		return false
	}
	return true
}

// recipientsLoaded hands the form HEY's list. Someone who started typing a
// recipient before it arrived gets their suggestions without typing again.
func (f *composeForm) recipientsLoaded(suggestions []recipientSuggestion) {
	f.recipients = suggestions
	if f.typing {
		f.refreshSuggestions()
	}
}

// suggestionsView draws the list, to be laid over the form under its field.
func (f *composeForm) suggestionsView() string {
	p := f.suggest
	width := min(max(f.width-composeLabelWidth-2, 20), 72)
	inner := width - 4 // border and padding
	selected := lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	rows := make([]string, 0, len(p.matches))
	for i, match := range p.matches {
		marker, name := "  ", match.label
		if i == p.cursor {
			marker = "› "
		}
		secondary := match.value
		if match.group() {
			secondary = match.detail
		}
		if secondary == name {
			secondary = ""
		}
		markerWidth := lipgloss.Width(marker)
		nameWidth := min(lipgloss.Width(name), max(inner-markerWidth, 1))
		name = truncateToWidth(name, nameWidth)
		line := marker + name
		if i == p.cursor {
			line = selected.Render(line)
		}
		if room := inner - markerWidth - nameWidth - 2; secondary != "" && room > 3 {
			line += "  " + styleMuted.Render(truncateToWidth(secondary, room))
		}
		rows = append(rows, line)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorChrome).
		Padding(0, 1).
		Width(width).
		Render(strings.Join(rows, "\n"))
}

// byteOffset converts a cursor position in runes, which is how a text input
// counts, into a byte offset into its value.
func byteOffset(s string, runes int) int {
	for i := range s {
		if runes == 0 {
			return i
		}
		runes--
	}
	return len(s)
}

// loadRecipients reads HEY's recipient list in the background. It is read
// every time a composer opens, so a person written to a minute ago is at the
// top of the list, but the SDK's response cache turns an unchanged list into a
// 304, and a composer that opens while the list is loading waits for that read
// rather than starting another.
func (v *mailView) loadRecipients() tea.Cmd {
	if v.vc.sdk == nil || v.recipientsLoading {
		return nil
	}
	v.recipientsLoading = true
	sdk, ctx := v.vc.sdk, v.vc.ctx
	return func() tea.Msg {
		rows, err := sdk.Contacts().Addressable(ctx, true)
		if err != nil {
			return recipientsLoadedMsg{err: err}
		}
		return recipientsLoadedMsg{suggestions: newRecipientSuggestions(rows)}
	}
}

// settleRecipients keeps the list a read answered, and hands it to a composer
// that is open. A failed read keeps the list from before: suggestions are a
// convenience, and typing an address works without them.
func (v *mailView) settleRecipients(msg recipientsLoadedMsg) {
	v.recipientsLoading = false
	if msg.err != nil {
		return
	}
	v.recipients = msg.suggestions
	if form := modalOf[*composeForm](v); form != nil {
		form.recipientsLoaded(v.recipients)
	}
}
