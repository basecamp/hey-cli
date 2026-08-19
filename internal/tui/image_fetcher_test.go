package tui

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

type imageBlobDownloaderFunc func(context.Context, string, io.Writer) (int64, http.Header, error)

func (f imageBlobDownloaderFunc) DownloadBlob(ctx context.Context, source string, destination io.Writer) (int64, http.Header, error) {
	return f(ctx, source, destination)
}

func rejectingImageBlobDownloader(t *testing.T) imageBlobDownloaderFunc {
	t.Helper()
	return func(context.Context, string, io.Writer) (int64, http.Header, error) {
		t.Fatal("unexpected HEY image request")
		return 0, nil, errors.New("unexpected HEY image request")
	}
}

func TestTrustedImageFetcherSelectsEnvironmentGopherOrigin(t *testing.T) {
	tests := []struct {
		name       string
		heyOrigin  string
		wantGopher string
	}{
		{"production", "https://app.hey.com", "https://gopher.hey.com"},
		{"staging", "https://app.hey-staging.com", "https://gopher.hey-staging.com"},
		{"local development", "http://app.hey.localhost:3003", "http://127.0.0.1:8888"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := hey.NewClient(
				&hey.Config{BaseURL: test.heyOrigin},
				&hey.StaticTokenProvider{Token: "test-token"},
				hey.WithMaxRetries(0),
			)
			fetcher := newTrustedImageFetcher(client)

			if len(fetcher.gopherOrigins) != 1 {
				t.Fatalf("trusted Gopher origins = %v, want one", fetcher.gopherOrigins)
			}
			if _, ok := fetcher.gopherOrigins[test.wantGopher]; !ok {
				t.Fatalf("trusted Gopher origins = %v, want %s", fetcher.gopherOrigins, test.wantGopher)
			}
		})
	}
}

func TestTrustedImageFetcherRejectsLookalikeAndUnsafeOrigins(t *testing.T) {
	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		"https://gopher.hey.com",
	)

	for _, source := range []string{
		"http://gopher.hey.com/image.png",
		"https://gopher.hey.com.evil.example/image.png",
		"https://gopher.hey.com:444/image.png",
		"http://127.0.0.1/image.png",
		"data:image/png;base64,AAAA",
		"file:///etc/passwd",
	} {
		t.Run(source, func(t *testing.T) {
			if _, err := fetcher.Fetch(context.Background(), source); err == nil {
				t.Fatalf("unsafe image URL succeeded: %s", source)
			}
		})
	}
}

func TestTrustedImageFetcherRejectsURLCredentialsBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	imageData := testPNG(t)
	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	}))
	t.Cleanup(gopher.Close)

	parsed, err := url.Parse(gopher.URL + "/image.png")
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword("user", "password")
	fetcher := newTrustedImageFetcherWithOrigins(rejectingImageBlobDownloader(t), "https://app.hey.com", gopher.URL)

	if _, err := fetcher.Fetch(context.Background(), parsed.String()); err == nil {
		t.Fatal("image URL containing credentials succeeded")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("credential-bearing image URL made %d request(s)", got)
	}
}

func TestTrustedImageFetcherRejectsNetworkPathURL(t *testing.T) {
	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		"https://gopher.hey.com",
	)

	if _, err := fetcher.Fetch(context.Background(), "//127.0.0.1/internal.png"); err == nil {
		t.Fatal("network-path image URL succeeded")
	}
}

func TestTrustedImageFetcherLoadsRelativeAndSameOriginHEYImages(t *testing.T) {
	imageData := testPNG(t)
	for _, source := range []string{
		"/rails/active_storage/blobs/redirect/image.png",
		"https://app.hey.com/rails/active_storage/blobs/redirect/image.png",
	} {
		t.Run(source, func(t *testing.T) {
			var requested string
			downloader := imageBlobDownloaderFunc(func(_ context.Context, got string, destination io.Writer) (int64, http.Header, error) {
				requested = got
				written, err := destination.Write(imageData)
				return int64(written), http.Header{"Content-Type": []string{"image/png"}}, err
			})
			fetcher := newTrustedImageFetcherWithOrigins(downloader, "https://app.hey.com", "https://gopher.hey.com")

			got, err := fetcher.Fetch(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if requested != source || string(got) != string(imageData) {
				t.Fatalf("HEY image request = %q, data length = %d", requested, len(got))
			}
		})
	}
}

func TestTrustedImageFetcherRejectsOversizedHEYImage(t *testing.T) {
	downloader := imageBlobDownloaderFunc(func(_ context.Context, _ string, destination io.Writer) (int64, http.Header, error) {
		written, err := destination.Write([]byte("123456789"))
		return int64(written), http.Header{"Content-Type": []string{"image/png"}}, err
	})
	fetcher := newTrustedImageFetcherWithOrigins(downloader, "https://app.hey.com", "https://gopher.hey.com")
	fetcher.maxBytes = 8

	if _, err := fetcher.Fetch(context.Background(), "/image.png"); err == nil {
		t.Fatal("oversized HEY image succeeded")
	}
}

func TestTrustedImageFetcherLoadsImageFromGopherWithoutHEYCredentials(t *testing.T) {
	imageData := testPNG(t)
	var authorization string
	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	}))
	t.Cleanup(gopher.Close)

	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		gopher.URL,
	)

	got, err := fetcher.Fetch(context.Background(), gopher.URL+"/signed/image.png")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(imageData) {
		t.Fatal("Gopher image data changed")
	}
	if authorization != "" {
		t.Fatalf("Gopher received Authorization %q", authorization)
	}
}

func TestTrustedImageFetcherRejectsNonImageContent(t *testing.T) {
	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<script>alert(1)</script>"))
	}))
	t.Cleanup(gopher.Close)

	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		gopher.URL,
	)

	if _, err := fetcher.Fetch(context.Background(), gopher.URL+"/image.png"); err == nil {
		t.Fatal("non-image Gopher response succeeded")
	}
}

func TestTrustedImageFetcherRejectsExcessivePixelDimensions(t *testing.T) {
	imageData := testPNG(t)
	binary.BigEndian.PutUint32(imageData[16:20], 20_000)
	binary.BigEndian.PutUint32(imageData[20:24], 20_000)
	binary.BigEndian.PutUint32(imageData[29:33], crc32.ChecksumIEEE(imageData[12:29]))

	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	}))
	t.Cleanup(gopher.Close)

	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		gopher.URL,
	)

	if _, err := fetcher.Fetch(context.Background(), gopher.URL+"/image.png"); err == nil {
		t.Fatal("image with excessive pixel dimensions succeeded")
	}
}

func TestTrustedImageFetcherRejectsMalformedImage(t *testing.T) {
	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nmalformed"))
	}))
	t.Cleanup(gopher.Close)

	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		gopher.URL,
	)

	if _, err := fetcher.Fetch(context.Background(), gopher.URL+"/image.png"); err == nil {
		t.Fatal("malformed image succeeded")
	}
}

func TestTrustedImageFetcherRejectsOversizedGopherImage(t *testing.T) {
	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("123456789"))
	}))
	t.Cleanup(gopher.Close)

	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		gopher.URL,
	)
	fetcher.maxBytes = 8

	if _, err := fetcher.Fetch(context.Background(), gopher.URL+"/image.png"); err == nil {
		t.Fatal("oversized Gopher image succeeded")
	}
}

func TestTrustedImageFetcherRejectsGopherRedirectOutsideTrustedOrigin(t *testing.T) {
	var untrustedRequests atomic.Int64
	untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		untrustedRequests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("untrusted image"))
	}))
	t.Cleanup(untrusted.Close)

	gopher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, untrusted.URL+"/image.png", http.StatusFound)
	}))
	t.Cleanup(gopher.Close)

	fetcher := newTrustedImageFetcherWithOrigins(
		rejectingImageBlobDownloader(t),
		"https://app.hey.com",
		gopher.URL,
	)

	if _, err := fetcher.Fetch(context.Background(), gopher.URL+"/signed/image.png"); err == nil {
		t.Fatal("Gopher redirect to an untrusted origin succeeded")
	}
	if got := untrustedRequests.Load(); got != 0 {
		t.Fatalf("Gopher redirect reached an untrusted origin %d time(s)", got)
	}
}
