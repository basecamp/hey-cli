package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestParseLayout(t *testing.T) {
	for input, want := range map[string]Layout{
		"classic":    LayoutClassic,
		"CLASSIC":    LayoutClassic,
		"spacious":   LayoutSpacious,
		" Spacious ": LayoutSpacious,
	} {
		got, err := ParseLayout(input)
		if err != nil || got != want {
			t.Errorf("ParseLayout(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseLayout("roomy"); err == nil {
		t.Error("ParseLayout accepted an unknown layout")
	}
}

func TestSpaciousLayoutAddsVerticalSpaceAtRoomySizes(t *testing.T) {
	metrics := LayoutSpacious.metrics(80, 40)
	if !metrics.spacious || metrics.paddingX != 0 || metrics.paddingY != 1 || metrics.itemGap != 1 {
		t.Fatalf("spacious metrics = %+v", metrics)
	}
	if metrics.headerGap() != 1 || metrics.footerChrome() != 0 || metrics.drawFooterRule() {
		t.Fatalf("spacious vertical spacing = header %d, footer %d, rule %t", metrics.headerGap(), metrics.footerChrome(), metrics.drawFooterRule())
	}

	rendered := metrics.render("Mail", 80, 20)
	if width, height := lipgloss.Width(rendered), lipgloss.Height(rendered); width != 80 || height != 20 {
		t.Errorf("spacious layout = %dx%d, want 80x20", width, height)
	}
	plain := stripANSI(rendered)
	if strings.ContainsAny(plain, "┌┐└┘│─") {
		t.Errorf("spacious layout drew a border:\n%s", plain)
	}
	if !strings.Contains(plain, "\nMail") {
		t.Errorf("spacious layout lacks its vertical padding:\n%s", plain)
	}
}

func TestSpaciousLayoutCollapsesOnSmallTerminals(t *testing.T) {
	for _, size := range [][2]int{{47, 40}, {80, 15}} {
		metrics := LayoutSpacious.metrics(size[0], size[1])
		if metrics.spacious || metrics.itemGap != 0 {
			t.Errorf("spacious metrics at %dx%d = %+v, want collapsed chrome", size[0], size[1], metrics)
		}
	}
}

func TestControlGTogglesLayoutAndResizesTheActiveView(t *testing.T) {
	m := modelWithBoxes()
	classicWidth, classicHeight := m.vc.width, m.vc.height

	updated, cmd := m.Update(keyPress("ctrl+g"))
	m = updated.(model)
	if m.layout != LayoutSpacious || m.vc.layout.itemGap != 1 {
		t.Fatalf("layout after ctrl+g = %q with metrics %+v", m.layout, m.vc.layout)
	}
	if m.vc.width != classicWidth || m.vc.height != classicHeight {
		t.Errorf("spacious content = %dx%d, classic was %dx%d", m.vc.width, m.vc.height, classicWidth, classicHeight)
	}
	if cmd == nil {
		t.Fatal("layout toggle did not announce the new layout")
	}

	updated, _ = m.Update(keyPress("ctrl+g"))
	m = updated.(model)
	if m.layout != LayoutClassic || m.vc.width != classicWidth || m.vc.height != classicHeight {
		t.Errorf("classic layout was not restored: layout=%q size=%dx%d", m.layout, m.vc.width, m.vc.height)
	}
}

func TestSpaciousMailRowsHaveNegativeSpace(t *testing.T) {
	m := modelWithBoxes()
	m.layout = LayoutSpacious
	m.resizeActiveView()
	view := stripANSI(m.mailView.View())
	if !strings.Contains(view, "\n\nPreviously Seen") {
		t.Errorf("spacious mail rows have no blank row between them:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "Previously Seen") {
			if i+1 >= len(lines) || lines[i+1] != "" {
				t.Errorf("spacious mail section has no blank row below its header:\n%s", view)
			}
			return
		}
	}
	t.Errorf("spacious mail list lacks its Previously Seen section:\n%s", view)
}
