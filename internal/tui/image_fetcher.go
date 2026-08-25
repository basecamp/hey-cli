package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder for image.DecodeConfig
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/version"
)

type imageBlobDownloader interface {
	DownloadBlob(context.Context, string, io.Writer) (int64, http.Header, error)
}

// An imageFetcher reads one image, within its own bounds and within maxBytes, which is
// what the caller has left to spend on it: a body past that is stopped on the wire, not
// downloaded whole and then refused. A maxBytes of zero or less leaves the fetcher's own
// bound as the only one.
type imageFetcher interface {
	Fetch(ctx context.Context, source string, maxBytes int64) ([]byte, error)
}

const (
	maxInlineImageBytes  int64 = 20 * 1024 * 1024
	maxInlineImagePixels int64 = 100 * 1000 * 1000
)

// errImageRefused is the error a fetcher answers for a URL it will not request at all —
// malformed, carrying credentials, or from an origin it does not trust. No request was
// made, which is what a budget counting requests needs to know.
var errImageRefused = errors.New("image URL refused")

type trustedImageFetcher struct {
	heyOrigin     *url.URL
	hey           imageBlobDownloader
	gopherOrigins map[string]struct{}
	gopherClient  *http.Client
	maxBytes      int64
}

func newTrustedImageFetcher(client *hey.Client) *trustedImageFetcher {
	if client == nil {
		return nil
	}

	baseURL := client.Config().BaseURL
	return newTrustedImageFetcherWithOrigins(client, baseURL, gopherOriginForHEY(baseURL))
}

func gopherOriginForHEY(heyOrigin string) string {
	parsed, err := url.Parse(heyOrigin)
	if err != nil {
		return "https://gopher.hey.com"
	}
	hostname := strings.ToLower(parsed.Hostname())
	address := net.ParseIP(hostname)
	switch {
	case hostname == "localhost", strings.HasSuffix(hostname, ".localhost"), address != nil && address.IsLoopback():
		return "http://127.0.0.1:8888"
	case hostname == "hey-staging.com", strings.HasSuffix(hostname, ".hey-staging.com"):
		return "https://gopher.hey-staging.com"
	default:
		return "https://gopher.hey.com"
	}
}

func newTrustedImageFetcherWithOrigins(client imageBlobDownloader, heyOrigin string, gopherOrigins ...string) *trustedImageFetcher {
	parsedHEYOrigin, _ := url.Parse(heyOrigin)
	trustedOrigins := make(map[string]struct{}, len(gopherOrigins))
	for _, origin := range gopherOrigins {
		parsed, err := url.Parse(origin)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			trustedOrigins[urlOrigin(parsed)] = struct{}{}
		}
	}

	return &trustedImageFetcher{
		heyOrigin:     parsedHEYOrigin,
		hey:           client,
		gopherOrigins: trustedOrigins,
		maxBytes:      maxInlineImageBytes,
		gopherClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 Gopher redirects")
				}
				if _, trusted := trustedOrigins[urlOrigin(request.URL)]; !trusted {
					return fmt.Errorf("gopher redirected outside its trusted origin")
				}
				return nil
			},
		},
	}
}

func (f *trustedImageFetcher) Fetch(ctx context.Context, source string, maxBytes int64) ([]byte, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid image URL: %w", errImageRefused, err)
	}
	limit := f.maxBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}

	if parsed.User != nil {
		return nil, fmt.Errorf("%w: image URL must not contain credentials", errImageRefused)
	}
	if !parsed.IsAbs() && parsed.Host != "" {
		return nil, fmt.Errorf("%w: image URL must be relative or use an explicit trusted origin", errImageRefused)
	}

	if !parsed.IsAbs() || sameURLOrigin(parsed, f.heyOrigin) {
		destination := &limitedImageBuffer{limit: limit}
		_, headers, downloadErr := f.hey.DownloadBlob(ctx, source, destination)
		if downloadErr != nil {
			if errors.Is(downloadErr, errImageTooLarge) {
				return nil, fmt.Errorf("HEY image exceeds the %d byte limit", limit)
			}
			return nil, downloadErr
		}
		data := destination.Bytes()
		if validationErr := validateImageData(data, headers); validationErr != nil {
			return nil, validationErr
		}
		return data, nil
	}

	if _, trusted := f.gopherOrigins[urlOrigin(parsed)]; !trusted {
		return nil, fmt.Errorf("%w: image URL is not from HEY or Gopher", errImageRefused)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", version.UserAgent())
	response, err := f.gopherClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gopher returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("gopher image exceeds the %d byte limit", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("gopher image exceeds the %d byte limit", limit)
	}
	if err := validateImageData(data, response.Header); err != nil {
		return nil, err
	}
	return data, nil
}

func validateImageData(data []byte, headers http.Header) error {
	if contentType := headers.Get("Content-Type"); contentType != "" {
		declaredType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return fmt.Errorf("image has an invalid media type: %w", err)
		}
		if !strings.HasPrefix(declaredType, "image/") && declaredType != "application/octet-stream" {
			return fmt.Errorf("image response has media type %s", declaredType)
		}
	}

	detectedType := http.DetectContentType(data)
	switch detectedType {
	case "image/gif", "image/jpeg", "image/png":
	default:
		return fmt.Errorf("response content is not a supported image: %s", detectedType)
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("response content is not a valid image")
	}
	if int64(config.Width)*int64(config.Height) > maxInlineImagePixels {
		return fmt.Errorf("image exceeds the %d pixel limit", maxInlineImagePixels)
	}
	return nil
}

var errImageTooLarge = errors.New("image exceeds size limit")

type limitedImageBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *limitedImageBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - int64(b.Len())
	if remaining <= 0 {
		return 0, errImageTooLarge
	}
	if int64(len(data)) > remaining {
		written, _ := b.Buffer.Write(data[:int(remaining)])
		return written, errImageTooLarge
	}
	return b.Buffer.Write(data)
}

func sameURLOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func urlOrigin(parsed *url.URL) string {
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}
