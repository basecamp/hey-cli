package tui

import "testing"

func withWidths(t *testing.T, calibrated clusterWidths) {
	t.Helper()
	previous := widths
	widths = calibrated
	t.Cleanup(func() { widths = previous })
}

// The same text measures differently on terminals that answered the probe differently.
// The table pins both worlds: one that draws spacing marks and forced-emoji wide, and one
// that keeps them narrow.
func TestDisplayWidthFollowsTheCalibration(t *testing.T) {
	wide := clusterWidths{spacingMark: 1, vs16: 2, flagPair: 2, skinTone: 2, zwjJoined: true}
	narrow := clusterWidths{spacingMark: 0, vs16: 1, flagPair: 2, skinTone: 4, zwjJoined: false}

	cases := []struct {
		s            string
		wide, narrow int
	}{
		{"hello", 5, 5},
		{"漢字", 4, 4},
		{"café", 4, 4},        // combining marks are Mn and add nothing anywhere
		{"की", 2, 1},          // spacing matra
		{"बैठक", 3, 3},        // ै is Mn, no spacing marks here
		{"✈️", 2, 1},          // text-default base + VS16
		{"☑️", 2, 1},          // same
		{"😀️", 2, 2},          // VS16 on an already-wide base changes nothing
		{"🇭🇷", 2, 2},          // flag pair
		{"👍🏽", 2, 4},          // skin tone
		{"👨‍👩‍👧", 2, 6},       // ZWJ family: joined or sum of parts
		{"a👨‍👩‍👧b की", 7, 10}, // mixed, including the space
	}
	for _, c := range cases {
		withWidths(t, wide)
		if got := displayWidth(c.s); got != c.wide {
			t.Errorf("wide: displayWidth(%q) = %d, want %d", c.s, got, c.wide)
		}
		withWidths(t, narrow)
		if got := displayWidth(c.s); got != c.narrow {
			t.Errorf("narrow: displayWidth(%q) = %d, want %d", c.s, got, c.narrow)
		}
	}
}

func TestDisplayWidthIgnoresStyling(t *testing.T) {
	if got := displayWidth("\x1b[31mकी\x1b[0m"); got != displayWidth("की") {
		t.Errorf("styled width = %d, want %d", got, displayWidth("की"))
	}
}

func TestFirstClusterMeasuresLikeDisplayWidth(t *testing.T) {
	for s := "Fly to 🇭🇷 की ✈️!"; s != ""; {
		cluster, width := firstCluster(s)
		if cluster == "" {
			t.Fatalf("firstCluster stalled on %q", s)
		}
		if width != displayWidth(cluster) {
			t.Errorf("firstCluster(%q) width = %d, displayWidth = %d", cluster, width, displayWidth(cluster))
		}
		s = s[len(cluster):]
	}
}
