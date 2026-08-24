package tui

import (
	"strings"
	"testing"
)

func TestProbeRequestCarriesEveryProbe(t *testing.T) {
	request := probeRequest()
	for _, probe := range widthProbes {
		if !strings.Contains(request, "\r"+probe+cursorReport) {
			t.Errorf("request is missing the probe for %q", probe)
		}
	}
	if !strings.HasPrefix(request, probeSetup) || !strings.HasSuffix(request, probeCleanup) {
		t.Error("request does not restore the terminal around the probes")
	}
}

func TestParseCursorReports(t *testing.T) {
	reports := []byte("\x1b[12;3R\x1b[12;2R\x1b[12;3R")
	if got := parseCursorReports(reports); len(got) != 3 || got[0] != 3 || got[1] != 2 || got[2] != 3 {
		t.Errorf("parsed %v, want [3 2 3]", got)
	}

	// A keystroke or a stray escape landing mid-probe must not derail the reports behind it.
	noisy := []byte("q\x1b[12;3Rw\x1b[A\x1b[5;17R\x1b[9999;99999R\x1b[;R")
	if got := parseCursorReports(noisy); len(got) != 2 || got[0] != 3 || got[1] != 17 {
		t.Errorf("parsed noisy %v, want [3 17]", got)
	}

	if got := parseCursorReports([]byte("no reports here")); len(got) != 0 {
		t.Errorf("parsed %v from junk, want none", got)
	}
}

func TestDeriveWidths(t *testing.T) {
	// Columns are one past the width drawn: की=2, ✈️=2, 🇭🇷=2, 👍🏽=2, family=2.
	probed, ok := deriveWidths([]int{3, 3, 3, 3, 3})
	if !ok {
		t.Fatal("plausible columns were rejected")
	}
	want := clusterWidths{spacingMark: 1, vs16: 2, flagPair: 2, skinTone: 2, zwjJoined: true}
	if probed != want {
		t.Errorf("derived %+v, want %+v", probed, want)
	}

	// A terminal that keeps matras narrow, draws VS16 in text width, and splits families.
	probed, ok = deriveWidths([]int{2, 2, 3, 5, 7})
	if !ok {
		t.Fatal("plausible columns were rejected")
	}
	want = clusterWidths{spacingMark: 0, vs16: 1, flagPair: 2, skinTone: 4, zwjJoined: false}
	if probed != want {
		t.Errorf("derived %+v, want %+v", probed, want)
	}

	if _, ok := deriveWidths([]int{3, 3, 3}); ok {
		t.Error("a short answer was trusted")
	}
	if _, ok := deriveWidths([]int{3, 3, 3, 3, 42}); ok {
		t.Error("an implausible column was trusted")
	}
	if _, ok := deriveWidths([]int{1, 3, 3, 3, 3}); ok {
		t.Error("a zero width was trusted")
	}
}
