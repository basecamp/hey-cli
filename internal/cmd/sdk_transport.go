package cmd

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

// The SDK's generated parsers read every response body with io.ReadAll before
// anything here can apply a budget, so a server answering one entries page or one
// message with gigabytes was a memory exhaustion. Until the SDK bounds its own reads
// (basecamp/hey-cli#248), the bound lives in the transport the SDK is handed: a
// RoundTripper that caps what a text-bearing response can deliver, success and error
// alike, before a parser sees it.
//
// It sits inside the SDK's own http.Client rather than replacing it, so the SDK's
// timeout, its redirect credential stripping, its logging and its hooks all still
// apply. It reads decompressed bytes: the transport it wraps is the one that
// negotiated the encoding, and what comes out of it is what the parser would keep.
//
// Only JSON and text responses are capped. A blob is either streamed by the SDK to a
// destination of any size (DownloadBlob) or buffered under the SDK's own
// MaxResponseBodyBytes (GetBlob), and neither goes through a generated parser.

// maxTextResponseBytes is the most a JSON or text response may deliver: 16 MiB, which
// is a message with a very large HTML body several times over.
const maxTextResponseBytes int64 = 16 << 20

// ErrResponseTooLarge is the error a capped body ends with once it passes the cap.
var ErrResponseTooLarge = errors.New("response body exceeded the size limit")

// cappedTransport wraps an http.RoundTripper so that text-bearing responses cannot
// deliver more than limit decompressed bytes.
type cappedTransport struct {
	inner http.RoundTripper
	limit int64
}

func newCappedTransport(limit int64) *cappedTransport {
	inner := http.DefaultTransport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		// The same pooling the SDK's own default transport sets.
		transport := base.Clone()
		transport.MaxIdleConns = 100
		transport.MaxIdleConnsPerHost = 10
		transport.IdleConnTimeout = 90 * time.Second
		inner = transport
	}
	return &cappedTransport{inner: inner, limit: limit}
}

func (t *cappedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || !isTextResponse(resp) {
		return resp, err
	}
	if resp.ContentLength > t.limit {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s %s: declared %d bytes: %w", req.Method, req.URL.Path, resp.ContentLength, ErrResponseTooLarge)
	}
	resp.Body = &cappedBody{ReadCloser: resp.Body, remaining: t.limit, request: req}
	return resp, nil
}

// isTextResponse reports a body a generated parser would buffer: JSON, or any text.
// A response without a content type is treated as text, since the SDK parses it.
func isTextResponse(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") || strings.HasPrefix(mediaType, "text/")
}

type cappedBody struct {
	io.ReadCloser
	remaining int64
	request   *http.Request
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, fmt.Errorf("%s %s: %w", b.request.Method, b.request.URL.Path, ErrResponseTooLarge)
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if int64(n) > b.remaining {
		n = int(b.remaining)
		b.remaining = 0
		return n, fmt.Errorf("%s %s: %w", b.request.Method, b.request.URL.Path, ErrResponseTooLarge)
	}
	b.remaining -= int64(n)
	return n, err
}
