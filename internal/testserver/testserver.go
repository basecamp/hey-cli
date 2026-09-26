// Package testserver starts the HTTP servers tests stand in for HEY with. Only tests import
// it, so it is never linked into a binary.
package testserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// New starts a test server that answers with handler, and closes it when the test ends.
//
// A loopback port is not private to the test that opened it. Anything else on the machine
// can find it listening and ask what it is: moshi-hook, which the Moshi app runs over SSH
// to list dev servers, sends GET / to every loopback listener, once as 127.0.0.1 and once
// as localhost, with no credentials. A handler that fails its test on a request it did not
// expect, or counts the requests it gets, would blame the code under test for those. Nothing
// here reads HEY's root, so a GET / that carries no credentials is answered 404 and logged
// without reaching the handler; anything else, a write to / or an authenticated read of it
// included, still does. The longer a server is up the likelier such a visit, and a test
// waiting out the SDK's retry backoff is up for seconds.
func New(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isALookAtWhatIsListening(r) {
			t.Logf("ignored a look at what is listening: %s %s from %q", r.Method, r.URL, r.UserAgent())
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func isALookAtWhatIsListening(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/" &&
		r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == ""
}
