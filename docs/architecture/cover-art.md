# Imbox cover art

A cover is a lid over Previously Seen, not a decoration next to it. When the Imbox is
covered the threads the reader has already read are not drawn at all — the box ends at
what still wants attention — and the art fills the list to the bottom of the screen. That
is the whole point of the feature in the web app, and it is why `hey`'s version hides
rather than merely paints.

`internal/tui/covers.go` draws the art. HEY's covers are SVG assets
(`box-covers/{light,dark}/*.svg` in haystack) and a terminal cannot draw an SVG, so these
are the same six patterns — blobs, grid, peace, terrazzo, topo, waves — drawn as
characters. That is not a fallback: a blueprint grid is what box-drawing characters are
for.

**Curves go in braille.** A `brailleLayer` is a dot grid at 2×4 the resolution of the
canvas, folded into cells by `drawInto`. `paintTopo` draws contours into one. The dots are
square, incidentally: a cell is about twice as tall as it is wide and the 2×4 split cancels
that exactly, so nothing drawn there needs aspect correction. Reach for braille whenever a
pattern wants a curve, because the cell grid cannot hold one: those same contours in
box-drawing characters are cell-wide staircases that read as noise.

**Peace is the one cover that is a mark, and the terminal already has the mark.**
`paintPeace` tiles U+270C — two fingers up in a V, which is what the "peace" cover is, not
the CND symbol — offsetting alternate rows the way the web app's asset repeats it. The
glyph is deliberately bare: `peaceHand` carries no U+FE0F, because a variation selector
asks for emoji presentation and the glyph then measures two cells in some terminals and one
in others, which slides every hand to its right by an amount nothing here can know.
`TestPeaceHandIsOneCellWide` pins that.

Two things this costs, both accepted rather than overlooked. A color emoji font ignores the
foreground color it is handed, so the hand arrives in the font's colors rather than the
reader's `Yellow` — the field is still theirs, and the ink is a fallback for the terminals
that render it monochrome. And a glyph is one size forever, so `peaceSpacingX`/`Y` are the
only constants here that do *not* scale with the block: the lattice gets denser on a big
cover rather than bigger, which is what a repeating asset does anyway.

**Otherwise, scale every dimension with the block, then check the extremes.** A cover is
anywhere from 40×6 to 300×90, so a constant tuned at one size is a constant that only looks
right there. `waveRibbonShape` is the worked example: the amplitude follows the height, and
the wavelength takes whichever is larger of a quarter of the width and whatever the
amplitude needs to stay under `maxWaveSlope`. Scaling by height alone drew chevrons on a
tall cover; by width alone, a busy repeat on a wide short one. Pin it with a test over a
spread of aspect ratios — it is much cheaper than looking.

A painter writes glyphs into a `coverCanvas` and the field is whatever it leaves blank,
which is why a colorless terminal still gets the art instead of an empty band. The colors
are the ANSI-16 slots, for the reason `styles.go` gives: a desktop theme defines those
sixteen and retints running terminals over OSC 4, so a cover wears the reader's palette
and restyles live on a theme switch without anything here being told. HEY's own covers are
yellow and mint and violet; `coverPalettes` names the slots those stand in for. Do not put
hex back — a cover in HEY's colors on someone's gruvbox is the one thing that will look
wrong on every theme but one.

**There is one palette per preset, not a light one and a dark one**, even though the web
app needs both. Pick each slot for the job it does in a theme rather than for how bright it
happens to be, and the light-versus-dark question stops being asked. `Black` is the
background and `White` the foreground in *every* theme, so a field of `Black` is the
reader's own paper and foreground ink is legible on it either way — that is why HEY's white
terrazzo chips correctly turn black in light mode without a light palette existing. Hues
are mid-tones everywhere, so a hue on a hue holds its contrast too.

Choosing a slot for its brightness is the trap, and it fails in the direction that is
hardest to notice: a light-mode field painted `BrightWhite` comes out *dark*, because on a
light theme the bright foreground is a dark color. The cover is then inverted on exactly
the themes the light palette was added to fix. `TestCoversDoNotDependOnTheThemeMode` is
there to catch it — flipping `Theme.Dark` must not change a single byte of what is drawn.

Everything is deterministic: the scatter and the noise come from a cell's own hash, so a
cover is the same picture every time, the way the web app's asset is. `coverRenderer`
memoizes the last one it drew, because the posting list re-renders on every keystroke and
a cover is a thousand styled cells. Its memo key includes the palette, so a theme switch
repaints.

What the lid costs the list is in `contentList`. `coveredFrom` is the index the cover
starts at and `itemCount` is what the reader can still reach, so the cursor cannot land
under the art and a bulk action cannot aim at threads the reader thinks are put away.
`settleCover` is what keeps that true as the list changes underneath: opening a thread
turns it seen, which slides it under the cover, and a live re-read can do the same to a
selection. `listHeight` holds back the divider and `coverMinRows` at the bottom so the
cover can never be scrolled past, and `coverView` then gives the art every row the threads
above it did not use. A list too short for even the floor keeps the divider and skips the
art — the hint is worth more than a smear.

`z` lifts the cover (`coverPeeked`) and puts it back; the divider carries the hint and the
hidden count, where the web app puts its buttons. Setting a cover closes it, so a box
arrives covered rather than however the last one was left. Only the Imbox is coverable —
that is haystack's rule (`Box::Imbox#coverable?`), and it is the same box that gets the
seen sections at all. `mail.Source.Coverable()` is where that is decided, off the kind HEY
served rather than the box's name: a label somebody called "Imbox" is a label.

`ctrl+v` opens `coverPicker`, which draws whichever preset is highlighted — a cover is
picked by looking at it. The choice is stored locally, by `config.Cover` and
`config.SaveCover`, reached through the `loadCover` and `saveCover` seams on the view
context so the TUI does not depend on where it lives. A failed write is a notice, not a
refusal: the cover is on screen either way and all that is lost is remembering it.

**Local storage is the deliberate answer here, not a stopgap.** haystack does keep a cover
per box, in a `box_covers` table with a real `preset_image` column — but it serves it to
nobody. There is no `cover` in any jbuilder, and both native apps went their own way rather
than wait for one: `CoverArtPersistenceManager` on iOS keeps the choice in `Preferences`,
`CoverArtRepository` on Android keeps it in `CachePrefs`, each with its own numbered presets
and bundled assets, neither ever asking the server. So a cover set here does not show up on
the web, and that is true of every HEY client. Before adding a read of `box_covers`, work
out why two mobile teams decided against it.

An uploaded cover has no honest character version — that one wants the Kitty path in
`kitty.go`.

The help bar puts chorded keys last, via `modifiersLast`. The single-key bindings are what
a reader reaches for while working through mail; a `ctrl+` chord in the middle of them
pushes the everyday ones onto a second line and makes the bar read as a lookup table.
