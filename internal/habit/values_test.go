package habit

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Every icon's emoji must measure two cells. An emoji whose default presentation is
// text — the ones a variation selector would have to widen — measures one cell in some
// terminals and two in others, which slides everything to its right by an amount
// nothing here can know.
func TestEveryHabitEmojiIsTwoCellsWide(t *testing.T) {
	for _, icon := range Icons {
		if width := lipgloss.Width(icon.Emoji); width != 2 {
			t.Errorf("%s emoji %q is %d cells wide, want 2", icon.Name, icon.Emoji, width)
		}
		if strings.ContainsRune(icon.Emoji, '️') {
			t.Errorf("%s emoji %q carries a variation selector", icon.Name, icon.Emoji)
		}
	}
}

func TestEmojiForAnswersNothingForAnUnknownIcon(t *testing.T) {
	if got := EmojiFor("read"); got != "📖" {
		t.Errorf("EmojiFor(read) = %q", got)
	}
	if got := EmojiFor("hovercraft"); got != "" {
		t.Errorf("EmojiFor(hovercraft) = %q, want nothing", got)
	}
}

func TestValidateIconAcceptsEveryIconValue(t *testing.T) {
	for _, icon := range strings.Split(IconValues, ", ") {
		if err := ValidateIcon(icon); err != nil {
			t.Errorf("ValidateIcon(%q) = %v", icon, err)
		}
	}
}

func TestValidateColorAcceptsEveryColorValue(t *testing.T) {
	for _, color := range strings.Split(ColorValues, ", ") {
		if err := ValidateColor(color); err != nil {
			t.Errorf("ValidateColor(%q) = %v", color, err)
		}
	}
}

func TestHabitValuesRejectUnsupportedNames(t *testing.T) {
	if err := ValidateIcon("walking"); err == nil {
		t.Error("ValidateIcon accepted walking")
	}
	if err := ValidateColor("orange"); err == nil {
		t.Error("ValidateColor accepted orange")
	}
}
