package habit

import (
	"strings"
	"testing"
)

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
