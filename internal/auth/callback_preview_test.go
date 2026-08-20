package auth

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

// TestPreviewCallbackPages serves the callback pages for visual review:
//
//	make preview-callback
//
// or directly:
//
//	PREVIEW=1 go test -run TestPreviewCallbackPages ./internal/auth/ -count=1 -v
//
// Then open http://127.0.0.1:9999 in your browser. Pages re-render from the
// HTML files on disk on every request, so edit a template and refresh the
// browser to see the change — no restart needed. Ctrl-C to stop.
func TestPreviewCallbackPages(t *testing.T) {
	if os.Getenv("PREVIEW") == "" {
		t.Skip("set PREVIEW=1 to run this preview server")
	}

	pages := []struct {
		path, label, filename string
	}{
		{"/success", "Success", "callback_success.html"},
		{"/error", "Error", "callback_error.html"},
		{"/denied", "Denied", "callback_denied.html"},
		{"/invalid", "Invalid / expired", "callback_invalid.html"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><head><style>
  body { font-family: system-ui; max-width: 400px; margin: 80px auto; }
  a { display: block; padding: 12px 0; font-size: 18px; }
</style></head><body>
  <h2>Callback page previews</h2>`)
		for _, p := range pages {
			fmt.Fprintf(w, `  <a href="%s">%s</a>`+"\n", p.path, p.label)
		}
		fmt.Fprint(w, `</body></html>`)
	})
	for _, p := range pages {
		mux.HandleFunc(p.path, func(w http.ResponseWriter, r *http.Request) {
			// Render from disk, not the embedded copy, so edits show
			// up on refresh while the server keeps running.
			page, err := renderCallback(os.DirFS("."), p.filename)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			fmt.Fprint(w, page)
		})
	}

	t.Log("Preview server running at http://127.0.0.1:9999")
	for _, p := range pages {
		t.Logf("  http://127.0.0.1:9999%s", p.path)
	}
	t.Log("Edit the callback_*.html files and refresh the browser. Ctrl-C to stop.")

	server := &http.Server{Addr: "127.0.0.1:9999", Handler: mux} //nolint:gosec // G112: local preview server, timeouts irrelevant
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
