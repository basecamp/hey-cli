package tui

import (
	"image/color"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

var allCoverPresets = []coverPreset{coverBlobs, coverGrid, coverPeace, coverTerrazzo, coverTopo, coverWaves}

func TestParseCoverPreset(t *testing.T) {
	for _, preset := range allCoverPresets {
		if got := parseCoverPreset(string(preset)); got != preset {
			t.Errorf("parseCoverPreset(%q) = %q, want %q", preset, got, preset)
		}
	}

	if got := parseCoverPreset("  TOPO "); got != coverTopo {
		t.Errorf("parseCoverPreset with padding and case = %q, want %q", got, coverTopo)
	}
	for _, name := range []string{"", "calendar", "sunset"} {
		if got := parseCoverPreset(name); got != coverNone {
			t.Errorf("parseCoverPreset(%q) = %q, want no cover", name, got)
		}
	}
}

// A cover has to be exactly the block it was asked for: one row over and the list
// scrolls, one column over and every row wraps.
func TestCoverFillsItsBlockExactly(t *testing.T) {
	const width, height = 60, 9

	for _, preset := range allCoverPresets {
		renderer := &coverRenderer{}
		lines := strings.Split(renderer.view(preset, width, height), "\n")
		if len(lines) != height {
			t.Errorf("%s: %d rows, want %d", preset, len(lines), height)
			continue
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w != width {
				t.Errorf("%s: row %d is %d cells wide, want %d", preset, i, w, width)
			}
		}
	}
}

func TestCoverIsDeterministic(t *testing.T) {
	for _, preset := range allCoverPresets {
		first := (&coverRenderer{}).view(preset, 40, 8)
		second := (&coverRenderer{}).view(preset, 40, 8)
		if first != second {
			t.Errorf("%s renders differently on a second pass", preset)
		}
	}
}

func TestCoverNoneRendersNothing(t *testing.T) {
	if got := (&coverRenderer{}).view(coverNone, 40, 8); got != "" {
		t.Errorf("coverNone rendered %q, want empty", got)
	}
}

// The pattern lives in the glyphs, not the colors, so that a colorless terminal
// still gets art. Assert the grid's shape rather than its escape sequences.
func TestCoverGridPattern(t *testing.T) {
	canvas := newCoverCanvas(13, 6, coverPalette{})
	canvas.paintGrid()

	want := strings.Join([]string{
		"┼──┼──┃──┼──┼",
		"│  │  ┃  │  │",
		"━━━━━━╋━━━━━━",
		"│  │  ┃  │  │",
		"┼──┼──┃──┼──┼",
		"│  │  ┃  │  │",
	}, "\n")

	if got := canvas.plain(); got != want {
		t.Errorf("grid pattern:\n%s\nwant:\n%s", got, want)
	}
}

// Two presets draw curves, which characters cannot hold: contours and a circle
// both need braille's 2×4 dots. Box-drawing was tried for each and read as noise
// and as a broken television respectively.
func TestBraillePaintersDrawOnlyBraille(t *testing.T) {
	for preset, paint := range map[coverPreset]func(*coverCanvas){
		coverTopo:  (*coverCanvas).paintTopo,
		coverPeace: (*coverCanvas).paintPeace,
	} {
		canvas := newCoverCanvas(60, 16, coverPalette{})
		paint(canvas)

		drawn := 0
		for _, glyph := range canvas.plain() {
			switch {
			case glyph == ' ' || glyph == '\n':
			case glyph > brailleBlank && glyph <= brailleBlank+0xff:
				drawn++
			default:
				t.Fatalf("%s drew %q, which is not a braille cell", preset, glyph)
			}
		}
		if drawn == 0 {
			t.Errorf("%s drew nothing", preset)
		}
	}
}

// Clipped, the fingers run off the top and the mark stops being a hand, so it
// shrinks to fit a short cover instead.
func TestPeaceHandFitsEveryCover(t *testing.T) {
	for _, rows := range []int{coverMinRows, 8, 12, 24, 55, 90} {
		dotRows := rows * brailleRows
		height := peaceHandHeight(dotRows)

		if height > dotRows {
			t.Errorf("%d rows: a hand %d dots tall does not fit in %d", rows, height, dotRows)
		}
		if height < 14 {
			t.Errorf("%d rows: a hand %d dots tall is too small to read", rows, height)
		}
	}
}

// A ribbon has to stay a ribbon at every shape of cover. Wavelengths fixed in
// columns drew chevrons on a tall cover, because only the amplitude scaled with
// the height; a wavelength taken from the height alone then drew a busy repeat on
// a wide short one. Both properties are pinned here.
func TestBlobRibbonShapeHoldsAtAnyAspect(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{92, 12}, {190, 55}, {60, 30}, {240, 20}, {40, 6}, {300, 90},
	} {
		amplitude, wavelength := blobRibbonShape(size.width, size.height)

		if slope := amplitude / wavelength; slope > maxBlobSlope {
			t.Errorf("%dx%d: ribbons climb %.2f rows per column, want at most %.2f",
				size.width, size.height, slope, maxBlobSlope)
		}
		if sweeps := float64(size.width) / wavelength; sweeps > 4.01 {
			t.Errorf("%dx%d: %.1f sweeps across, want at most 4 so it reads as ribbons",
				size.width, size.height, sweeps)
		}
		if amplitude < 1 {
			t.Errorf("%dx%d: amplitude %.2f leaves the ribbons flat", size.width, size.height, amplitude)
		}
	}
}

// Ribbons cover some of the field, not all of it and not none: HEY's blobs are
// yellow on mint, and both colors have to be in the picture.
func TestBlobsLeaveTheFieldShowing(t *testing.T) {
	for _, size := range []struct{ width, height int }{{92, 12}, {190, 55}, {240, 20}} {
		canvas := newCoverCanvas(size.width, size.height, coverPalette{})
		canvas.paintBlobs()

		inked := strings.Count(canvas.plain(), "█")
		share := float64(inked) / float64(size.width*size.height)
		if share < 0.15 || share > 0.6 {
			t.Errorf("%dx%d: ribbons cover %.0f%% of the cover, want between 15%% and 60%%",
				size.width, size.height, share*100)
		}
	}
}

// Covers are painted in the ANSI-16 slots so they follow the terminal's theme.
// A hex value here would look right on the one theme it was picked against and
// wrong on every other, and would not follow a theme switch.
func TestCoverPalettesUseThemeColors(t *testing.T) {
	ansiSlots := map[color.Color]bool{}
	for slot := lipgloss.Black; slot <= lipgloss.BrightWhite; slot++ {
		ansiSlots[slot] = true
	}
	if len(ansiSlots) != 16 {
		t.Fatalf("collected %d ANSI slots, want 16 — the check below passes on anything", len(ansiSlots))
	}
	if ansiSlots[lipgloss.Color("#facc15")] {
		t.Fatal("a hex color counts as a theme color, so the check below is no check at all")
	}

	for _, preset := range allCoverPresets {
		palette := coverPalettes[preset]
		for _, c := range append(palette.ink[:], palette.field) {
			if c != nil && !ansiSlots[c] {
				t.Errorf("%s uses %v, which is not one of the sixteen theme colors", preset, c)
			}
		}
	}
}

// A cover has one palette, not a light one and a dark one, because every slot it
// uses does the same job in either kind of theme. Switching the mode must not
// change what is drawn — if it does, some slot is being picked for its brightness,
// which is the mistake that paints a light theme's field with its text color.
func TestCoversDoNotDependOnTheThemeMode(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	for _, preset := range allCoverPresets {
		dark := defaultTheme()
		dark.Dark = true
		applyTheme(dark)
		inDark := (&coverRenderer{}).view(preset, 40, 8)

		light := defaultTheme()
		light.Dark = false
		applyTheme(light)
		if inLight := (&coverRenderer{}).view(preset, 40, 8); inLight != inDark {
			t.Errorf("%s renders differently on a light theme", preset)
		}
	}
}

func TestCoverColorlessKeepsTheGlyphs(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	applyTheme(noColorTheme())

	for _, preset := range allCoverPresets {
		rendered := (&coverRenderer{}).view(preset, 40, 8)
		if strings.Contains(rendered, "\x1b") {
			t.Errorf("%s emitted an escape sequence with color disabled", preset)
		}
		if strings.TrimSpace(rendered) == "" {
			t.Errorf("%s rendered nothing but blanks with color disabled", preset)
		}
	}
}

// The renderer keeps the last cover it drew, so its memo has to notice color being
// turned off — otherwise a NO_COLOR terminal keeps painting until it resizes. A
// retint needs no repaint: the ANSI slots do not change, the terminal's idea of
// them does.
func TestCoverRepaintsWhenColorGoesAway(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	renderer := &coverRenderer{}
	colored := renderer.view(coverTerrazzo, 40, 8)

	applyTheme(noColorTheme())
	if renderer.view(coverTerrazzo, 40, 8) == colored {
		t.Error("cover kept its colors after color was turned off")
	}
}

// --- Picking a cover ---

func TestCoverPickerOffersEveryPresetAndNone(t *testing.T) {
	picker := newCoverPicker(coverNone)

	if len(picker.choices) != len(allCoverPresets)+1 {
		t.Fatalf("picker offers %d choices, want every preset plus none", len(picker.choices))
	}
	if picker.choices[0] != coverNone {
		t.Errorf("first choice is %q, want no cover", picker.choices[0])
	}
	for _, preset := range allCoverPresets {
		if !slices.Contains(picker.choices, preset) {
			t.Errorf("picker does not offer %s", preset)
		}
	}
}

// The picker opens on what is already covering the Imbox, so enter is a no-op
// rather than a surprise.
func TestCoverPickerStartsOnTheCurrentCover(t *testing.T) {
	for _, preset := range append([]coverPreset{coverNone}, allCoverPresets...) {
		if got := newCoverPicker(preset).selected(); got != preset {
			t.Errorf("picker opened on %q, want %q", got, preset)
		}
	}
}

func TestCoverPickerMovesAndStops(t *testing.T) {
	picker := newCoverPicker(coverNone)
	last := len(picker.choices) - 1

	for range len(picker.choices) + 3 {
		picker.update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if picker.cursor != last {
		t.Errorf("cursor ran to %d, want to stop at %d", picker.cursor, last)
	}

	for range len(picker.choices) + 3 {
		picker.update(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if picker.cursor != 0 {
		t.Errorf("cursor ran to %d, want to stop at 0", picker.cursor)
	}
}

// A cover is chosen by looking at it, so the picker draws the highlighted one.
func TestCoverPickerPreviewsTheHighlightedCover(t *testing.T) {
	picker := newCoverPicker(coverTopo)
	view := picker.view(newStyles(), 60)

	if !strings.Contains(view, "Topo") {
		t.Error("picker does not name the highlighted cover")
	}
	preview := (&coverRenderer{}).view(coverTopo, 56, coverPreviewRows)
	if !strings.Contains(view, preview) {
		t.Error("picker does not draw the highlighted cover")
	}

	if strings.Contains(newCoverPicker(coverNone).view(newStyles(), 60), preview) {
		t.Error("picker drew art for no cover")
	}
}

// ctrl+v picks the cover, and the choice outlives the box it was made in: there
// is nowhere to read a cover back from, so the session is what remembers it.
func TestCoverPickerAppliesToTheImbox(t *testing.T) {
	v := mailWithPostings()

	if cmd := v.HandleContentKey(keyPress("ctrl+v")); cmd != nil {
		t.Fatal("opening the cover picker should not start a request")
	}
	if v.coverPicker == nil || !v.CapturingInput() {
		t.Fatal("ctrl+v did not open a picker that captures input")
	}

	for v.coverPicker.selected() != coverTopo {
		v.HandleContentKey(keyPress("down"))
	}
	v.HandleContentKey(keyPress("enter"))

	if v.coverPicker != nil {
		t.Error("enter left the picker open")
	}
	if v.cover != coverTopo || v.postingList.cover != coverTopo {
		t.Errorf("cover = %q, list = %q, want topo", v.cover, v.postingList.cover)
	}

	// A re-read of the box keeps it, and so does coming back from another box.
	v.Update(currentPostingsLoaded(v, testPostings()))
	if v.postingList.cover != coverTopo {
		t.Errorf("reading the Imbox again dropped the cover: %q", v.postingList.cover)
	}
}

// Only the Imbox is coverable, which is haystack's rule. Elsewhere the key says so
// rather than doing nothing.
func TestCoverPickerRefusesOtherBoxes(t *testing.T) {
	v := mailWithPostings()
	v.boxIndex = 1
	v.Update(currentPostingsLoaded(v, testPostings()))

	v.HandleContentKey(keyPress("ctrl+v"))
	if v.coverPicker != nil {
		t.Error("picker opened on a box that cannot be covered")
	}
	if v.notice == "" {
		t.Error("picker refused silently")
	}
	if hasHelpBinding(v.HelpBindings(), "ctrl+v") {
		t.Error("help offers cover art on a box that cannot be covered")
	}
}

// The chorded keys sit at the end of the help bar, together, keeping their order.
// Scattered among the single keys they push the everyday ones onto a second line.
func TestModifiersLast(t *testing.T) {
	got := modifiersLast([]helpBinding{
		{"/", "search"},
		{"ctrl+s", "screener"},
		{"c", "compose"},
		{"ctrl+r", "reload"},
		{"u", "undo"},
	})

	want := []helpBinding{
		{"/", "search"},
		{"c", "compose"},
		{"u", "undo"},
		{"ctrl+s", "screener"},
		{"ctrl+r", "reload"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("bindings = %v, want %v", got, want)
	}
}

// --- The cover as a lid ---

func coveredList(cover coverPreset, height int, seen ...bool) *contentList {
	postings := make([]models.Posting, len(seen))
	for i, isSeen := range seen {
		postings[i] = models.Posting{
			ID:      int64(i + 1),
			Name:    coveredThreadName(isSeen, i),
			Seen:    isSeen,
			Creator: models.Contact{Name: "Jason Fried"},
		}
	}
	list := &contentList{width: 60, height: height, cover: cover}
	list.setPostings(postings)
	return list
}

func coveredThreadName(seen bool, index int) string {
	if seen {
		return "Read thread " + string(rune('A'+index))
	}
	return "Unread thread " + string(rune('A'+index))
}

// The point of a cover is that what is under it is not on screen. The divider
// stays, so the reader knows there is something there and how to get at it.
func TestCoverHidesPreviouslySeen(t *testing.T) {
	list := coveredList(coverTopo, 24, false, true, true, true)

	view := list.view()
	if !strings.Contains(view, sectionPreviouslySeen.label()) {
		t.Error("covered list dropped the Previously Seen divider")
	}
	if !strings.Contains(view, "3 hidden · v to peek") {
		t.Error("covered list gave no hint about what is under the cover")
	}
	for _, posting := range list.postings[1:] {
		if strings.Contains(view, posting.Name) {
			t.Errorf("%q is visible through the cover", posting.Name)
		}
	}
	if !strings.Contains(view, list.postings[0].Name) {
		t.Error("the cover hid an unseen thread")
	}
}

// Hidden threads are out of reach too — a cursor under the cover would be a
// cursor nobody can see, and a bulk action nobody meant.
func TestCoveredThreadsAreOutOfReach(t *testing.T) {
	list := coveredList(coverTopo, 24, false, false, true, true)

	if got := list.itemCount(); got != 2 {
		t.Errorf("itemCount = %d, want 2", got)
	}
	for range 5 {
		list.moveDown()
	}
	if list.cursor != 1 {
		t.Errorf("cursor reached %d, want to stop at 1", list.cursor)
	}
	if posting := list.selectedPosting(); posting == nil || posting.Seen {
		t.Error("the cursor got under the cover")
	}
}

func TestPeekingLiftsTheCover(t *testing.T) {
	list := coveredList(coverTopo, 24, false, true, true)
	list.toggleCoverPeek()

	view := list.view()
	if got := list.itemCount(); got != 3 {
		t.Errorf("itemCount after peeking = %d, want 3", got)
	}
	if !strings.Contains(view, list.postings[2].Name) {
		t.Error("peeking did not reveal the seen threads")
	}
	if !strings.Contains(view, "v to cover") {
		t.Error("a peeked list does not say how to put the cover back")
	}

	list.toggleCoverPeek()
	if got := list.itemCount(); got != 1 {
		t.Errorf("itemCount after covering again = %d, want 1", got)
	}
}

// The art gets every row the threads above it did not use, so the cover reaches
// the bottom of the screen instead of floating as a band.
func TestCoverFillsToTheBottom(t *testing.T) {
	for _, height := range []int{18, 24, 40} {
		list := coveredList(coverWaves, height, false, false, true, true, true)
		rows := strings.Count(list.view(), "\n") + 1
		if rows != height {
			t.Errorf("height %d: covered list rendered %d rows", height, rows)
		}
	}
}

// A list with no room for art keeps the divider: the hint matters more than the
// picture, and half a picture is worse than none.
func TestCoverDropsTheArtBeforeTheDivider(t *testing.T) {
	list := coveredList(coverTopo, coverMinRows+2, false, false, true)

	view := list.view()
	if !strings.Contains(view, "1 hidden · v to peek") {
		t.Error("a short covered list lost its divider")
	}
	if rows := strings.Count(view, "\n") + 1; rows > coverMinRows+2 {
		t.Errorf("short covered list rendered %d rows, over its height", rows)
	}
}

// With everything read, the Imbox is the cover and nothing else.
func TestCoverWithNothingUnread(t *testing.T) {
	list := coveredList(coverPeace, 20, true, true)

	if got := list.itemCount(); got != 0 {
		t.Errorf("itemCount = %d, want 0", got)
	}
	view := list.view()
	if !strings.Contains(view, "2 hidden · v to peek") {
		t.Error("an all-read Imbox does not say what is under the cover")
	}
	if rows := strings.Count(view, "\n") + 1; rows != 20 {
		t.Errorf("all-read covered list rendered %d rows, want 20", rows)
	}
}

func TestUncoveredAndFlatListsShowEverything(t *testing.T) {
	uncovered := coveredList(coverNone, 24, false, true, true)
	if got := uncovered.itemCount(); got != 3 {
		t.Errorf("uncovered list reaches %d postings, want 3", got)
	}

	flat := coveredList(coverTopo, 24, false, true, true)
	flat.hideSeenState = true
	if got := flat.itemCount(); got != 3 {
		t.Errorf("list without seen sections reaches %d postings, want 3", got)
	}
}

// Reading a thread puts it under the cover, which must not leave the cursor or a
// selection stranded there.
func TestMarkingSeenSlidesAThreadUnderTheCover(t *testing.T) {
	list := coveredList(coverTopo, 24, false, false, true)
	list.cursor = 1
	list.toggleSelected()

	list.markSeen(1)

	if got := list.itemCount(); got != 1 {
		t.Errorf("itemCount = %d, want 1", got)
	}
	if list.cursor >= list.itemCount() {
		t.Errorf("cursor %d is under the cover", list.cursor)
	}
	if ids := list.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection %v survived under the cover", ids)
	}
}

// The Imbox stacks three sections and only the last one is covered.
func TestBubbledUpAndNewStayVisible(t *testing.T) {
	list := &contentList{width: 60, height: 24, cover: coverTopo}
	list.setPostings([]models.Posting{
		{ID: 1, Name: "Bubbled thread", BubbledUp: true},
		{ID: 2, Name: "Unread thread"},
		{ID: 3, Name: "Read thread", Seen: true},
	})

	view := list.view()
	for _, name := range []string{"Bubbled thread", "Unread thread"} {
		if !strings.Contains(view, name) {
			t.Errorf("%q was covered", name)
		}
	}
	if strings.Contains(view, "Read thread") {
		t.Error("the seen thread is visible through the cover")
	}
}
