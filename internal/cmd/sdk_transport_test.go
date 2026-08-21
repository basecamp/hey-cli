package cmd

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cappedClient caps text bodies at a kilobyte, which the tests overrun by a few.
func cappedClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: &cappedTransport{inner: http.DefaultTransport, limit: 1024}}
}

// get asks the way the SDK's generated client does, for JSON; getAccepting asks for
// whatever the caller names, the way a blob or an export does.
func get(t *testing.T, client *http.Client, url string) ([]byte, error) {
	t.Helper()
	return getAccepting(t, client, url, "application/json")
}

func getAccepting(t *testing.T, client *http.Client, url, accept string) ([]byte, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", accept)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func TestCappedTransportPassesABodyWithinTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"content":"short"}`)
	}))
	t.Cleanup(server.Close)

	body, err := get(t, cappedClient(t), server.URL)
	if err != nil || string(body) != `{"id":1,"content":"short"}` {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}

// A body past the limit ends in ErrResponseTooLarge before the parser has it all,
// whether the server declared its length or streamed it.
func TestCappedTransportStopsAnOversizedBody(t *testing.T) {
	for name, declare := range map[string]bool{"declared": true, "streamed": false} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if declare {
					w.Header().Set("Content-Length", "4096")
				}
				flusher, _ := w.(http.Flusher)
				for range 64 {
					_, _ = io.WriteString(w, strings.Repeat("x", 64))
					if flusher != nil && !declare {
						flusher.Flush()
					}
				}
			}))
			t.Cleanup(server.Close)

			body, err := get(t, cappedClient(t), server.URL)
			if !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("err = %v (body %d bytes), want ErrResponseTooLarge", err, len(body))
			}
			if len(body) > 1024 {
				t.Errorf("delivered %d bytes past a 1024-byte limit", len(body))
			}
		})
	}
}

// The limit counts decompressed bytes: a small gzip that inflates past it is refused.
func TestCappedTransportCountsDecompressedBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, `{"content":"`+strings.Repeat("a", 8192)+`"}`)
		_ = zw.Close()
	}))
	t.Cleanup(server.Close)

	_, err := get(t, cappedClient(t), server.URL)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge for an inflated body", err)
	}
}

func TestCappedTransportCapsErrorBodiesToo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"`+strings.Repeat("e", 4096)+`"}`)
	}))
	t.Cleanup(server.Close)

	_, err := get(t, cappedClient(t), server.URL)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge for an oversized error body", err)
	}
}

// A body of exactly the limit is read whole; one byte more is refused.
func TestCappedTransportAcceptsABodyExactlyAtTheLimit(t *testing.T) {
	for _, size := range []int{1024, 1025} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			flusher, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, strings.Repeat("x", size))
			if flusher != nil {
				flusher.Flush()
			}
		}))
		body, err := get(t, cappedClient(t), server.URL)
		server.Close()
		switch size {
		case 1024:
			if err != nil || len(body) != 1024 {
				t.Errorf("exactly at the limit: %d bytes, err = %v, want the whole body", len(body), err)
			}
		default:
			if !errors.Is(err, ErrResponseTooLarge) {
				t.Errorf("one past the limit: err = %v, want ErrResponseTooLarge", err)
			}
		}
	}
}

// What is capped is decided by the request. A blob the SDK asked for with */* streams
// whole whatever it turns out to be — a text file included — and a JSON answer the
// server labels as an image is capped all the same.
func TestCappedTransportDecidesByTheRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", r.URL.Query().Get("type"))
		_, _ = io.WriteString(w, strings.Repeat("%", 4096))
	}))
	t.Cleanup(server.Close)

	for _, test := range []struct {
		accept, contentType string
		capped              bool
	}{
		{"*/*", "application/pdf", false},
		{"*/*", "text/plain", false},
		{"*/*", "application/json", false},
		{"text/csv", "text/csv", false},
		{"application/json", "application/json", true},
		{"application/json", "image/png", true},
		{"application/json", "application/octet-stream", true},
		{"text/html", "text/html", true},
		{"", "application/json", true},
	} {
		body, err := getAccepting(t, cappedClient(t), server.URL+"?type="+test.contentType, test.accept)
		if capped := errors.Is(err, ErrResponseTooLarge); capped != test.capped {
			t.Errorf("Accept %q, Content-Type %q: capped = %v (err %v, %d bytes), want %v", test.accept, test.contentType, capped, err, len(body), test.capped)
		}
	}
}
