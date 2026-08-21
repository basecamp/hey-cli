package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestKittyUploadAndPlaceSmall(t *testing.T) {
	result := kittyUploadAndPlace([]byte("hello"), 1, 10, 5)
	// Should contain upload sequence
	if !strings.Contains(result, "\033_Ga=t,") {
		t.Error("should contain upload APC sequence")
	}
	// Should contain placement sequence
	if !strings.Contains(result, "\033_Ga=p,U=1,i=1,c=10,r=5") {
		t.Error("should contain virtual placement sequence")
	}
}

func TestKittyUploadAndPlaceEmpty(t *testing.T) {
	result := kittyUploadAndPlace(nil, 1, 10, 5)
	if result != "" {
		t.Errorf("empty data should return empty string, got %q", result)
	}
}

func TestKittyUploadAndPlaceLarge(t *testing.T) {
	data := make([]byte, 4000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	result := kittyUploadAndPlace(data, 1, 10, 5)
	// Should have continuation chunks (m=1)
	if !strings.Contains(result, "m=1") {
		t.Error("large data should have continuation chunks with m=1")
	}
	// Should end with placement
	if !strings.Contains(result, "a=p,U=1") {
		t.Error("should end with virtual placement")
	}
}

func TestRenderImagePlaceholder(t *testing.T) {
	result := renderImagePlaceholder(1, 3, 2)
	// Should contain the placeholder character
	if !strings.ContainsRune(result, placeholder) {
		t.Error("should contain U+10EEEE placeholder character")
	}
	// Should contain foreground color sequence (ID=1 → 0,0,1)
	if !strings.Contains(result, "\033[38;2;0;0;1m") {
		t.Error("should contain foreground color encoding image ID")
	}
	// Should contain reset
	if !strings.Contains(result, "\033[39m") {
		t.Error("should contain foreground color reset")
	}
	// Should have a newline between rows
	if !strings.Contains(result, "\n") {
		t.Error("should have newline between rows")
	}
}

func TestRenderImagePlaceholderZero(t *testing.T) {
	result := renderImagePlaceholder(1, 0, 0)
	if result != "" {
		t.Errorf("zero dimensions should return empty string, got %q", result)
	}
}

func TestImageDimensionsStaysWithinTheImagesOwnPixels(t *testing.T) {
	// A logo the size of the one Sentry puts at the top of its notifications.
	cols, rows := imageDimensions(pngImage(t, 234, 64), 100)
	if cols != 24 || rows != 3 {
		t.Errorf("dimensions = (%d, %d), want (24, 3)", cols, rows)
	}

	// An icon must not be blown up over half the thread either.
	cols, rows = imageDimensions(pngImage(t, 32, 32), 100)
	if cols != 4 || rows != 2 {
		t.Errorf("dimensions = (%d, %d), want (4, 2)", cols, rows)
	}
}

func TestImageDimensionsCaps(t *testing.T) {
	cols, rows := imageDimensions(pngImage(t, 2000, 1000), 100)
	if cols != maxImageCols || rows != 15 {
		t.Errorf("wide image = (%d, %d), want (%d, 15)", cols, rows, maxImageCols)
	}

	cols, rows = imageDimensions(pngImage(t, 2000, 1000), 30)
	if cols != 30 || rows != 7 {
		t.Errorf("narrow viewport = (%d, %d), want (30, 7)", cols, rows)
	}

	// A tall screenshot loses columns rather than its proportions.
	cols, rows = imageDimensions(pngImage(t, 600, 4000), 100)
	if cols != 12 || rows != maxImageRows {
		t.Errorf("tall image = (%d, %d), want (12, %d)", cols, rows, maxImageRows)
	}
}

func TestPngEncodedRewritesWhatTheTerminalWouldDrop(t *testing.T) {
	original := pngImage(t, 40, 20)
	if !bytes.Equal(pngEncoded(original), original) {
		t.Error("a PNG should be passed through untouched")
	}

	encoded := pngEncoded(jpegImage(t, 40, 20))
	if !bytes.HasPrefix(encoded, []byte("\x89PNG\r\n\x1a\n")) {
		t.Error("a JPEG should be re-encoded as PNG")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil || format != "png" || config.Width != 40 || config.Height != 20 {
		t.Errorf("re-encoded image = %s %dx%d, err %v", format, config.Width, config.Height, err)
	}

	if got := pngEncoded([]byte("not an image")); string(got) != "not an image" {
		t.Errorf("undecodable data should be left alone, got %q", got)
	}
}

func TestNextImageID(t *testing.T) {
	first, second := nextImageID(), nextImageID()
	if first == second {
		t.Error("ids must not repeat")
	}
	for _, id := range []int{first, second} {
		assertUsableImageID(t, id)
	}
}

// Every byte of an id is a base-255 digit offset by one, so carrying from one digit to the
// next never leaves a zero byte behind — which is where plain addition went wrong: the
// 255th id it handed out was 0x010200, and a terminal reads a color with a zero byte as a
// palette index and loses the image.
func TestNextImageIDCarriesWithoutAZeroByte(t *testing.T) {
	t.Cleanup(func() { imageIDs.Store(0) })

	imageIDs.Store(250)
	seen := make(map[int]struct{}, 12)
	for range 12 {
		id := nextImageID()
		assertUsableImageID(t, id)
		if _, repeated := seen[id]; repeated {
			t.Errorf("id %#x was handed out twice across a byte boundary", id)
		}
		seen[id] = struct{}{}
	}

	// The same boundary a digit further up.
	imageIDs.Store(imageIDDigit*imageIDDigit - 2)
	for range 4 {
		assertUsableImageID(t, nextImageID())
	}
}

// There is nothing safe left to hand out once every id has been: an image drawn under a
// reused id takes over one that is still on screen, so the ids run out rather than round.
func TestNextImageIDStopsMintingWhenItRunsOut(t *testing.T) {
	t.Cleanup(func() { imageIDs.Store(0) })

	imageIDs.Store(imageIDCount - 1)
	if last := nextImageID(); last != lastImageID {
		t.Errorf("last id = %#x, want %#x", last, lastImageID)
	}
	if exhausted := nextImageID(); exhausted != noImageID {
		t.Errorf("id past the last = %#x, want none", exhausted)
	}
	if drawn := renderImagePlaceholder(noImageID, 4, 2); drawn != "" {
		t.Errorf("an image without an id drew %q", drawn)
	}
	if uploaded := kittyUploadAndPlace(pngImage(t, 40, 20), noImageID, 4, 2); uploaded != "" {
		t.Errorf("an image without an id was uploaded: %q", uploaded)
	}
}

func assertUsableImageID(t *testing.T, id int) {
	t.Helper()

	if id>>16&0xFF == 0 || id>>8&0xFF == 0 || id&0xFF == 0 {
		t.Errorf("id %#x has a zero color byte", id)
	}
	if id < firstImageID || id > lastImageID {
		t.Errorf("id %#x is outside the three bytes of color that carry it", id)
	}
}

func pngImage(t *testing.T, width, height int) []byte {
	t.Helper()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, filledImage(width, height)); err != nil {
		t.Fatalf("encoding PNG: %v", err)
	}
	return encoded.Bytes()
}

func jpegImage(t *testing.T, width, height int) []byte {
	t.Helper()

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, filledImage(width, height), nil); err != nil {
		t.Fatalf("encoding JPEG: %v", err)
	}
	return encoded.Bytes()
}

func filledImage(width, height int) image.Image {
	filled := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			filled.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	return filled
}

func TestImageDimensionsFallback(t *testing.T) {
	// Invalid image data should use fallback dimensions
	cols, rows := imageDimensions([]byte("not an image"), 80)
	if cols != 40 || rows != 10 {
		t.Errorf("fallback dimensions = (%d, %d), want (40, 10)", cols, rows)
	}
}
