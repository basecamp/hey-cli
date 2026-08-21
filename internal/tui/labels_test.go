package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/mail"
)

func TestLabelPickerConstrainsLongNamesToModalWidth(t *testing.T) {
	picker := newLabelPicker([]mail.Source{{
		Kind: mail.KindFolder,
		ID:   12,
		Name: "Receipts and invoices from every supplier for the annual financial review",
	}}, 0)

	const width = 30
	view := stripANSI(picker.overlay("", width, 10))
	if !strings.Contains(view, "Labels") || strings.Contains(view, "Collections") {
		t.Errorf("picker title should identify folders as Labels: %q", view)
	}
	foundSelection := false
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > width {
			t.Errorf("picker line width = %d, want at most %d: %q", lipgloss.Width(line), width, line)
		}
		if !strings.Contains(line, "› ") {
			continue
		}
		foundSelection = true
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "│") || !strings.HasSuffix(trimmed, "│") {
			t.Errorf("selected row should keep both modal borders: %q", line)
		}
	}
	if !foundSelection {
		t.Fatal("picker did not render the selected label")
	}
}
