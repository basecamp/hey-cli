package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/version"
)

// newCLIServer starts a test server for the CLI to talk to, and closes it when the test ends.
//
// A loopback port is not private to the test that opened it. Anything else on the machine
// can find it listening and ask what it is: moshi-hook, which the Moshi app runs over SSH
// to list dev servers, sends GET / to every loopback listener, once as 127.0.0.1 and once
// as localhost. A handler that fails its test on a request it did not expect would blame
// the CLI for those. The CLI never asks for the root, so a GET / that does not carry the
// CLI's user agent is answered 404 and logged without reaching the handler; anything else,
// a write to / included, still does. The longer a server is up the likelier such a visit,
// and a test waiting out the SDK's retry backoff is up for seconds.
func newCLIServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" && !strings.HasPrefix(r.UserAgent(), version.UserAgent()) {
			t.Logf("ignored a look at what is listening: %s %s from %q", r.Method, r.URL, r.UserAgent())
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// A look at what is listening is answered without the handler seeing it; everything the CLI
// sends, a write to / from anyone, and anything else for a real path, still reaches it.
func TestCLIServerKeepsALookAtWhatIsListeningFromTheHandler(t *testing.T) {
	var reached []string
	server := newCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Method+" "+r.URL.Path+" "+r.UserAgent())
	}))

	send := func(method, path, userAgent string) int {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("User-Agent", userAgent)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}

	if got := send(http.MethodGet, "/", "Go-http-client/1.1"); got != http.StatusNotFound {
		t.Errorf("a look at / answered %d, want 404", got)
	}
	send(http.MethodGet, "/", version.UserAgent()+" hey-sdk-go")
	send(http.MethodPost, "/", "Go-http-client/1.1")
	send(http.MethodPut, "/storage/upload", "Go-http-client/1.1")

	want := []string{
		"GET / " + version.UserAgent() + " hey-sdk-go",
		"POST / Go-http-client/1.1",
		"PUT /storage/upload Go-http-client/1.1",
	}
	if strings.Join(reached, "\n") != strings.Join(want, "\n") {
		t.Errorf("handler saw %q, want %q", reached, want)
	}
}
