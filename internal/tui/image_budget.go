package tui

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
)

// Inline images are fetched for a thread or a journal page after its text is read, one
// request per image, from a server that chose the images. The fetcher already bounds
// each image — origin, bytes, pixels, content type — and imageBudget bounds the set:
// how many requests one view may make, how many bytes the images it keeps may add up
// to, how long the whole fetch may take, and that the same URL is fetched once. A
// relative URL is fetched only from HEY's blob paths, the one place an email's images
// live.
//
// Lazy loading — fetching an image as the viewport reaches it — is not done here; every
// image within the budget is fetched before the view renders, and the budget is what
// keeps that bounded (#262 is the follow-up). The limits are literal numbers for the
// same reason threadload's are: a reader should be able to say what the TUI will and
// will not do.
const (
	// maxImagesPerView is how many image requests one thread or journal page makes.
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
	deadline       time.Duration
	seen           map[string]struct{}
}

func newImageBudget() *imageBudget {
	return &imageBudget{
		remaining:      maxImagesPerView,
		remainingBytes: maxImageBytesPerView,
		deadline:       imageFetchDeadline,
		seen:           map[string]struct{}{},
	}
}

// fetchImages fetches the images a view may show, in the order their URLs were found,
// within the budget and the deadline. A URL that repeats, points outside HEY's blob
// paths or the fetcher refuses on sight is not requested; one that fails or would not
// fit in what is left of the byte budget is skipped. Every request made is charged to
// the count, whatever it answers: the content chose the URLs, and a URL that fails is
// still a request it caused. A refusal is not a request, so it costs nothing — two
// dozen third-party images ahead of HEY's own cannot use up the count.
func (b *imageBudget) fetchImages(ctx context.Context, fetcher imageFetcher, urls []string) [][]byte {
	if fetcher == nil || len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, b.deadline)
	defer cancel()

	var images [][]byte
	for _, source := range urls {
		if b.remaining <= 0 || b.remainingBytes <= 0 || ctx.Err() != nil {
			break
		}
		if !b.admit(source) {
			continue
		}
		b.remaining--
		// The fetcher is told what is left, so an image that would not fit is stopped
		// on the wire rather than downloaded whole and then discarded.
		data, err := fetcher.Fetch(ctx, source, b.remainingBytes)
		if errors.Is(err, errImageRefused) {
			b.remaining++
			continue
		}
		if err != nil || len(data) == 0 || int64(len(data)) > b.remainingBytes {
			continue
		}
		b.remainingBytes -= int64(len(data))
		images = append(images, data)
	}
	return images
}

// admit reports whether a URL is one the budget will request: not seen before, and if
// relative, a clean path under HEY's blob paths. The path is judged as it resolves, so
// "/rails/../admin/export.png" is the request outside the blob paths that it is, and a
// path that is not already clean is refused rather than rewritten — HEY never serves
// one, so there is nothing to fetch behind it.
func (b *imageBudget) admit(source string) bool {
	if _, seen := b.seen[source]; seen {
		return false
	}
	b.seen[source] = struct{}{}
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	if parsed.IsAbs() {
		return true
	}
	return parsed.Host == "" && path.Clean(parsed.Path) == parsed.Path && strings.HasPrefix(parsed.Path, heyBlobPathPrefix)
}
