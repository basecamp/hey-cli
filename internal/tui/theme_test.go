package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TestMain isolates HOME: newModel resolves the live Omarchy theme from it, and
// the rendering tests assert exact ANSI output, so a developer's active theme
// must never leak into them. Tests that want an Omarchy theme build one with
// omarchyHome and point HOME at it themselves.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "hey-tui-test-home-")
	if err == nil {
		os.Setenv("HOME", home)
	}
	os.Unsetenv("HEY_THEME")
	os.Unsetenv("NO_COLOR")
	code := m.Run()
	if err == nil {
		os.RemoveAll(home)
	}
	os.Exit(code)
}

func envOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// omarchyHome builds a fake $HOME with an Omarchy state dir holding the given
// theme files and returns the home path.
func omarchyHome(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	themeDir := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(themeDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

const tokyoNightColors = `mode = "dark"

accent = "#7aa2f7"
selection = "#292e42"
muted = "#414868"

background = "#1a1b26"
foreground = "#a9b1d6"
bright_foreground = "#c0caf5"

red = "#f7768e"
blue = "#7aa2f7"
`

func TestParseThemeFile(t *testing.T) {
	values := parseThemeFile(`
# a comment
mode = "light"
accent = '#ABCDEF' # trailing comment
selection="#123"
[section]
odd line without equals
empty =
not_hex = "blue"
`)
	want := map[string]string{"mode": "light", "accent": "#ABCDEF", "selection": "#123", "not_hex": "blue"}
	if len(values) != len(want) {
		t.Fatalf("parsed %v, want %v", values, want)
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("%s = %q, want %q", k, values[k], v)
		}
	}
}

func TestOverlayThemeKeepsDefaultsForMissingKeys(t *testing.T) {
	theme := overlayTheme(defaultTheme(), map[string]string{"accent": "#7aa2f7", "not_hex": "blue", "error": "nope"}, false)
	if theme.Accent != lipgloss.Color("#7aa2f7") {
		t.Errorf("accent not overlaid: %v", theme.Accent)
	}
	if theme.Muted != lipgloss.BrightBlack || theme.Error != lipgloss.Red || theme.Bright != lipgloss.BrightWhite {
		t.Errorf("defaults not kept: %+v", theme)
	}
	if theme.Selection != nil {
		t.Errorf("selection should stay unset, got %v", theme.Selection)
	}
	if theme.HasMode || !theme.Dark {
		t.Errorf("mode should be the dark default without HasMode, got dark=%v hasMode=%v", theme.Dark, theme.HasMode)
	}
}

func TestOverlayThemeColorsToml(t *testing.T) {
	theme := overlayTheme(defaultTheme(), parseThemeFile(tokyoNightColors), false)
	if theme.Accent != lipgloss.Color("#7aa2f7") || theme.Selection != lipgloss.Color("#292e42") || theme.Muted != lipgloss.Color("#414868") {
		t.Errorf("accent/selection/muted wrong: %+v", theme)
	}
	if theme.Error != lipgloss.Color("#f7768e") {
		t.Errorf("error should map from red, got %v", theme.Error)
	}
	if theme.Bright != lipgloss.Color("#c0caf5") {
		t.Errorf("bright should map from bright_foreground, got %v", theme.Bright)
	}
	if !theme.Dark || !theme.HasMode {
		t.Errorf("mode = dark should set Dark and HasMode: %+v", theme)
	}
}

func TestOverlayThemeHeyTomlKeysWin(t *testing.T) {
	theme := overlayTheme(defaultTheme(), map[string]string{"error": "#111111", "red": "#222222", "mode": "light"}, false)
	if theme.Error != lipgloss.Color("#111111") {
		t.Errorf("error should beat red, got %v", theme.Error)
	}
	if theme.Dark || !theme.HasMode {
		t.Errorf("mode = light should clear Dark: %+v", theme)
	}
}

func TestResolveThemeNoColor(t *testing.T) {
	home := omarchyHome(t, map[string]string{"colors.toml": tokyoNightColors})
	theme := resolveTheme(envOf(map[string]string{"NO_COLOR": "1"}), home)
	if theme.Source != "NO_COLOR" {
		t.Fatalf("NO_COLOR should win over omarchy, got source %q", theme.Source)
	}
	if _, ok := theme.Accent.(lipgloss.NoColor); !ok {
		t.Errorf("accent should be NoColor, got %v", theme.Accent)
	}
}

func TestResolveThemeOrder(t *testing.T) {
	home := omarchyHome(t, map[string]string{
		"colors.toml": tokyoNightColors,
		"hey.toml":    "accent = \"#ff0000\"\n",
	})
	custom := filepath.Join(t.TempDir(), "custom.toml")
	if err := os.WriteFile(custom, []byte("accent = \"#00ff00\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	theme := resolveTheme(envOf(map[string]string{"HEY_THEME": custom}), home)
	if theme.Accent != lipgloss.Color("#00ff00") || theme.Source != custom {
		t.Errorf("HEY_THEME should win: %+v", theme)
	}

	theme = resolveTheme(envOf(map[string]string{"HEY_THEME": "/nonexistent/theme.toml"}), home)
	if theme.Accent != lipgloss.Color("#ff0000") {
		t.Errorf("missing HEY_THEME should fall through to hey.toml: %+v", theme)
	}
	if theme.Muted != lipgloss.BrightBlack {
		t.Errorf("hey.toml alone should not pull keys from colors.toml: %+v", theme)
	}

	if err := os.Remove(filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "hey.toml")); err != nil {
		t.Fatal(err)
	}
	theme = resolveTheme(envOf(nil), home)
	if theme.Accent != lipgloss.Color("#7aa2f7") {
		t.Errorf("without hey.toml colors.toml should be read: %+v", theme)
	}
}

func TestResolveThemeWithoutOmarchy(t *testing.T) {
	theme := resolveTheme(envOf(nil), t.TempDir())
	if theme != defaultTheme() {
		t.Errorf("non-omarchy machine should get the ANSI defaults, got %+v", theme)
	}
	if omarchyWatchDir(t.TempDir()) != "" {
		t.Error("nothing to watch without an omarchy state dir")
	}
	if watchThemeCmd("") != nil {
		t.Error("watch command should be nil when there is no directory")
	}
}

func TestOmarchyWatchDirIsThemeParent(t *testing.T) {
	home := omarchyHome(t, nil)
	want := filepath.Join(home, ".local", "state", "omarchy", "current")
	if got := omarchyWatchDir(home); got != want {
		t.Errorf("watch dir = %q, want %q", got, want)
	}
}

func TestWatchThemeCmdSeesWritesInsideThemeDir(t *testing.T) {
	// omarchy-theme-refresh re-renders files in place instead of swapping the
	// directory, so a write inside theme/ has to wake the watcher too.
	home := omarchyHome(t, map[string]string{"colors.toml": tokyoNightColors})
	done := make(chan tea.Msg, 1)
	go func() { done <- watchThemeCmd(omarchyWatchDir(home))() }()

	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	hey := filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "hey.toml")
	for {
		select {
		case msg := <-done:
			if _, ok := msg.(themeChangedMsg); !ok {
				t.Fatalf("want themeChangedMsg, got %T", msg)
			}
			return
		case <-deadline:
			t.Fatal("write inside theme/ never woke the watcher")
		case <-tick.C:
			// Re-write until the watcher observes it: arming races the first write.
			if err := os.WriteFile(hey, []byte("accent = \"#ff0000\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestApplyThemeLightModeFlipsBright(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	light := defaultTheme()
	light.Dark = false
	applyTheme(light)
	if colorBright != lipgloss.Black {
		t.Errorf("light ANSI theme should use black for emphasis, got %v", colorBright)
	}

	themed := overlayTheme(defaultTheme(), map[string]string{"foreground": "#333333", "mode": "light"}, false)
	applyTheme(themed)
	if colorBright != lipgloss.Color("#333333") {
		t.Errorf("a themed foreground should be kept in light mode, got %v", colorBright)
	}
	if selectionStyle(lipgloss.NewStyle()).GetBackground() != lipgloss.NewStyle().GetBackground() {
		t.Error("no selection color should leave the style untouched")
	}
}

func TestUsableAccentFallsBackToBrightBlue(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"hackerman keeps a vivid accent", map[string]string{
			"accent": "#82FB9C", "background": "#0B0C16", "bright_foreground": "#f2f2f2", "bright_blue": "#c4d2ed"}, "#82FB9C"},
		{"kanagawa's accent is its foreground", map[string]string{
			"accent": "#dcd7ba", "background": "#1f1f28", "bright_foreground": "#dcd7ba", "bright_blue": "#7fb4ca"}, ""},
		{"osaka-jade's accent is dimmer than its blue", map[string]string{
			"accent": "#509475", "background": "#111c18", "bright_foreground": "#e1f0ea", "bright_blue": "#ACD4CF"}, ""},
		{"lupine's accent beats its even dimmer blue", map[string]string{
			"accent": "#3264eb", "background": "#fafafa", "bright_foreground": "#1a1a1a", "bright_blue": "#5482ff"}, "#3264eb"},
		{"a bare hey.toml accent is trusted", map[string]string{"accent": "#ff00ff"}, "#ff00ff"},
	}
	for _, tc := range cases {
		theme := overlayTheme(defaultTheme(), tc.values, false)
		want := color.Color(lipgloss.BrightBlue) // "" = fall back to the terminal's own bright blue
		if tc.want != "" {
			want = lipgloss.Color(tc.want)
		}
		if theme.Accent != want {
			t.Errorf("%s: accent = %v, want %v", tc.name, theme.Accent, want)
		}
	}

	noBlue := overlayTheme(defaultTheme(), map[string]string{"accent": "#dcd7ba", "bright_foreground": "#dcd7ba"}, false)
	if noBlue.Accent != lipgloss.BrightBlue {
		t.Errorf("without a theme blue the fallback is ANSI bright blue, got %v", noBlue.Accent)
	}
}

func TestCursorStylesCarryTheSelectionBackground(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	applyTheme(overlayTheme(defaultTheme(), map[string]string{"accent": "#82FB9C", "selection": "#1f253a"}, false))

	marker, text := cursorStyles()
	gap := selectionStyle(lipgloss.NewStyle())
	want := lipgloss.Color("#1f253a")
	if marker.GetBackground() != want || text.GetBackground() != want || gap.GetBackground() != want {
		t.Error("every cursor-row segment a section renders through the helpers must carry the selection background")
	}
	if renderScreenerRows(&screenerPane{rows: []screenerRow{{detail: "maria@example.com"}}}, 5, 40) == "" {
		t.Error("screener rows should render")
	}
}

func TestPillTextReadsOnTheAccent(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	applyTheme(defaultTheme())
	if colorOnAccent != color.Color(lipgloss.Black) {
		t.Errorf("the ANSI accent keeps the classic black pill text, got %v", colorOnAccent)
	}

	// lupine's dark blue carries black at ~3.7:1; white reads better.
	lupine := map[string]string{"accent": "#3264eb", "background": "#fafafa",
		"bright_foreground": "#1a1a1a", "bright_blue": "#5482ff"}
	applyTheme(overlayTheme(defaultTheme(), lupine, false))
	if colorOnAccent != color.Color(lipgloss.BrightWhite) {
		t.Errorf("a dark themed accent needs bright white pill text, got %v", colorOnAccent)
	}

	// hackerman's bright green still reads best with black on it.
	applyTheme(overlayTheme(defaultTheme(), map[string]string{"accent": "#82FB9C"}, false))
	if colorOnAccent != color.Color(lipgloss.Black) {
		t.Errorf("a bright themed accent keeps black pill text, got %v", colorOnAccent)
	}

	applyTheme(noColorTheme())
	if _, ok := colorOnAccent.(lipgloss.NoColor); !ok {
		t.Errorf("NO_COLOR must keep the pill colorless, got %v", colorOnAccent)
	}
}

func TestTrustedThemeBypassesTheGates(t *testing.T) {
	// kanagawa's foreground-as-accent would be gated for a machine palette, but a
	// hand-chosen HEY_THEME file means it exactly as written.
	values := map[string]string{"accent": "#dcd7ba", "background": "#1f1f28", "bright_foreground": "#dcd7ba"}
	if theme := overlayTheme(defaultTheme(), values, true); theme.Accent != lipgloss.Color("#dcd7ba") {
		t.Errorf("a trusted accent must be used as written, got %v", theme.Accent)
	}

	t.Cleanup(func() { applyTheme(defaultTheme()) })
	// rose-pine's 2.5:1 accent-on-selection pair is dropped for machine palettes.
	trusted := overlayTheme(defaultTheme(), map[string]string{"accent": "#56949f", "selection": "#dfdad9"}, true)
	trusted.Trusted = true
	applyTheme(trusted)
	if colorSelection == nil {
		t.Error("a trusted selection must be kept even below the contrast gate")
	}

	home := omarchyHome(t, nil)
	custom := filepath.Join(home, "mine.toml")
	if err := os.WriteFile(custom, []byte(`accent = "#dcd7ba"`+"\n"+`bright_foreground = "#dcd7ba"`), 0o644); err != nil {
		t.Fatal(err)
	}
	theme := resolveTheme(envOf(map[string]string{"HEY_THEME": custom}), home)
	if !theme.Trusted || theme.Accent != lipgloss.Color("#dcd7ba") {
		t.Errorf("HEY_THEME must resolve as trusted with the accent as written, got %+v", theme)
	}
}

func TestSelectionBackgroundNeedsContrastWithAccent(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	// hackerman: green accent on a near-black selection, 11.7:1
	applyTheme(overlayTheme(defaultTheme(), map[string]string{"accent": "#82FB9C", "selection": "#1f253a"}, false))
	if colorSelection == nil {
		t.Error("a high-contrast selection should be kept")
	}

	// rose-pine: teal accent on a warm light selection, 2.5:1 — would be mud
	applyTheme(overlayTheme(defaultTheme(), map[string]string{"accent": "#56949f", "selection": "#dfdad9"}, false))
	if colorSelection != nil {
		t.Error("a selection the accent cannot be read on should be dropped")
	}
	marker, text := cursorStyles()
	if marker.GetForeground() != lipgloss.Color("#56949f") || text.GetForeground() != lipgloss.Color("#56949f") {
		t.Error("the cursor row must stay accent-colored without a selection background")
	}

	if ratio := contrastRatio(lipgloss.Color("#000000"), lipgloss.Color("#ffffff")); ratio < 20.9 || ratio > 21.1 {
		t.Errorf("black/white contrast = %.2f, want 21", ratio)
	}
}

func TestRestyleRefreshesEveryCopy(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	m := modelWithBoxes()

	theme := overlayTheme(defaultTheme(), map[string]string{"accent": "#ff8800", "selection": "#222233"}, false)
	m.restyle(theme)

	want := lipgloss.Color("#ff8800")
	if m.styles.title.GetForeground() != want {
		t.Errorf("model styles not rebuilt: %v", m.styles.title.GetForeground())
	}
	if m.vc.styles.title.GetForeground() != want {
		t.Errorf("view context styles not rebuilt: %v", m.vc.styles.title.GetForeground())
	}
	if m.help.styles.title.GetForeground() != want {
		t.Errorf("help bar styles not rebuilt: %v", m.help.styles.title.GetForeground())
	}
	if selectionStyle(lipgloss.NewStyle()).GetBackground() != lipgloss.Color("#222233") {
		t.Error("selection background not applied")
	}
	if view := m.mailView.View(); view == "" {
		t.Error("mail view should still render after a restyle")
	}
}

func TestBackgroundColorMsgOnlyWhenThemeHasNoMode(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	m := sizedModel()
	m.theme = defaultTheme()

	updated, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	m = updated.(model)
	if m.theme.Dark {
		t.Error("a light terminal background should flip Dark when the theme has no mode")
	}

	m.theme.HasMode, m.theme.Dark = true, true
	updated, _ = m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	m = updated.(model)
	if !m.theme.Dark {
		t.Error("a theme that states its mode must not be overridden by the terminal")
	}
}

func TestThemeChangedMsgKeepsPaletteMidSwap(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	home := omarchyHome(t, nil)
	t.Setenv("HOME", home)
	if err := os.RemoveAll(filepath.Join(home, ".local", "state", "omarchy", "current", "theme")); err != nil {
		t.Fatal(err)
	}

	m := sizedModel()
	before := overlayTheme(defaultTheme(), map[string]string{"accent": "#ff8800"}, false)
	m.restyle(before)

	updated, cmd := m.Update(themeChangedMsg{})
	m = updated.(model)
	if m.theme.Accent != lipgloss.Color("#ff8800") {
		t.Errorf("theme dir missing mid-swap should keep the palette, got %v", m.theme.Accent)
	}
	if cmd == nil {
		t.Error("watcher should be re-armed")
	}
}

func TestThemeChangedMsgReloadsTheme(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	home := omarchyHome(t, map[string]string{"colors.toml": tokyoNightColors})
	t.Setenv("HOME", home)

	m := sizedModel()
	m.restyle(defaultTheme())
	updated, _ := m.Update(themeChangedMsg{})
	m = updated.(model)
	if m.theme.Accent != lipgloss.Color("#7aa2f7") {
		t.Errorf("theme change should re-read colors.toml, got %v", m.theme.Accent)
	}
}
