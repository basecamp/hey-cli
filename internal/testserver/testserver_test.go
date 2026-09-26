package testserver

import (
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A look at what is listening is answered without the handler seeing it; an authenticated
// read of /, a write to / and anything for a real path still reach it.
func TestNewKeepsALookAtWhatIsListeningFromTheHandler(t *testing.T) {
	var mu sync.Mutex
	var reached []string
	server := New(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reached = append(reached, r.Method+" "+r.URL.Path)
	}))

	send := func(method, path string, header http.Header) int {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, values := range header {
			request.Header[name] = values
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}

	if got := send(http.MethodGet, "/", nil); got != http.StatusNotFound {
		t.Errorf("a look at / answered %d, want 404", got)
	}
	send(http.MethodGet, "/", http.Header{"Authorization": {"Bearer hey-token"}})
	send(http.MethodGet, "/", http.Header{"Cookie": {"session_token=hey-session"}})
	send(http.MethodPost, "/", nil)
	send(http.MethodGet, "/boxes.json", nil)

	want := []string{"GET /", "GET /", "POST /", "GET /boxes.json"}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(reached, "\n") != strings.Join(want, "\n") {
		t.Errorf("handler saw %q, want %q", reached, want)
	}
}
