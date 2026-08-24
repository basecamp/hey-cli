package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Terminals disagree about exactly the clusters the width rules leave undefined — a
// spacing combining mark like a Devanagari matra, a text-default symbol forced to emoji
// by a variation selector, a joined emoji on a terminal that does not join. The
// disagreement is not cosmetic: the terminal advances the cursor by what it drew, so
// every cell to the right of such a glyph shifts and every aligned row it sits on
// shears. No width table is right everywhere, so these widths are asked of the terminal
// itself at startup (probeWidths) and kept here. Without an answer the guess errs wide:
// over-allocating shows as a one-cell gap beside the glyph, under-allocating shears the
// row.
type clusterWidths struct {
	spacingMark int  // cells one spacing combining mark adds to its base
	vs16        int  // cells for a text-default base forced to emoji presentation
	flagPair    int  // cells for a regional-indicator pair
	skinTone    int  // cells for an emoji carrying a skin-tone modifier
	zwjJoined   bool // whether a ZWJ sequence draws as one glyph
}

var widths = clusterWidths{spacingMark: 1, vs16: 2, flagPair: 2, skinTone: 2, zwjJoined: true}

const (
	zwj  = '\u200d'
	vs16 = '\ufe0f'
)

// displayWidth is the number of terminal cells s occupies, by the calibrated widths.
// Styling is not counted, so a styled cell measures what is visible, as lipgloss.Width
// does.
func displayWidth(s string) int {
	total := 0
	for rest := ansi.Strip(s); rest != ""; {
		cluster, _ := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		total += clusterCells(cluster)
		rest = rest[len(cluster):]
	}
	return total
}

// firstCluster returns the leading grapheme cluster of s and the cells it occupies.
func firstCluster(s string) (string, int) {
	cluster, _ := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
	return cluster, clusterCells(cluster)
}

func clusterCells(cluster string) int {
	if strings.ContainsRune(cluster, zwj) {
		width := 0
		for part := range strings.SplitSeq(cluster, string(zwj)) {
			if widths.zwjJoined {
				width = max(width, clusterCells(part))
			} else {
				width += clusterCells(part)
			}
		}
		return width
	}
	if isFlagPair(cluster) {
		return widths.flagPair
	}
	if strings.ContainsFunc(cluster, isSkinToneModifier) {
		return widths.skinTone
	}
	if strings.ContainsRune(cluster, vs16) {
		base := strings.ReplaceAll(cluster, string(vs16), "")
		if ansi.StringWidth(base) >= 2 {
			return 2
		}
		return widths.vs16
	}
	if marks := countSpacingMarks(cluster); marks > 0 {
		base := strings.Map(dropSpacingMark, cluster)
		return ansi.StringWidth(base) + marks*widths.spacingMark
	}
	return ansi.StringWidth(cluster)
}

func isFlagPair(cluster string) bool {
	runes := []rune(cluster)
	return len(runes) == 2 && isRegionalIndicator(runes[0]) && isRegionalIndicator(runes[1])
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}

func isSkinToneModifier(r rune) bool {
	return r >= 0x1f3fb && r <= 0x1f3ff
}

func countSpacingMarks(cluster string) int {
	marks := 0
	for _, r := range cluster {
		if unicode.Is(unicode.Mc, r) {
			marks++
		}
	}
	return marks
}

func dropSpacingMark(r rune) rune {
	if unicode.Is(unicode.Mc, r) {
		return -1
	}
	return r
}
