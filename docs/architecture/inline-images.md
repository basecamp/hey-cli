# Inline images in the TUI

The TUI renders inline images using the Kitty graphics protocol's Unicode Placeholder extension (`internal/tui/kitty.go`). This works because Bubble Tea's cell-based renderer corrupts raw APC escape sequences, but Unicode placeholders are regular text that survives rendering. The approach has three steps:

1. **Upload** — image data is sent to the terminal via `tea.Raw()` with `a=t` (transmit only) and `q=2` (suppress response), then a virtual placement is created with `U=1`. The upload declares `f=100`, which means PNG, so `pngEncoded` re-encodes anything else first: a JPEG or a GIF handed over under that format code is dropped by the terminal and the thread shows a gap where its image was.
2. **Display** — U+10EEEE placeholder characters with combining diacritics (encoding row/column) are placed in the viewport content. The image ID is encoded in the foreground color, so ids come from `nextImageID`: they start at `0x010101` to keep every color byte non-zero, stay inside three bytes, and are never reused. Reusing an id replaces the image the terminal holds under it while the placement drawn for the old geometry is still on screen, and the new image renders clipped into the old one's cells.
3. **Sizing** — `image.DecodeConfig` reads dimensions without full decoding. The terminal stretches the image over the cells it is placed in and scales each cell on its own, so an image placed in more cells than its pixels cover comes out smeared and seamed rather than merely soft. `imageDimensions` therefore treats the image's own size as the ceiling, and a tall image gives up columns instead of its proportions when it hits `maxImageRows`.

This works in Kitty and Ghostty. Other terminals show the text content normally (placeholders are invisible).
