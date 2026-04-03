package tui

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// --- Hourglass loading animation ---

const (
	hourglassVisualWidth = 11  // visual cell width of each frame line
	hourglassDrainFrames = 7   // frames where sand falls
	hourglassFlipFrames  = 4   // frames for the rotation
	hourglassTotalFrames = 11  // drain + flip
	hourglassDrainPhase  = 9.0 // ~3 sec of drain (60 ticks × 0.15)
	hourglassFlipPhase   = 2.4 // ~0.8 sec of flip (16 ticks × 0.15)
	hourglassCyclePhase  = hourglassDrainPhase + hourglassFlipPhase
)

// hourglassFrames defines each frame of the hourglass animation.
// Frames 0-6: sand drains from top to bottom over ~3 seconds.
// Frames 7-10: hourglass rotates 180° — tilts to horizontal, continues
// to inverted, then the next drain cycle starts upright again.
var hourglassFrames = [hourglassTotalFrames][]string{
	{ // 0: full top, empty bottom
		"   ╭───╮   ",
		"   │⣿⣿⣿│   ",
		"   ╰╮⣿╭╯   ",
		"   ╭╯ ╰╮   ",
		"   │   │   ",
		"   ╰───╯   ",
	},
	{ // 1: grain falling
		"   ╭───╮   ",
		"   │⣿⣿⣿│   ",
		"   ╰╮ ╭╯   ",
		"   ╭╯⡇╰╮   ",
		"   │ ⣀ │   ",
		"   ╰───╯   ",
	},
	{ // 2
		"   ╭───╮   ",
		"   │⣿ ⣿│   ",
		"   ╰╮ ╭╯   ",
		"   ╭╯⡇╰╮   ",
		"   │⣀⣤⣀│   ",
		"   ╰───╯   ",
	},
	{ // 3
		"   ╭───╮   ",
		"   │⣀ ⣀│   ",
		"   ╰╮ ╭╯   ",
		"   ╭╯⡇╰╮   ",
		"   │⣤⣤⣤│   ",
		"   ╰───╯   ",
	},
	{ // 4
		"   ╭───╮   ",
		"   │   │   ",
		"   ╰╮ ╭╯   ",
		"   ╭╯⡇╰╮   ",
		"   │⣿⣤⣿│   ",
		"   ╰───╯   ",
	},
	{ // 5: last grain
		"   ╭───╮   ",
		"   │   │   ",
		"   ╰╮ ╭╯   ",
		"   ╭╯⡇╰╮   ",
		"   │⣿⣿⣿│   ",
		"   ╰───╯   ",
	},
	{ // 6: empty top, full bottom
		"   ╭───╮   ",
		"   │   │   ",
		"   ╰╮ ╭╯   ",
		"   ╭╯ ╰╮   ",
		"   │⣿⣿⣿│   ",
		"   ╰───╯   ",
	},
	// --- Rotation frames ---
	{ // 7: ~45° tilt — top half shifts left, bottom shifts right
		"╭───╮      ",
		" │   │     ",
		"  ╰╮ ╭╯    ",
		"    ╭╯ ╰╮  ",
		"     │⣿⣿⣿│ ",
		"      ╰───╯",
	},
	{ // 8: 90° horizontal — sand on right
		"           ",
		"╭───╮ ╭───╮",
		"│    ⣠⣾⣿⣿⣿│",
		"╰───╯ ╰───╯",
		"           ",
		"           ",
	},
	{ // 9: ~135° tilt — sand now top-right, empty bottom-left
		"      ╭───╮",
		"     │⣿⣿⣿│ ",
		"    ╰╮ ╭╯  ",
		"  ╭╯ ╰╮    ",
		" │   │     ",
		"╰───╯      ",
	},
	{ // 10: 180° inverted — sand at top, glass upside-down
		"   ╭───╮   ",
		"   │⣿⣿⣿│   ",
		"   ╰╮⣿╭╯   ",
		"   ╭╯ ╰╮   ",
		"   │   │   ",
		"   ╰───╯   ",
	},
}

// spinnerTick returns a command that ticks every 50ms for the loading animation.
func spinnerTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// hourglassFrameIndex maps a continuous phase value to a frame index.
// Drain frames play slower (~430ms each), flip frames play faster (~200ms each).
func hourglassFrameIndex(phase float64) int {
	p := math.Mod(phase, hourglassCyclePhase)
	if p < hourglassDrainPhase {
		idx := int(p * float64(hourglassDrainFrames) / hourglassDrainPhase)
		return min(idx, hourglassDrainFrames-1)
	}
	fp := p - hourglassDrainPhase
	idx := int(fp * float64(hourglassFlipFrames) / hourglassFlipPhase)
	return hourglassDrainFrames + min(idx, hourglassFlipFrames-1)
}

// loadingView renders an animated hourglass centered in the content area.
func loadingView(width, contentHeight int, phase float64) string {
	if width < hourglassVisualWidth || contentHeight < 2 {
		return "Loading..."
	}

	frame := hourglassFrames[hourglassFrameIndex(phase)]

	glass := lipgloss.NewStyle().Foreground(colorPrimary)
	label := lipgloss.NewStyle().Foreground(colorMuted)

	// Build centered lines
	padLeft := (width - hourglassVisualWidth) / 2
	if padLeft < 0 {
		padLeft = 0
	}
	prefix := strings.Repeat(" ", padLeft)

	var lines []string
	for _, line := range frame {
		lines = append(lines, prefix+glass.Render(line))
	}

	// "Loading..." label centered below
	labelText := "Loading..."
	labelPad := (width - len(labelText)) / 2
	if labelPad < 0 {
		labelPad = 0
	}
	lines = append(lines, strings.Repeat(" ", labelPad)+label.Render(labelText))

	// Vertical centering
	totalLines := len(lines)
	topPad := (contentHeight - totalLines) / 2
	if topPad < 0 {
		topPad = 0
	}

	var b strings.Builder
	for range topPad {
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(lines, "\n"))

	return b.String()
}
