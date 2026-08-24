package tui

import (
	"strings"
)

// The probe clusters, one per class whose width no rule pins down. Each is printed at
// column one followed by a cursor position report, so the column the terminal answers is
// the width it actually drew — the one measurement no table can contradict. Mode 2027 is
// switched on for the probe because Bubble Tea runs the session with it wherever the
// terminal supports it, and a terminal that ignores the mode probes the same either way.
var widthProbes = []string{
	"की",    // base plus a spacing combining mark
	"✈️",    // text-default base forced to emoji presentation
	"🇭🇷",    // regional-indicator pair
	"👍🏽",    // emoji with a skin-tone modifier
	"👨‍👩‍👧", // ZWJ sequence
}

const (
	probeSetup    = "\x1b[?2026h\x1b[?25l\x1b[?2027h"
	probeCleanup  = "\r\x1b[K\x1b[?2027l\x1b[?25h\x1b[?2026l"
	cursorReport  = "\x1b[6n"
	probeMaxWidth = 8
)

func probeRequest() string {
	var b strings.Builder
	b.WriteString(probeSetup)
	for _, probe := range widthProbes {
		b.WriteString("\r" + probe + cursorReport)
	}
	b.WriteString(probeCleanup)
	return b.String()
}

// parseCursorReports pulls the column out of every ESC[row;colR in data, in order,
// skipping anything else — a keystroke landing mid-probe must not derail the answers
// behind it.
func parseCursorReports(data []byte) []int {
	columns := []int{}
	for i := 0; i < len(data); i++ {
		if col, length, ok := cursorReportAt(data[i:]); ok {
			columns = append(columns, col)
			i += length - 1
		}
	}
	return columns
}

func cursorReportAt(data []byte) (col, length int, ok bool) {
	if len(data) < 2 || data[0] != 0x1b || data[1] != '[' {
		return 0, 0, false
	}
	i := 2
	if _, i = digitsAt(data, i); i < 0 || i >= len(data) || data[i] != ';' {
		return 0, 0, false
	}
	if col, i = digitsAt(data, i+1); i < 0 || i >= len(data) || data[i] != 'R' {
		return 0, 0, false
	}
	return col, i + 1, true
}

func digitsAt(data []byte, i int) (value, next int) {
	start := i
	for i < len(data) && data[i] >= '0' && data[i] <= '9' {
		value = value*10 + int(data[i]-'0')
		i++
	}
	if i == start || i-start > 4 {
		return 0, -1
	}
	return value, i
}

// deriveWidths turns the reported columns into a width table. A report outside the
// plausible range means the probe was disturbed — a resize, a wrapped line — and the
// whole calibration is discarded rather than half-trusted.
func deriveWidths(columns []int) (clusterWidths, bool) {
	if len(columns) != len(widthProbes) {
		return clusterWidths{}, false
	}
	measured := make([]int, len(columns))
	for i, column := range columns {
		width := column - 1
		if width < 1 || width > probeMaxWidth {
			return clusterWidths{}, false
		}
		measured[i] = width
	}
	return clusterWidths{
		spacingMark: measured[0] - 1,
		vs16:        measured[1],
		flagPair:    measured[2],
		skinTone:    measured[3],
		zwjJoined:   measured[4] <= 2,
	}, true
}
