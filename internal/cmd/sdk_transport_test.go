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

func get(t *testing.T, client *http.Client, url string) ([]byte, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
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

// A blob is not a parser's body: the SDK streams it to a destination of any size, so
// the cap leaves it alone.
func TestCappedTransportLeavesBlobsAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, strings.Repeat("%", 4096))
	}))
	t.Cleanup(server.Close)

	body, err := get(t, cappedClient(t), server.URL)
	if err != nil || len(body) != 4096 {
		t.Fatalf("blob = %d bytes, err = %v, want the whole blob", len(body), err)
	}
}

func TestIsTextResponse(t *testing.T) {
	for contentType, want := range map[string]bool{
		"application/json":                true,
		"application/json; charset=utf-8": true,
		"application/problem+json":        true,
		"text/html":                       true,
		"text/plain; charset=utf-8":       true,
		"":                                true,
		"not a type":                      true,
		"image/png":                       false,
		"application/pdf":                 false,
		"application/octet-stream":        false,
	} {
		resp := &http.Response{Header: http.Header{}}
		if contentType != "" {
			resp.Header.Set("Content-Type", contentType)
		}
		if got := isTextResponse(resp); got != want {
			t.Errorf("isTextResponse(%q) = %v, want %v", contentType, got, want)
		}
	}
}
