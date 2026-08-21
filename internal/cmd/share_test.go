package cmd

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestShareCommand(t *testing.T) {
	var requests atomic.Int32
	handler := sharingHandler(t, &requests, `{"published":true,"url":"https://public.hey.com/p/abc123"}`)

	response, err := runJSONCommand(t, handler, "share", "42")
	if err != nil {
		t.Fatalf("execute share: %v", err)
	}
	if requests.Load() != 2 {
		t.Errorf("requests = %d, want 2", requests.Load())
	}
	if response.Summary != "Sharing link turned on" {
		t.Errorf("summary = %q, want Sharing link turned on", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["published"] != true || data["url"] != "https://public.hey.com/p/abc123" {
		t.Errorf("data = %#v", response.Data)
	}
}

func TestShareCommandStyledOutputIncludesSharingLink(t *testing.T) {
	var requests atomic.Int32
	handler := sharingHandler(t, &requests, `{"published":true,"url":"https://public.hey.com/p/abc123"}`)

	output, err := runStyledCommand(t, handler, "share", "42")
	if err != nil {
		t.Fatalf("execute share: %v", err)
	}
	if output != "Sharing link: https://public.hey.com/p/abc123\n" {
		t.Errorf("output = %q", output)
	}
}

func TestUnshareCommand(t *testing.T) {
	var requests atomic.Int32
	handler := sharingHandler(t, &requests, "")

	response, err := runJSONCommand(t, handler, "unshare", "42")
	if err != nil {
		t.Fatalf("execute unshare: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
	if response.Summary != "Sharing link turned off" {
		t.Errorf("summary = %q, want Sharing link turned off", response.Summary)
	}
}

func TestSharingCommandValidationMakesNoRequest(t *testing.T) {
	for _, command := range []string{"share", "unshare"} {
		t.Run(command, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}), command, "not-a-thread")
			if err == nil || !strings.Contains(err.Error(), "invalid thread ID") {
				t.Fatalf("error = %v, want invalid thread ID", err)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestShareCommandRequiresSharingLink(t *testing.T) {
	var requests atomic.Int32
	handler := sharingHandler(t, &requests, `{"published":false}`)

	_, err := runJSONCommand(t, handler, "share", "42")
	if err == nil || !strings.Contains(err.Error(), "did not return a sharing link") {
		t.Fatalf("error = %v, want missing sharing link error", err)
	}
}

func sharingHandler(t *testing.T, requests *atomic.Int32, publication string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/topics/42/publication":
			if contentType := r.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
				t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", contentType)
			}
			w.Header().Set("Location", "/topics/42/publication/edit")
			w.WriteHeader(http.StatusSeeOther)
		case r.Method == http.MethodGet && r.URL.Path == "/topics/42/publication.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, publication)
		case r.Method == http.MethodDelete && r.URL.Path == "/topics/42/publication":
			w.Header().Set("Location", "/topics/42")
			w.WriteHeader(http.StatusSeeOther)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}
