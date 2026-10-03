package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A terminal speaking the kitty keyboard protocol reports Num Lock and Caps Lock
// as modifiers. Every form matches Shift+Tab by its keystroke, which leaves the
// lock keys out; TestComposeShiftTabGoesBackWithALockKeyOn holds the composer to
// that, and this holds the other forms.
func TestFormsGoBackOnShiftTabWithALockKeyOn(t *testing.T) {
	locks := []tea.KeyMod{0, tea.ModNumLock, tea.ModCapsLock, tea.ModNumLock | tea.ModCapsLock}
	shiftTab := func(lock tea.KeyMod) tea.KeyPressMsg {
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift | lock})
	}

	for _, lock := range locks {
		contact := newContactForm(contactFormAdd, Contact{}, newStyles())
		contact.handleKey(shiftTab(lock))
		if want := len(contact.inputs) - 1; contact.focus != want {
			t.Errorf("contact form, lock %v: shift+tab from the first field went to %d, want %d", lock, contact.focus, want)
		}

		habit := newHabitForm(habitFormCreate, Recording{}, newStyles())
		habit.handleKey(shiftTab(lock))
		if want := habitFieldCount - 1; habit.focus != want {
			t.Errorf("habit form, lock %v: shift+tab from the first field went to %d, want %d", lock, habit.focus, want)
		}
	}
}
