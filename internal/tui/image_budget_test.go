package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingFetcher answers every URL with a body of the size it names, or an error, and
// refuses a body past the allowance it is handed the way the real fetcher stops one on
// the wire. It remembers the allowances it was given, in order.
type countingFetcher struct {
	calls      atomic.Int64
	size       int
	fail       map[string]bool
	slow       time.Duration
	allowances []int64
}

func (f *countingFetcher) Fetch(ctx context.Context, source string, maxBytes int64) ([]byte, error) {
	f.calls.Add(1)
	f.allowances = append(f.allowances, maxBytes)
	if f.slow > 0 {
		select {
		case <-time.After(f.slow):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.fail[source] {
		return nil, errors.New("no such image")
	}
	if maxBytes > 0 && int64(f.size) > maxBytes {
		return nil, errors.New("image exceeds the allowance")
	}
	return []byte(strings.Repeat("x", f.size)), nil
}

func blobURLs(n int) []string {
	urls := make([]string, 0, n)
	for i := range n {
		urls = append(urls, fmt.Sprintf("/rails/blobs/%d.png", i))
	}
	return urls
}

func TestImageBudgetFetchesAtMostTheCount(t *testing.T) {
	fetcher := &countingFetcher{size: 10}
	images := newImageBudget().fetchImages(context.Background(), fetcher, blobURLs(maxImagesPerView+5))
	if len(images) != maxImagesPerView || fetcher.calls.Load() != maxImagesPerView {
		t.Errorf("fetched %d images with %d requests, want %d and no request past the count", len(images), fetcher.calls.Load(), maxImagesPerView)
	}
}

// The count is a bound on requests, not on images kept: a URL that fails is charged
// like one that loads, so content full of broken images cannot keep the TUI requesting
// until the deadline.
func TestImageBudgetChargesEveryRequestToTheCount(t *testing.T) {
	urls := blobURLs(maxImagesPerView + 5)
	fail := map[string]bool{}
	for _, source := range urls {
		fail[source] = true
	}
	fetcher := &countingFetcher{size: 10, fail: fail}
	images := newImageBudget().fetchImages(context.Background(), fetcher, urls)
	if len(images) != 0 || fetcher.calls.Load() != maxImagesPerView {
		t.Errorf("fetched %d images with %d requests, want none and no request past the count", len(images), fetcher.calls.Load())
	}
}

func TestImageBudgetFetchesEachURLOnce(t *testing.T) {
	fetcher := &countingFetcher{size: 10}
	images := newImageBudget().fetchImages(context.Background(), fetcher, []string{"/rails/blobs/a.png", "/rails/blobs/a.png", "/rails/blobs/b.png"})
	if len(images) != 2 || fetcher.calls.Load() != 2 {
		t.Errorf("fetched %d images with %d requests, want 2 and 2", len(images), fetcher.calls.Load())
	}
}

// A relative URL is fetched only from HEY's blob paths; an absolute one is left to the
// fetcher's own origin rules.
func TestImageBudgetFetchesRelativeURLsOnlyFromBlobPaths(t *testing.T) {
	fetcher := &countingFetcher{size: 10}
	images := newImageBudget().fetchImages(context.Background(), fetcher, []string{
		"/rails/active_storage/blobs/redirect/abc/chart.png",
		"/rails/blobs/chart.png",
		"/admin/export.png",
		"../rails/blobs/up.png",
		"/rails/../admin/export.png",
		"/rails/blobs/../../admin/export.png",
		"/rails/./blobs/chart.png",
		"//gopher.hey.com/rails/blobs/chart.png",
		"https://gopher.hey.com/avatar.png",
	})
	if len(images) != 3 || fetcher.calls.Load() != 3 {
		t.Errorf("fetched %d images with %d requests, want the two blob paths and the absolute URL only", len(images), fetcher.calls.Load())
	}
}

// The fetcher is handed what is left of the byte budget, so an image that would pass
// it is stopped on the wire at that allowance rather than downloaded whole.
func TestImageBudgetKeepsWithinTheByteBudget(t *testing.T) {
	size := int(maxImageBytesPerView/2) + 1
	fetcher := &countingFetcher{size: size}
	images := newImageBudget().fetchImages(context.Background(), fetcher, []string{"/rails/blobs/a.png", "/rails/blobs/b.png", "/rails/blobs/c.png"})
	if len(images) != 1 {
		t.Errorf("kept %d images, want one: the second would pass the byte budget", len(images))
	}
	remaining := maxImageBytesPerView - int64(size)
	if want := []int64{maxImageBytesPerView, remaining, remaining}; !slices.Equal(fetcher.allowances, want) {
		t.Errorf("allowances = %v, want %v: each fetch is told what is left", fetcher.allowances, want)
	}
}

func TestImageBudgetSkipsFailuresAndKeepsOrder(t *testing.T) {
	fetcher := &countingFetcher{size: 10, fail: map[string]bool{"/rails/blobs/b.png": true}}
	images := newImageBudget().fetchImages(context.Background(), fetcher, []string{"/rails/blobs/a.png", "/rails/blobs/b.png", "/rails/blobs/c.png"})
	if len(images) != 2 {
		t.Errorf("kept %d images, want the two that loaded", len(images))
	}
}

// The deadline is the budget's own: a fetch still in flight when it passes is cancelled,
// and nothing after it is requested.
func TestImageBudgetStopsAtItsDeadline(t *testing.T) {
	fetcher := &countingFetcher{size: 10, slow: time.Second}
	budget := newImageBudget()
	budget.deadline = 20 * time.Millisecond
	started := time.Now()
	images := budget.fetchImages(context.Background(), fetcher, []string{"/rails/blobs/a.png", "/rails/blobs/b.png"})
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("fetching took %s, want the in-flight fetch cancelled at the deadline", elapsed)
	}
	if len(images) != 0 || fetcher.calls.Load() != 1 {
		t.Errorf("fetched %d images with %d requests, want none and no request after the deadline", len(images), fetcher.calls.Load())
	}
}

func TestImageBudgetStopsAtTheCallersCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetcher := &countingFetcher{size: 10}
	if images := newImageBudget().fetchImages(ctx, fetcher, []string{"/rails/blobs/a.png"}); len(images) != 0 || fetcher.calls.Load() != 0 {
		t.Errorf("fetched %d with %d requests after cancellation", len(images), fetcher.calls.Load())
	}
}
