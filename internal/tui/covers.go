package tui

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Cover art for the Imbox, drawn where the HEY web app draws it: under the
// Previously Seen divider, so the seen threads sit behind a piece of art rather
// than trailing off the bottom of the list.
//
// The web app's covers are SVG assets, and a terminal cannot draw an SVG. These
// are the same six patterns re-drawn as characters, which is not a fallback but
// the better medium for two of them: a blueprint grid is what box-drawing
// characters are for, and a contour map is a band boundary in a noise field.
//
// The art is painted in the ANSI-16 slots, so a cover wears the terminal's own
// theme rather than HEY's hex. Colors are laid over glyphs that carry the pattern
// on their own, so a colorless terminal still gets the art rather than a blank
// band.
type coverPreset string

const (
	coverNone     coverPreset = ""
	coverBlobs    coverPreset = "blobs"
	coverGrid     coverPreset = "grid"
	coverPeace    coverPreset = "peace"
	coverTerrazzo coverPreset = "terrazzo"
	coverTopo     coverPreset = "topo"
	coverWaves    coverPreset = "waves"
)

// coverMinRows is the point below which a cover is a stripe rather than a
// picture. A list with less room than this to spare keeps the divider and skips
// the art, rather than drawing a smear nobody can read.
const coverMinRows = 5

// The only thing about the active theme a cover cares about, set by applyTheme:
// whether to paint at all. NO_COLOR leaves the glyphs, which is the pattern.
// Nothing here needs to know whether the theme is light or dark — see
// coverPalettes for why not.
var coverColorless = false

func parseCoverPreset(name string) coverPreset {
	switch coverPreset(strings.ToLower(strings.TrimSpace(name))) {
	case coverBlobs:
		return coverBlobs
	case coverGrid:
		return coverGrid
	case coverPeace:
		return coverPeace
	case coverTerrazzo:
		return coverTerrazzo
	case coverTopo:
		return coverTopo
	case coverWaves:
		return coverWaves
	default:
		return coverNone
	}
}

// coverRenderer draws a cover and remembers the last one it drew. The posting
// list re-renders on every keystroke and a cover is thousands of styled cells,
// so it is painted when its preset, size or palette changes and not per frame.
type coverRenderer struct {
	preset   coverPreset
	width    int
	height   int
	palette  coverPalette
	rendered string
}

func (r *coverRenderer) view(preset coverPreset, width, height int) string {
	palette := coverPalettes[preset].current()
	if r.rendered != "" && r.preset == preset && r.width == width && r.height == height && r.palette == palette {
		return r.rendered
	}

	canvas := newCoverCanvas(width, height, palette)
	switch preset {
	case coverBlobs:
		canvas.paintBlobs()
	case coverGrid:
		canvas.paintGrid()
	case coverPeace:
		canvas.paintPeace()
	case coverTerrazzo:
		canvas.paintTerrazzo()
	case coverTopo:
		canvas.paintTopo()
	case coverWaves:
		canvas.paintWaves()
	case coverNone:
		return ""
	}

	r.preset, r.width, r.height, r.palette = preset, width, height, palette
	r.rendered = canvas.view()
	return r.rendered
}

// --- Palettes ---

// coverPalette is a cover's field and the inks drawn on it. Each preset names its
// own inks in the order its painter uses them.
type coverPalette struct {
	field color.Color
	ink   [4]color.Color
}

func (p coverPalette) current() coverPalette {
	if coverColorless {
		return coverPalette{}
	}
	return p
}

// Covers are painted in the ANSI-16 slots, like the rest of the TUI and for the
// same reason (see styles.go): a desktop theme defines those sixteen colors and
// retints running terminals over OSC 4, so the art follows the theme with no work
// here and restyles live when the theme changes. HEY's own covers are yellow and
// mint and violet; these name the slots those stand in for, and what arrives is
// the reader's palette.
//
// There is deliberately one palette per preset rather than a light one and a dark
// one, which is what the web app needs. The slots a cover uses are chosen for the
// job each one does in a theme, not for how bright it happens to be:
//
//   - Black is the background and White the foreground — in every theme, light or
//     dark. A cover whose field is Black is a cover on the reader's own paper,
//     and its foreground ink is legible there without anyone deciding which way
//     round the theme is. That is what makes HEY's white terrazzo chips turn black
//     in light mode: they are not white, they are the text color.
//   - A hue — yellow, cyan, magenta, blue — is a mid-tone everywhere, so a hue on
//     a hue keeps its contrast either way.
//
// Reaching for a slot because it looks light or dark is what goes wrong. Painting
// a light-mode field BrightWhite gets a *dark* field, because on a light theme the
// bright foreground is dark: exactly inverted, and only on the themes the cover
// was supposed to be fixing.
var coverPalettes = map[coverPreset]coverPalette{
	coverBlobs: {field: lipgloss.Magenta, ink: [4]color.Color{
		lipgloss.BrightYellow, lipgloss.Red, lipgloss.BrightMagenta, lipgloss.Blue}},
	coverGrid: {field: lipgloss.Black, ink: [4]color.Color{lipgloss.Blue, lipgloss.BrightBlue}},

	// HEY draws the hand white on a yellow card. Yellow is the lightest hue a theme
	// has, so a whole cover of it glares in every one of them, and there is no
	// darker yellow to reach for — a palette has one. The yellow moves to the hand
	// instead: the cover still reads yellow, but as line work on the reader's own
	// paper rather than as a lit panel.
	coverPeace: {field: lipgloss.Black, ink: [4]color.Color{lipgloss.Yellow}},

	coverTerrazzo: {field: lipgloss.Black, ink: [4]color.Color{
		lipgloss.BrightWhite, lipgloss.Red, lipgloss.Cyan}},

	// A contour map on the reader's own paper. The web's violet is between blue and
	// magenta and no palette has it; blue is the darkest hue on offer and still
	// read as too bright across a whole cover, and there is nothing darker than a
	// hue except the background. So the field is the background and the contours
	// keep the violet end of the palette.
	coverTopo:  {field: lipgloss.Black, ink: [4]color.Color{lipgloss.Magenta}},
	coverWaves: {field: lipgloss.Cyan, ink: [4]color.Color{lipgloss.BrightYellow}},
}

// --- Canvas ---

// coverCanvas is a grid of cells a painter writes glyphs into. Empty cells are
// the field: blank, so a colorless terminal shows the pattern rather than a
// solid block.
type coverCanvas struct {
	width   int
	height  int
	palette coverPalette
	glyphs  []string
	inks    []color.Color
}

func newCoverCanvas(width, height int, palette coverPalette) *coverCanvas {
	canvas := &coverCanvas{
		width:   width,
		height:  height,
		palette: palette,
		glyphs:  make([]string, width*height),
		inks:    make([]color.Color, width*height),
	}
	for i := range canvas.glyphs {
		canvas.glyphs[i] = " "
	}
	return canvas
}

// set writes a glyph, ignoring anything a painter puts outside the canvas so the
// painters can compute positions without bounds-checking each one.
func (c *coverCanvas) set(x, y int, glyph string, ink color.Color) {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return
	}
	c.glyphs[y*c.width+x] = glyph
	c.inks[y*c.width+x] = ink
}

func (c *coverCanvas) view() string {
	style := lipgloss.NewStyle()
	if c.palette.field != nil {
		style = style.Background(c.palette.field)
	}

	var b strings.Builder
	for y := range c.height {
		if y > 0 {
			b.WriteString("\n")
		}
		// Cells with the same ink render as one segment: a cover is a thousand
		// cells and all but the scatter patterns are long runs of the field.
		start := 0
		row := c.glyphs[y*c.width : (y+1)*c.width]
		inks := c.inks[y*c.width : (y+1)*c.width]
		for x := 1; x <= c.width; x++ {
			if x < c.width && inks[x] == inks[start] {
				continue
			}
			segment := style
			if inks[start] != nil {
				segment = segment.Foreground(inks[start])
			}
			b.WriteString(segment.Render(strings.Join(row[start:x], "")))
			start = x
		}
	}
	return b.String()
}

// plain returns the glyph grid without color, which is the pattern itself.
func (c *coverCanvas) plain() string {
	rows := make([]string, c.height)
	for y := range c.height {
		rows[y] = strings.Join(c.glyphs[y*c.width:(y+1)*c.width], "")
	}
	return strings.Join(rows, "\n")
}

// --- Braille ---

// A braille cell carries a 2×4 grid of dots, which is the finest thing a
// terminal can draw without leaving text behind. It is what lets a cover hold a
// curve: eight times the resolution of the cell grid, and thin lines rather than
// cell-wide staircases.
//
// The dots are square. A terminal cell is about twice as tall as it is wide and
// the 2×4 split cancels that exactly, so anything drawn here needs no aspect
// correction — a circle of equal radii comes out round.
const (
	brailleCols  = 2
	brailleRows  = 4
	brailleBlank = '⠀'
)

// brailleDot maps a dot's position in the cell to its bit. The order is the
// standard's, which numbers down the left column, then down the right, then the
// two bottom dots that eight-dot braille added — not row by row.
var brailleDot = [brailleRows][brailleCols]uint8{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// brailleLayer is a dot grid the size of a canvas. Painters draw into it in dots
// and hand the whole thing to drawInto, which folds each 2×4 block into its cell.
type brailleLayer struct {
	cols   int // canvas cells across
	rows   int // canvas cells down
	width  int // dots across
	height int // dots down
	dots   []bool
}

func newBrailleLayer(cols, rows int) *brailleLayer {
	width, height := cols*brailleCols, rows*brailleRows
	return &brailleLayer{
		cols: cols, rows: rows,
		width: width, height: height,
		dots: make([]bool, width*height),
	}
}

func (l *brailleLayer) set(x, y int) {
	if x >= 0 && y >= 0 && x < l.width && y < l.height {
		l.dots[y*l.width+x] = true
	}
}

func (l *brailleLayer) at(x, y int) bool {
	if x < 0 || y < 0 || x >= l.width || y >= l.height {
		return false
	}
	return l.dots[y*l.width+x]
}

func (l *brailleLayer) drawInto(c *coverCanvas, ink color.Color) {
	for y := range l.rows {
		for x := range l.cols {
			bits := uint8(0)
			for dy := range brailleRows {
				for dx := range brailleCols {
					if l.at(x*brailleCols+dx, y*brailleRows+dy) {
						bits |= brailleDot[dy][dx]
					}
				}
			}
			if bits != 0 {
				c.set(x, y, string(brailleBlank+rune(bits)), ink)
			}
		}
	}
}

// --- Painters ---

// paintGrid draws blueprint paper: a minor rule every three columns and every
// other row, a heavier one every fourth minor line.
func (c *coverCanvas) paintGrid() {
	const (
		minorCols, minorRows = 3, 2
		majorCols, majorRows = 12, 5
	)
	minor, major := c.palette.ink[0], c.palette.ink[1]

	for y := range c.height {
		for x := range c.width {
			onMajorRow, onMajorCol := y%majorRows == 2, x%majorCols == 6
			onMinorRow, onMinorCol := y%minorRows == 0, x%minorCols == 0
			switch {
			case onMajorRow && onMajorCol:
				c.set(x, y, "╋", major)
			case onMajorRow:
				c.set(x, y, "━", major)
			case onMajorCol:
				c.set(x, y, "┃", major)
			case onMinorRow && onMinorCol:
				c.set(x, y, "┼", minor)
			case onMinorRow:
				c.set(x, y, "─", minor)
			case onMinorCol:
				c.set(x, y, "│", minor)
			}
		}
	}
}

// paintTopo draws a contour map in braille. A dot is on a contour when it sits
// in a different band of the noise field than the dot left of or above it, which
// traces the level sets one dot wide.
//
// Braille is what makes this read as a map. A contour drawn out of box-drawing
// characters is a cell wide and a cell tall, so at any size worth looking at the
// lines come out as thick staircases — closer to noise than to terrain. A
// braille cell carries a 2×4 grid of dots, so the same contours land eight times
// finer, and thin. The dots are square, too: a terminal cell is about twice as
// tall as it is wide, which the 2×4 split cancels exactly, so the field needs no
// aspect correction and the contours come out round.
func (c *coverCanvas) paintTopo() {
	const (
		bands      = 7
		featureDiv = 32 // noise lattice, in dots
	)

	// The sample is shifted a dot in from the origin because a contour compares
	// against its left and upper neighbors, and the field starts at zero.
	band := func(x, y int) int {
		return int(coverNoise(float64(x+1)/featureDiv, float64(y+1)/featureDiv) * bands)
	}

	dots := newBrailleLayer(c.width, c.height)
	for y := range dots.height {
		for x := range dots.width {
			here := band(x, y)
			if here != band(x-1, y) || here != band(x, y-1) {
				dots.set(x, y)
			}
		}
	}
	dots.drawInto(c, c.palette.ink[0])
}

// paintTerrazzo scatters chips over the field. The scatter comes from the cell's
// own hash rather than a random source, so a cover is the same every time it is
// drawn, the way the web app's is.
func (c *coverCanvas) paintTerrazzo() {
	chips := []string{"▪", "◆", "▰", "●", "▮", "◣"}
	const density = 7 // one chip per this many cells

	for y := range c.height {
		for x := range c.width {
			h := coverHash(uint32(x), uint32(y))
			if h%density != 0 {
				continue
			}
			c.set(x, y, chips[int(h>>16)%len(chips)], c.palette.ink[(h>>8)%3])
		}
	}
}

// paintPeace tiles HEY's hand across the cover, offsetting alternate rows the way
// the web app's asset repeats it.
//
// This is the one cover that is a mark rather than a pattern, and the terminal
// already has the mark: U+270C is the gesture HEY draws. A tiled glyph is what
// the asset does anyway, so nothing is lost by letting the font draw the hand.
func (c *coverCanvas) paintPeace() {
	for y := peaceSpacingY / 2; y < c.height; y += peaceSpacingY {
		offset := 0
		if (y/peaceSpacingY)%2 == 1 {
			offset = peaceSpacingX / 2
		}
		for x := offset; x < c.width; x += peaceSpacingX {
			c.set(x, y, peaceHand, c.palette.ink[0])
		}
	}
}

// peaceHand is U+270C on its own, with no variation selector. Bare, it measures
// one cell wide; U+FE0F after it asks for emoji presentation and the glyph
// becomes two cells in some terminals and one in others, which shifts every hand
// to its right by an amount this cannot know.
const peaceHand = "✌"

// The gaps between hands, in cells. A glyph is one size whatever the cover's, so
// these are the one thing here that does not scale with the block — a mark drawn
// by the font cannot grow, and stretching the gaps instead would leave a large
// cover looking emptier rather than bigger. The row gap is about half the column
// gap because a cell is about twice as tall as it is wide, which puts the hands
// on a roughly square lattice.
const (
	peaceSpacingX = 10
	peaceSpacingY = 4
)

// maxWaveSlope is the steepest a ribbon may climb, in rows per column. A cell is
// about twice as tall as it is wide, so half a row per column is a ribbon at
// roughly forty-five degrees on screen — a sweep. Past that they read as
// chevrons.
const maxWaveSlope = 0.5

// waveRibbonShape is how tall and how long a ribbon is on a cover this size.
// Both scale with the block, so the pattern looks the same at any aspect ratio.
// The wavelength takes whichever is larger of two floors: a quarter of the width,
// so a wide cover shows a handful of sweeps instead of one lazy curve, and
// whatever the amplitude needs to stay under maxWaveSlope, which is what keeps a
// tall cover from turning into chevrons.
func waveRibbonShape(width, height int) (amplitude, wavelength float64) {
	amplitude = float64(height) / 3
	return amplitude, max(float64(width)/4, amplitude/maxWaveSlope)
}

// paintWaves lays HEY's yellow ribbons over mint. They are solid blocks rather
// than the field's background color so the shape survives a colorless terminal.
func (c *coverCanvas) paintWaves() {
	// Each ribbon is stretched from the base wavelength and given its own phase.
	// Sharing a wavelength and offsetting the phase is tempting and wrong: half a
	// wavelength apart makes the second ribbon an exact reflection of the first,
	// and the field comes out symmetrical. Stretch factors are all at or above 1
	// so no ribbon runs steeper than the base one.
	ribbons := []struct{ stretch, phase float64 }{{1, 0}, {1.3, 1.7}, {1.7, 4.1}}
	amplitude, wavelength := waveRibbonShape(c.width, c.height)
	center := float64(c.height-1) / 2
	thickness := max(float64(c.height)/14, 1)

	for x := range c.width {
		for _, r := range ribbons {
			ribbon := center + amplitude*math.Sin(float64(x)/(wavelength*r.stretch)+r.phase)
			for y := range c.height {
				if math.Abs(float64(y)-ribbon) < thickness {
					c.set(x, y, "█", c.palette.ink[0])
				}
			}
		}
	}
}

// paintBlobs sweeps soft bands of color from the top left to the bottom right,
// warped so they roll instead of stepping straight. Each band gets both a color
// and a step of the shade ramp: the shades read as a gradient on their own where
// there is no color, and blend one band into the next where there is.
//
// The web app's version is concentric arcs. A cover is far wider than it is
// tall, and over that block arcs flatten into vertical stripes, so the curve
// comes from the warp rather than from a radius.
func (c *coverCanvas) paintBlobs() {
	shades := []string{"░", "▒", "▓", "█"}
	const (
		wavelength = 11
		amplitude  = 9
		// Rows count for more than columns because cells are about twice as tall
		// as they are wide, and the sweep should read as a diagonal.
		rowWeight = 1.5
	)
	span := float64(c.width)/2 + float64(c.height)*rowWeight

	for y := range c.height {
		for x := range c.width {
			sweep := float64(x)/2 + float64(y)*rowWeight + amplitude*math.Sin(float64(x)/wavelength)
			band := min(max(int(sweep/span*float64(len(shades))), 0), len(shades)-1)
			c.set(x, y, shades[band], c.palette.ink[band])
		}
	}
}

// --- Deterministic value noise ---

func coverHash(x, y uint32) uint32 {
	const seed = 0x9e3779b9
	h := x*374761393 + y*668265263 + seed
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}

// coverNoise is value noise in [0,1): the lattice hash, smoothstepped between
// corners. Enough to grow contours out of, and it needs no assets. It is defined
// for non-negative coordinates only, which is the whole canvas.
func coverNoise(x, y float64) float64 {
	x0, y0 := math.Floor(max(x, 0)), math.Floor(max(y, 0))
	fx, fy := coverSmoothstep(x-x0), coverSmoothstep(y-y0)

	corner := func(ix, iy float64) float64 {
		return float64(coverHash(uint32(ix), uint32(iy))%1024) / 1024
	}

	top := coverLerp(corner(x0, y0), corner(x0+1, y0), fx)
	bottom := coverLerp(corner(x0, y0+1), corner(x0+1, y0+1), fx)
	return coverLerp(top, bottom, fy)
}

func coverSmoothstep(t float64) float64 { return t * t * (3 - 2*t) }

func coverLerp(a, b, t float64) float64 { return a + (b-a)*t }
