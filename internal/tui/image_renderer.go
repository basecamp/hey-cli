package tui

import (
	"os"
	"strings"
)

type imageProtocol string

const (
	imageProtocolText  imageProtocol = "text"
	imageProtocolKitty imageProtocol = "kitty"
	imageProtocolSixel imageProtocol = "sixel"
)

type renderedImage struct {
	content string
	raw     string
}

type imageRenderer interface {
	protocol() imageProtocol
	render(data []byte, id, maxCols int) renderedImage
}

type textImageRenderer struct{}

type kittyImageRenderer struct{}

func selectImageRenderer(lookupEnv func(string) string) imageRenderer {
	switch detectImageCapability(lookupEnv) {
	case imageProtocolKitty:
		return kittyImageRenderer{}
	case imageProtocolSixel:
		// Sixel graphics are cursor-positioned. Bubble Tea redraws the cell grid
		// after raw output, so use the visible text marker until it has a stable
		// Sixel placement integration.
		return textImageRenderer{}
	default:
		return textImageRenderer{}
	}
}

func detectImageCapability(lookupEnv func(string) string) imageProtocol {
	term := strings.ToLower(lookupEnv("TERM"))
	termProgram := strings.ToLower(lookupEnv("TERM_PROGRAM"))

	if lookupEnv("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty") || termProgram == "kitty" || termProgram == "ghostty" {
		return imageProtocolKitty
	}
	if strings.HasPrefix(term, "foot") || termProgram == "foot" {
		return imageProtocolSixel
	}
	return imageProtocolText
}

func environmentImageRenderer() imageRenderer {
	return selectImageRenderer(os.Getenv)
}

func (textImageRenderer) protocol() imageProtocol { return imageProtocolText }

func (textImageRenderer) render([]byte, int, int) renderedImage { return renderedImage{} }

func (kittyImageRenderer) protocol() imageProtocol { return imageProtocolKitty }

func (kittyImageRenderer) render(data []byte, id, maxCols int) renderedImage {
	cols, rows := imageDimensions(data, maxCols)
	return renderedImage{
		content: renderImagePlaceholder(id, cols, rows),
		raw:     kittyUploadAndPlace(data, id, cols, rows),
	}
}
