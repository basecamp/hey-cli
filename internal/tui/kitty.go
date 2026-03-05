package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder for image.DecodeConfig
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"math"
	"strings"
)

// Diacritics for encoding row/column indices in Kitty unicode placeholders.
// From https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders
var diacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0596,
	0x0597, 0x0598, 0x0599, 0x059A, 0x059B, 0x059C, 0x059D, 0x059E,
	0x059F, 0x05A0, 0x05A1, 0x05A2, 0x05A3, 0x05A4, 0x05A5, 0x05A6,
	0x05A7, 0x05A8, 0x05A9, 0x05AA, 0x05AB, 0x05AC, 0x05AD, 0x05AE,
	0x05AF, 0x05C4, 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615,
	0x0616, 0x0617, 0x0657, 0x0658, 0x0659, 0x065A, 0x065B, 0x065C,
	0x065D, 0x065E,
}

const placeholder = '\U0010EEEE'

// kittyUploadAndPlace returns escape sequences to upload image data and create
// a virtual Unicode placement. The result should be sent via tea.Raw().
func kittyUploadAndPlace(data []byte, id, cols, rows int) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	const chunkSize = 4096

	if len(encoded) == 0 {
		return ""
	}

	var b strings.Builder

	// Upload image data: a=t (transmit only, no display), q=2 (suppress response)
	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]
		more := 1
		if end == len(encoded) {
			more = 0
		}

		if i == 0 {
			fmt.Fprintf(&b, "\033_Ga=t,t=d,f=100,q=2,i=%d,m=%d;%s\033\\", id, more, chunk)
		} else {
			fmt.Fprintf(&b, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}

	// Create virtual placement for unicode placeholders
	fmt.Fprintf(&b, "\033_Ga=p,U=1,i=%d,c=%d,r=%d,q=2;\033\\", id, cols, rows)

	return b.String()
}

// renderImagePlaceholder returns U+10EEEE characters with combining diacritics
// that encode image position for Kitty's unicode placeholder rendering.
func renderImagePlaceholder(id, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}

	// Cap to diacritics table size
	if cols > len(diacritics) {
		cols = len(diacritics)
	}
	if rows > len(diacritics) {
		rows = len(diacritics)
	}

	var b strings.Builder

	// Encode image ID as 24-bit foreground color
	r := (id >> 16) & 0xFF
	g := (id >> 8) & 0xFF
	bl := id & 0xFF
	fgSet := fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, bl)
	fgReset := "\033[39m"

	for row := 0; row < rows; row++ {
		b.WriteString(fgSet)
		for col := 0; col < cols; col++ {
			b.WriteRune(placeholder)
			b.WriteRune(diacritics[row]) // row diacritic
			b.WriteRune(diacritics[col]) // column diacritic
		}
		b.WriteString(fgReset)
		if row < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// imageDimensions calculates display size in terminal cells for an image.
// Terminal cells are roughly 2:1 (height:width ratio).
func imageDimensions(data []byte, maxCols int) (cols, rows int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		// Fallback for unknown formats (e.g. WebP)
		if maxCols > 40 {
			return 40, 10
		}
		return maxCols, 10
	}

	cols = maxCols
	if cols > 60 {
		cols = 60
	}
	// Divide by 2 because terminal cells are ~2x taller than wide
	rows = int(math.Round(float64(cols) * float64(cfg.Height) / float64(cfg.Width) / 2.0))
	if rows < 1 {
		rows = 1
	}
	if rows > 40 {
		rows = 40
	}
	return cols, rows
}
