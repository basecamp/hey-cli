package tui

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// Inline images are fetched for a thread or a journal page after its text is read, one
// request per image, from a server that chose the images. The fetcher already bounds
// each image — origin, bytes, pixels, content type — and imageBudget bounds the set:
// how many are fetched for one view, how many bytes they may add up to, how long the
// whole fetch may take, and that the same URL is fetched once. A relative URL is fetched
// only from HEY's blob paths, the one place an email's images live.
//
// Lazy loading — fetching an image as the viewport reaches it — is not done here; every
// image within the budget is fetched before the view renders, and the budget is what
// keeps that bounded. The limits are literal numbers for the same reason threadload's
// are: a reader should be able to say what the TUI will and will not do.
const (
	// maxImagesPerView is how many inline images one thread or journal page fetches.
	maxImagesPerView = 24
	// maxImageBytesPerView is how many bytes of image data one view keeps in all.
	maxImageBytesPerView int64 = 48 << 20
	// imageFetchDeadline is how long all of one view's image fetches may take.
	imageFetchDeadline = 20 * time.Second
	// heyBlobPathPrefix is where a relative image URL must point.
	heyBlobPathPrefix = "/rails/"
)

type imageBudget struct {
	remaining      int
	remainingBytes int64
	seen           map[string]struct{}
}

func newImageBudget() *imageBudget {
	return &imageBudget{remaining: maxImagesPerView, remainingBytes: maxImageBytesPerView, seen: map[string]struct{}{}}
}

// fetchImages fetches the images a view may show, in the order their URLs were found,
// within the budget and the deadline. A URL that fails, repeats, points outside HEY's
// blob paths or would exceed the budget is skipped; the rest are returned in order.
func (b *imageBudget) fetchImages(ctx context.Context, fetcher imageFetcher, urls []string) [][]byte {
	if fetcher == nil || len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, imageFetchDeadline)
	defer cancel()

	var images [][]byte
	for _, source := range urls {
		if b.remaining <= 0 || ctx.Err() != nil {
			break
		}
		if !b.admit(source) {
			continue
		}
		data, err := fetcher.Fetch(ctx, source)
		if err != nil || len(data) == 0 {
			continue
		}
		if int64(len(data)) > b.remainingBytes {
			continue
		}
		b.remainingBytes -= int64(len(data))
		b.remaining--
		images = append(images, data)
	}
	return images
}

// admit reports whether a URL is one the budget will fetch: not seen before, and if
// relative, under HEY's blob paths.
func (b *imageBudget) admit(source string) bool {
	if _, seen := b.seen[source]; seen {
		return false
	}
	b.seen[source] = struct{}{}
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	if !parsed.IsAbs() && !strings.HasPrefix(parsed.Path, heyBlobPathPrefix) {
		return false
	}
	return true
}
