package tui

import (
	"image/color"
	"math"
	"os"
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

// imboxCover is the preset the Imbox is covered with. HEY serves a box's cover to
// the web app but not over JSON, so HEY_COVER stands in until boxes carry it and
// this reads the box instead. An unset or unknown value draws nothing.
func imboxCover() coverPreset {
	return parseCoverPreset(os.Getenv("HEY_COVER"))
}

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
	coverBlobs: {field: lipgloss.Cyan, ink: [4]color.Color{lipgloss.BrightYellow}},
	coverGrid:  {field: lipgloss.Black, ink: [4]color.Color{lipgloss.Blue, lipgloss.BrightBlue}},
	coverPeace: {field: lipgloss.Yellow, ink: [4]color.Color{lipgloss.BrightWhite}},
	coverTerrazzo: {field: lipgloss.Black, ink: [4]color.Color{
		lipgloss.BrightWhite, lipgloss.Red, lipgloss.Cyan}},
	coverTopo: {field: lipgloss.Magenta, ink: [4]color.Color{lipgloss.Black}},
	coverWaves: {field: lipgloss.Magenta, ink: [4]color.Color{
		lipgloss.BrightYellow, lipgloss.Red, lipgloss.BrightMagenta, lipgloss.Blue}},
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

// coverEllipse is a filled ellipse. Everything a cover draws with curves is built
// out of these and outlined by silhouette; there is no stroke-an-arc primitive,
// because filling and tracing gives a shape of any thickness and a union of any
// number of parts for the same effort.
type coverEllipse struct {
	cx, cy, rx, ry, tilt float64
}

func (e coverEllipse) contains(px, py float64) bool {
	dx, dy := px-e.cx, py-e.cy
	sin, cos := math.Sin(e.tilt), math.Cos(e.tilt)
	lx, ly := dx*cos+dy*sin, dy*cos-dx*sin
	return (lx*lx)/(e.rx*e.rx)+(ly*ly)/(e.ry*e.ry) <= 1
}

// silhouette draws the outline of the union of shapes: one continuous stroke
// around everything they cover together, with no seam where they meet.
//
// Abutting drawn arcs cannot do this. Their ends never quite land on each other,
// so the joins show as notches and stray tails, and every arc that crosses
// another leaves its own line running through the inside of the shape. Filling
// the union and keeping the dots that have a neighbor outside it sidesteps the
// joins altogether — there is only ever one boundary to find.
func (l *brailleLayer) silhouette(shapes ...coverEllipse) {
	inside := func(px, py float64) bool {
		for _, shape := range shapes {
			if shape.contains(px, py) {
				return true
			}
		}
		return false
	}

	// Only the shapes' own extent is scanned. A silhouette is stamped many times
	// over a cover, and walking the whole dot grid for each one is millions of
	// point tests to find a shape that covers a corner of it.
	left, top := math.Inf(1), math.Inf(1)
	right, bottom := math.Inf(-1), math.Inf(-1)
	for _, shape := range shapes {
		reach := max(shape.rx, shape.ry)
		left, top = min(left, shape.cx-reach), min(top, shape.cy-reach)
		right, bottom = max(right, shape.cx+reach), max(bottom, shape.cy+reach)
	}

	for y := max(int(top)-1, 0); y <= min(int(bottom)+1, l.height-1); y++ {
		for x := max(int(left)-1, 0); x <= min(int(right)+1, l.width-1); x++ {
			fx, fy := float64(x), float64(y)
			if inside(fx, fy) && !buried(inside, fx, fy) {
				l.set(x, y)
			}
		}
	}
}

// coverStroke is how many dots thick a drawn edge is. One dot is a dotted line
// rather than a line: braille dots do not touch, so a single-dot outline on a
// bright field reads as a scattering of specks and the shape it describes is lost.
const coverStroke = 2

// buried reports whether everything within the stroke's reach of this dot is also
// inside the shape, which is what puts the dot in the interior rather than on the
// edge. Reaching further than one dot is what thickens the edge.
func buried(inside func(x, y float64) bool, x, y float64) bool {
	for step := 1.0; step <= coverStroke; step++ {
		if !inside(x-step, y) || !inside(x+step, y) || !inside(x, y-step) || !inside(x, y+step) {
			return false
		}
	}
	return true
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

// paintPeace tiles HEY's hand in braille, offsetting alternate rows.
//
// Character cells cannot hold this. Two earlier attempts proved it: U+270C is
// the right gesture, but a terminal is free to take an emoji codepoint from a
// color font, which ignores the foreground color it is handed and arrives as its
// own dull yellow hand, invisible on yellow; box-drawing line art at five cells
// across has too few strokes to read as anything but a broken television. At 2×4
// dots to the cell there is room for the fingers, the fist and the thumb.
func (c *coverCanvas) paintPeace() {
	dots := newBrailleLayer(c.width, c.height)
	height := peaceHandHeight(dots.height)
	width := int(peaceHandAspect * float64(height))
	spacingX, spacingY := width*5/4, height*5/4

	for y := 0; y < dots.height+spacingY; y += spacingY {
		offset := 0
		if (y/spacingY)%2 == 1 {
			offset = spacingX / 2
		}
		for x := offset - spacingX; x < dots.width+spacingX; x += spacingX {
			dots.peaceHand(float64(x), float64(y), float64(width), float64(height))
		}
	}
	dots.drawInto(c, c.palette.ink[0])
}

// peaceHandAspect is the hand's width as a fraction of its height. The fingers
// splay wide enough that the mark comes out nearly square.
const peaceHandAspect = 0.85

// peaceHandHeight is how tall the hand may be, in dots, on a cover this many dots
// tall. It never grows past what the cover can hold — clipped, the fingers run off
// the top and the mark stops being a hand — and it is big enough that the fingers
// read as loops rather than strokes.
// The preferred height is generous because the mark has six shapes in it: the
// curled fingers stop telling themselves apart below about sixty dots, and the
// whole thing is a smudge below twenty.
func peaceHandHeight(dotRows int) int {
	const preferred = 76
	return max(min(preferred, dotRows-2), 14)
}

// peaceHand draws HEY's mark: the index and middle fingers up in a V over a
// closed fist, with the thumb across the front of it. Everything is a fraction of
// the mark's own box, so the same drawing works at any size the cover allows.
//
// The palm, the two fingers and the thumb are one silhouette — a single outline
// around the whole hand — with the thumb's own loop drawn back over it, which is
// the only line inside the shape. The thumb is what says the fist is a fist.
func (l *brailleLayer) peaceHand(x, y, width, height float64) {
	const lean = 0.42 // radians each finger tips away from vertical

	// A finger is placed by its base, so the two of them rise from one point in
	// the palm and splay apart. Leaning an ellipse about its own center instead
	// swings the base sideways, sending each finger's base across to the other's
	// side: the loops cross halfway up and the mark reads as a rabbit from behind.
	finger := func(baseX, baseY, halfWidth, length, angle float64) coverEllipse {
		return coverEllipse{
			cx:   baseX + length*math.Sin(angle),
			cy:   baseY - length*math.Cos(angle),
			rx:   halfWidth,
			ry:   length,
			tilt: angle,
		}
	}

	const (
		// The thumb lies across the front of the fist at about twenty degrees off
		// horizontal, so its angle is measured from vertical like the fingers' but
		// is nearly a right angle.
		thumbAngle = math.Pi/2 - 0.35
		// The curled fingers lie twenty degrees further round than the index, which
		// is counter-clockwise on screen: a positive angle leans a shape clockwise.
		curlAngle = -lean - 0.345
	)

	// The palm is a squashed oval, turned thirty degrees clockwise. Its centre
	// sits lower than the height alone would suggest so that the fingers still
	// root well inside it: a shallow palm whose edge only grazes the finger bases
	// leaves the silhouette pinched where they meet, or breaks it in two.
	// Shorter on the left than the right, which an ellipse cannot be: the long axis
	// is trimmed and the centre slid along it by half as much, so the left end
	// comes in and the right end stays put.
	palm := coverEllipse{x + width*0.44, y + height*0.752, width * 0.259, height * 0.14, 0.37}
	index := finger(x+width*0.52, y+height*0.68, width*0.07, height*0.25, -lean)
	middle := finger(x+width*0.52, y+height*0.68, width*0.07, height*0.28, lean)
	thumb := finger(x+width*0.545, y+height*0.705, width*0.05, height*0.16, thumbAngle)

	// The last two fingers, curled along the left, overlapping the index just
	// enough to belong to the same hand.
	// Wide enough that a two-dot stroke still leaves them hollow: any thinner and
	// the two edges meet in the middle and the finger is a solid smear.
	ring := finger(x+width*0.486, y+height*0.715, width*0.065, height*0.14, curlAngle)
	little := finger(x+width*0.42, y+height*0.745, width*0.06, height*0.12, curlAngle)

	// The palm, the V and the thumb are one silhouette. The curled fingers are not
	// in it: unioned, their outlines merge into the palm's and trail off along it,
	// and the two of them read as one lump on the edge of the fist. Laid over a
	// whole palm they stay two shapes.
	l.silhouette(palm, index, middle, thumb)
	for _, over := range []coverEllipse{thumb, ring, little} {
		l.silhouette(over)
	}
}

// maxBlobSlope is the steepest a ribbon may climb, in rows per column. A cell is
// about twice as tall as it is wide, so half a row per column is a ribbon at
// roughly forty-five degrees on screen — a sweep. Past that they read as
// chevrons.
const maxBlobSlope = 0.5

// blobRibbonShape is how tall and how long a ribbon is on a cover this size.
// Both scale with the block, so the pattern looks the same at any aspect ratio.
// The wavelength takes whichever is larger of two floors: a quarter of the width,
// so a wide cover shows a handful of sweeps instead of one lazy curve, and
// whatever the amplitude needs to stay under maxBlobSlope, which is what keeps a
// tall cover from turning into chevrons.
func blobRibbonShape(width, height int) (amplitude, wavelength float64) {
	amplitude = float64(height) / 3
	return amplitude, max(float64(width)/4, amplitude/maxBlobSlope)
}

// paintBlobs lays HEY's yellow ribbons over mint. They are solid blocks rather
// than the field's background color so the shape survives a colorless terminal.
func (c *coverCanvas) paintBlobs() {
	// Each ribbon is stretched from the base wavelength and given its own phase.
	// Sharing a wavelength and offsetting the phase is tempting and wrong: half a
	// wavelength apart makes the second ribbon an exact reflection of the first,
	// and the field comes out symmetrical. Stretch factors are all at or above 1
	// so no ribbon runs steeper than the base one.
	ribbons := []struct{ stretch, phase float64 }{{1, 0}, {1.3, 1.7}, {1.7, 4.1}}
	amplitude, wavelength := blobRibbonShape(c.width, c.height)
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

// paintWaves sweeps a gradient from the top left to the bottom right, warped by
// a wave so the bands roll instead of stepping straight. Each band gets both a
// color and a step of the shade ramp: the shades read as a gradient on their own
// where there is no color, and blend one band into the next where there is.
//
// The web app's version is concentric arcs. A cover is far wider than it is
// tall, and over that block arcs flatten into vertical stripes, so the curve
// comes from the warp rather than from a radius.
func (c *coverCanvas) paintWaves() {
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
