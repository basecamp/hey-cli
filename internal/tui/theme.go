package tui

import (
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is the accent overlay the TUI lays over the terminal's own ANSI palette.
//
// hey-cli deliberately styles with ANSI-16 colors so a terminal retint restyles the
// running TUI for free. A Theme only replaces the handful of semantic colors where a
// desktop theme has a better answer than "bright blue": the accent, the selection
// background, the muted tone, the emphasized foreground and the error color. Anything
// a theme file leaves out keeps its ANSI default.
type Theme struct {
	Accent    color.Color
	Selection color.Color // nil when the theme gives no selection background
	Muted     color.Color
	Bright    color.Color
	Error     color.Color

	// Dark reports whether the theme is for a dark background. HasMode is true when
	// a theme file said so; otherwise Dark is a guess the terminal can correct.
	Dark    bool
	HasMode bool

	// Trusted marks a theme the user pointed at explicitly (HEY_THEME). Trusted
	// values skip the accent and selection readability gates: the gates exist for
	// machine-derived palettes, not for a file chosen by hand.
	Trusted bool

	// Source names the file the overlay came from, "" for the ANSI defaults and
	// "NO_COLOR" when color is disabled.
	Source string
}

// defaultTheme is the palette the TUI has always used: pure ANSI-16, dark.
func defaultTheme() Theme {
	return Theme{
		Accent: lipgloss.BrightBlue,
		Muted:  lipgloss.BrightBlack,
		Bright: lipgloss.BrightWhite,
		Error:  lipgloss.Red,
		Dark:   true,
	}
}

// noColorTheme honors NO_COLOR: every color is unset, output is plain text.
func noColorTheme() Theme {
	nc := lipgloss.NoColor{}
	return Theme{Accent: nc, Muted: nc, Bright: nc, Error: nc, Dark: true, Source: "NO_COLOR"}
}

// ResolveTheme picks the active theme from the environment, in order:
//
//  1. NO_COLOR set → no color at all
//  2. HEY_THEME → a theme file at that path
//  3. Omarchy's rendered hey.toml in the current theme, when a theme ships or
//     templates one
//  4. Omarchy's colors.toml in the current theme
//  5. the ANSI defaults
//
// Only the first file that exists is read; within it, only the keys it provides
// override the defaults.
func ResolveTheme() Theme {
	return resolveTheme(os.Getenv, userHomeDir())
}

func resolveTheme(getenv func(string) string, home string) Theme {
	if getenv("NO_COLOR") != "" {
		return noColorTheme()
	}

	type candidate struct {
		path    string
		trusted bool
	}
	candidates := []candidate{}
	if path := getenv("HEY_THEME"); path != "" {
		candidates = append(candidates, candidate{filepath.Clean(path), true})
	}
	if dir := omarchyThemeDir(home); dir != "" {
		candidates = append(candidates,
			candidate{filepath.Join(dir, "hey.toml"), false},
			candidate{filepath.Join(dir, "colors.toml"), false})
	}

	for _, c := range candidates {
		data, err := os.ReadFile(c.path) //nolint:gosec // G304: theme paths come from the user's own environment
		if err != nil {
			continue
		}
		theme := overlayTheme(defaultTheme(), parseThemeFile(string(data)), c.trusted)
		theme.Source = c.path
		theme.Trusted = c.trusted
		return theme
	}
	return defaultTheme()
}

// omarchyStateDir is where Omarchy keeps the active theme. Omarchy writes it under
// $HOME/.local/state unconditionally, so XDG_STATE_HOME is deliberately ignored.
func omarchyStateDir(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "omarchy")
}

// omarchyThemeDir returns the active Omarchy theme directory, or "" when this
// machine does not run Omarchy.
func omarchyThemeDir(home string) string {
	dir := omarchyStateDir(home)
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Join(dir, "current", "theme")
}

// omarchyWatchDir returns the directory to watch for theme switches, or "" when
// there is nothing to watch. omarchy-theme-set swaps the whole theme directory
// with an atomic mv, so the watch has to sit on the parent: a watch inside the
// swapped directory dies with the old inode.
func omarchyWatchDir(home string) string {
	dir := omarchyThemeDir(home)
	if dir == "" {
		return ""
	}
	return filepath.Dir(dir)
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// parseThemeFile reads the `key = "value"` lines of a theme file. It understands
// exactly the subset Omarchy's colors.toml uses: one scalar per line, double or
// single quotes, # comments. Anything else is skipped rather than rejected so a
// theme author's extra keys never break the TUI.
func parseThemeFile(data string) map[string]string {
	values := make(map[string]string)
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if idx := unquotedIndex(value, '#'); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		value = strings.Trim(value, `"'`)
		if key != "" && value != "" {
			values[strings.ToLower(key)] = value
		}
	}
	return values
}

// unquotedIndex returns the index of the first c outside quotes, or -1.
func unquotedIndex(s string, c rune) int {
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == c:
			return i
		}
	}
	return -1
}

// overlayTheme lays the keys of a parsed theme file over base. The hey.toml keys
// (accent, selection, muted, foreground, error, mode) and the colors.toml spellings
// (red, bright_foreground) share one parser. Emphasis prefers bright_foreground:
// Omarchy's terminal templates map ANSI bright white to it, and plain foreground
// is measurably dimmer on most themes. A trusted file's accent is used as written;
// only machine-derived palettes go through the readability gate.
func overlayTheme(base Theme, values map[string]string, trusted bool) Theme {
	theme := base
	if c, ok := themeColor(values, "accent"); ok {
		theme.Accent = c
		if !trusted {
			theme.Accent = usableAccent(c, values, base.Accent)
		}
	}
	if c, ok := themeColor(values, "selection"); ok {
		theme.Selection = c
	}
	if c, ok := themeColor(values, "muted"); ok {
		theme.Muted = c
	}
	if c, ok := themeColor(values, "bright_foreground", "foreground"); ok {
		theme.Bright = c
	}
	if c, ok := themeColor(values, "error", "red"); ok {
		theme.Error = c
	}
	switch strings.ToLower(values["mode"]) {
	case "dark":
		theme.Dark, theme.HasMode = true, true
	case "light":
		theme.Dark, theme.HasMode = false, true
	}
	return theme
}

// A theme's accent is a UI tint, not always a text highlight: kanagawa's is its
// foreground color, osaka-jade's is a deep jade dimmer than anything else on
// screen. usableAccent keeps the accent when it can do the cursor row's job —
// visibly different from the emphasis text, and at least as readable on the
// background as the theme's bright blue — and otherwise falls back to plain ANSI
// bright blue, so the cursor row always matches the palette the rest of the text
// is rendered in. That matters in foot, where a running window keeps the palette
// it opened with: painting the new theme's hex there would mix palettes, while
// ANSI stays self-consistent. The theme's bright_blue hex is used only to judge
// the accent, never painted.
const (
	minAccentDistance = 64.0 // sRGB distance from the emphasis text
	minAccentContrast = 5.0  // contrast against the background that wins outright
)

func usableAccent(accent color.Color, values map[string]string, ansiBlue color.Color) color.Color {
	if text, ok := themeColor(values, "bright_foreground", "foreground"); ok && colorDistance(accent, text) < minAccentDistance {
		return ansiBlue
	}
	background, ok := themeColor(values, "background")
	if !ok {
		return accent
	}
	estimate, ok := themeColor(values, "bright_blue", "blue")
	if !ok {
		estimate = ansiBlue
	}
	if ratio := contrastRatio(accent, background); ratio >= minAccentContrast || ratio >= contrastRatio(estimate, background) {
		return accent
	}
	return ansiBlue
}

// colorDistance is the Euclidean distance between two colors in 8-bit sRGB.
func colorDistance(a, b color.Color) float64 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) float64 { return float64(x>>8) - float64(y>>8) }
	return math.Sqrt(d(ar, br)*d(ar, br) + d(ag, bg)*d(ag, bg) + d(ab, bb)*d(ab, bb))
}

// themeColor returns the first key that holds a valid hex color.
func themeColor(values map[string]string, keys ...string) (color.Color, bool) {
	for _, key := range keys {
		if hex, ok := values[key]; ok && isHexColor(hex) {
			return lipgloss.Color(hex), true
		}
	}
	return nil, false
}

func isHexColor(s string) bool {
	if !strings.HasPrefix(s, "#") {
		return false
	}
	digits := s[1:]
	if len(digits) != 3 && len(digits) != 6 {
		return false
	}
	for _, r := range digits {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'f'
		isUpper := r >= 'A' && r <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}
