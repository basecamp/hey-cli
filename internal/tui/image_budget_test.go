package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingFetcher answers every URL with a body of the size it names, or an error.
type countingFetcher struct {
	calls atomic.Int64
	size  int
	fail  map[string]bool
	slow  time.Duration
}

func (f *countingFetcher) Fetch(ctx context.Context, source string) ([]byte, error) {
	f.calls.Add(1)
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
	return []byte(strings.Repeat("x", f.size)), nil
}

func TestImageBudgetFetchesAtMostTheCount(t *testing.T) {
	urls := make([]string, 0, maxImagesPerView+5)
	for i := range cap(urls) {
		urls = append(urls, fmt.Sprintf("/rails/blobs/%d.png", i))
	}
	fetcher := &countingFetcher{size: 10}
	images := newImageBudget().fetchImages(context.Background(), fetcher, urls)
	if len(images) != maxImagesPerView || fetcher.calls.Load() != maxImagesPerView {
		t.Errorf("fetched %d images with %d requests, want %d and no request past the count", len(images), fetcher.calls.Load(), maxImagesPerView)
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
		"https://gopher.hey.com/avatar.png",
	})
	if len(images) != 3 || fetcher.calls.Load() != 3 {
		t.Errorf("fetched %d images with %d requests, want the two blob paths and the absolute URL only", len(images), fetcher.calls.Load())
	}
}

func TestImageBudgetKeepsWithinTheByteBudget(t *testing.T) {
	fetcher := &countingFetcher{size: int(maxImageBytesPerView/2) + 1}
	images := newImageBudget().fetchImages(context.Background(), fetcher, []string{"/rails/blobs/a.png", "/rails/blobs/b.png", "/rails/blobs/c.png"})
	if len(images) != 1 {
		t.Errorf("kept %d images, want one: the second would pass the byte budget", len(images))
	}
}

func TestImageBudgetSkipsFailuresAndKeepsOrder(t *testing.T) {
	fetcher := &countingFetcher{size: 10, fail: map[string]bool{"/rails/blobs/b.png": true}}
	images := newImageBudget().fetchImages(context.Background(), fetcher, []string{"/rails/blobs/a.png", "/rails/blobs/b.png", "/rails/blobs/c.png"})
	if len(images) != 2 {
		t.Errorf("kept %d images, want the two that loaded", len(images))
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
