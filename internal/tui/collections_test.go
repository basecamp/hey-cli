package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

func TestCollectionPickerConstrainsLongNamesToModalWidth(t *testing.T) {
	picker := newCollectionPicker([]models.Box{{
		ID:   12,
		Kind: mailSourceKindFolder,
		Name: "Receipts and invoices from every supplier for the annual financial review",
	}}, 0)

	const width = 30
	view := stripANSI(picker.overlay("", width, 10))
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
		t.Fatal("picker did not render the selected collection")
	}
}
