package cmd

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/basecamp/hey-cli/internal/threadload"
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
// A success body past the limit is refused — its first read past the cap fails — since
// a parser that buffers it would buffer it whole. An error body past the limit is cut
// off at the cap instead: the status is what matters about an error, and a refusal
// would hide it behind a read failure. Which responses are capped is decided by the
// request, not by what the server says it answered with: the SDK asks for application/json where a generated parser will
// buffer the answer and for text/html where GetHTML will, and asks for */* for a blob,
// which it streams to a destination of any size (DownloadBlob) or buffers under its
// own MaxResponseBodyBytes (GetBlob). A server that labels a JSON answer as a PNG is
// still capped; an attachment that happens to be a text file is still streamed.

// maxTextResponseBytes is the most a JSON or text response may deliver: 16 MiB, which
// is a message with a very large HTML body several times over.
const maxTextResponseBytes int64 = 16 << 20

// ErrResponseTooLarge is the error a capped body ends with once it passes the cap. It
// wraps threadload.ErrOverLimit, so a loader that meets it through the SDK knows the
// one message was too large rather than the service failing.
var ErrResponseTooLarge = fmt.Errorf("%w: response body exceeded the size limit", threadload.ErrOverLimit)

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
	if err != nil || resp == nil || resp.Body == nil || !isParsedRequest(req) {
		return resp, err
	}
	// A body declared past the limit is refused on its first read rather than at the
	// round trip: a round-trip error is one the SDK retries, and the body would be too
	// large again; a read error is the same failure the streamed case produces.
	if resp.StatusCode >= 400 {
		resp.Body = &truncatedBody{Reader: io.LimitReader(resp.Body, t.limit), closer: resp.Body}
		return resp, nil
	}
	remaining := t.limit
	if resp.ContentLength > t.limit {
		remaining = -1
	}
	resp.Body = &cappedBody{ReadCloser: resp.Body, remaining: remaining, request: req}
	return resp, nil
}

// isParsedRequest reports a request whose answer the SDK buffers and parses, by what
// the request asked for. Anything it did not ask for as JSON or HTML — a blob's */*, an
// export's text/csv — it handles by streaming or under its own bound.
func isParsedRequest(req *http.Request) bool {
	accept := req.Header.Get("Accept")
	if accept == "" {
		return true
	}
	for _, part := range strings.Split(accept, ",") {
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") || mediaType == "text/html" {
			return true
		}
	}
	return false
}

// truncatedBody is an error body cut off at the cap: what the server said, up to the
// limit, with the status still standing.
type truncatedBody struct {
	io.Reader
	closer io.Closer
}

func (b *truncatedBody) Close() error { return b.closer.Close() }

type cappedBody struct {
	io.ReadCloser
	remaining int64
	request   *http.Request
}

// Read delivers up to the limit and fails on the first byte past it. A body that is
// exactly the limit is read whole: with nothing remaining, the next read still asks the
// wrapped body for one byte, and gets its EOF rather than a refusal. A body declared
// past the limit starts with a negative remainder and fails on its first read.
func (b *cappedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining < 0 {
		return 0, fmt.Errorf("%s %s: declared past the limit: %w", b.request.Method, b.request.URL.Path, ErrResponseTooLarge)
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
