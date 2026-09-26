package cable

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-client/go/v2"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
)

func TestURL(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"https://app.hey.com", "wss://app.hey.com/cable"},
		{"https://app.hey.com/", "wss://app.hey.com/cable"},
		{"http://app.hey.localhost:3003", "ws://app.hey.localhost:3003/cable"},
	}

	for _, c := range cases {
		got, err := URL(c.baseURL)
		if err != nil {
			t.Fatalf("URL(%q) failed: %v", c.baseURL, err)
		}
		if got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.baseURL, got, c.want)
		}
	}
}

func TestURLOverride(t *testing.T) {
	t.Setenv("HEY_CABLE_URL", "ws://cable.example.com/cable")

	got, err := URL("https://app.hey.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ws://cable.example.com/cable" {
		t.Errorf("URL = %q, want the HEY_CABLE_URL override", got)
	}
}

func TestURLRejectsOtherSchemes(t *testing.T) {
	if _, err := URL("ftp://app.hey.com"); err == nil {
		t.Fatal("expected an error for a base URL that isn't http or https")
	}
}

// recordingTransport keeps the header of every dial and refuses to connect, which is all
// a test of what the upgrade request carries needs from it.
type recordingTransport struct {
	dialed chan struct{}

	mu      sync.Mutex
	headers []http.Header
}

func (t *recordingTransport) Dial(_ context.Context, _ string, options actioncable.DialOptions) (actioncable.Conn, error) {
	t.mu.Lock()
	t.headers = append(t.headers, options.Header)
	t.mu.Unlock()

	select {
	case t.dialed <- struct{}{}:
	default:
	}

	return nil, errors.New("no connection in this test")
}

func (t *recordingTransport) recorded() []http.Header {
	t.mu.Lock()
	defer t.mu.Unlock()

	return slices.Clone(t.headers)
}

func TestEveryDialCarriesCurrentCredentials(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "token-at-dial-time")

	recorded := &recordingTransport{dialed: make(chan struct{}, 1)}
	dialing, stopDialing := context.WithTimeout(t.Context(), 2*time.Second)
	defer stopDialing()

	go func() {
		<-recorded.dialed
		os.Setenv("HEY_TOKEN", "token-after-a-refresh")
	}()

	_, err := Dial(dialing, "https://app.hey.com", auth.NewManager("https://app.hey.com", http.DefaultClient, t.TempDir()),
		actioncable.WithTransport(recorded), actioncable.WithBackoff(time.Millisecond, time.Millisecond))
	if err == nil {
		t.Fatal("expected a client that never connects to give up when its context is done")
	}

	headers := recorded.recorded()
	if len(headers) < 2 {
		t.Fatalf("dials = %d, want the client to have redialed at least once", len(headers))
	}

	if got := headers[0].Get("Authorization"); got != "Bearer token-at-dial-time" {
		t.Errorf("first dial Authorization = %q", got)
	}

	redialed := slices.IndexFunc(headers[1:], func(header http.Header) bool {
		return header.Get("Authorization") == "Bearer token-after-a-refresh"
	})
	if redialed < 0 {
		t.Fatal("no redial carried the credentials as they are now")
	}
	if got := headers[1+redialed].Get("Origin"); got != "https://app.hey.com" {
		t.Errorf("redial Origin = %q, want the client's own headers kept", got)
	}

	// A dial that gave up leaves nothing behind it: the client stops itself rather
	// than retrying under a caller who has already been handed the error.
	dialsAtReturn := len(headers)
	time.Sleep(10 * time.Millisecond)
	if got := len(recorded.recorded()); got != dialsAtReturn {
		t.Errorf("dials after return = %d, want the %d attempts already made", got, dialsAtReturn)
	}
}

type droppingConn struct {
	reads   chan []byte
	dropped chan struct{}
	once    sync.Once
}

func newDroppingConn() *droppingConn {
	return &droppingConn{
		reads:   make(chan []byte, 1),
		dropped: make(chan struct{}),
	}
}

func (c *droppingConn) Subprotocol() string { return actioncable.V1JSON{}.Subprotocol() }

func (c *droppingConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case payload := <-c.reads:
		return payload, nil
	case <-c.dropped:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *droppingConn) Write(context.Context, []byte) error { return nil }

func (c *droppingConn) Close() error {
	c.once.Do(func() { close(c.dropped) })
	return nil
}

type singleConnTransport struct {
	conn *droppingConn

	mu    sync.Mutex
	dials int
}

func (t *singleConnTransport) Dial(context.Context, string, actioncable.DialOptions) (actioncable.Conn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dials++
	if t.dials > 1 {
		return nil, errors.New("unexpected second transport dial")
	}
	return t.conn, nil
}

func TestOnlyAuthenticationFailuresStopReconnects(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "authentication", err: apierr.ErrAuth("signed out"), want: true},
		{name: "rate limit", err: apierr.ErrRateLimit(30), want: false},
		{name: "network", err: apierr.ErrNetwork(io.EOF), want: false},
		{name: "storage", err: errors.New("keyring is locked"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalConnectionError(tt.err); got != tt.want {
				t.Errorf("terminalConnectionError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestAuthenticationFailureStopsAReconnect(t *testing.T) {
	refreshCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/tokens" {
			t.Errorf("path = %q, want /oauth/tokens", r.URL.Path)
		}
		refreshCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()

	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "")
	configDir := t.TempDir()
	manager := auth.NewManager(server.URL, server.Client(), configDir)
	if err := manager.GetStore().Save(manager.CredentialKey(), &auth.Credentials{
		AccessToken:  "working-access",
		RefreshToken: "dead-refresh",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("save working credential: %v", err)
	}

	conn := newDroppingConn()
	conn.reads <- []byte(`{"type":"welcome"}`)
	transport := &singleConnTransport{conn: conn}
	client, err := Dial(t.Context(), server.URL, manager,
		actioncable.WithTransport(transport),
		actioncable.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	replacement := auth.NewManager(server.URL, server.Client(), configDir)
	if err := replacement.GetStore().Save(replacement.CredentialKey(), &auth.Credentials{
		AccessToken:  "expired-access",
		RefreshToken: "dead-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("save expired credential: %v", err)
	}
	_ = conn.Close()

	select {
	case <-client.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client kept reconnecting after authentication failed")
	}
	var authErr *apierr.Error
	if err := client.Err(); !errors.As(err, &authErr) || authErr.Code != apierr.CodeAuth {
		t.Fatalf("client error = %v, want authentication failure", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refresh requests = %d, want 1", refreshCalls)
	}
}

func TestDialHeaderRereadsStoredCredentials(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "")

	configDir := t.TempDir()
	manager := auth.NewManager("https://app.hey.com", http.DefaultClient, configDir)
	if err := manager.LoginWithCookie("cookie-at-first-dial"); err != nil {
		t.Fatalf("store first cookie: %v", err)
	}

	first, err := authHeader(t.Context(), "https://app.hey.com", manager)
	if err != nil {
		t.Fatalf("first dial header: %v", err)
	}
	if got := first.Get("Cookie"); got != "session_token=cookie-at-first-dial" {
		t.Errorf("first dial Cookie = %q", got)
	}

	replacement := auth.NewManager("https://app.hey.com", http.DefaultClient, configDir)
	if err := replacement.LoginWithCookie("cookie-at-redial"); err != nil {
		t.Fatalf("replace stored cookie: %v", err)
	}

	redial, err := authHeader(t.Context(), "https://app.hey.com", manager)
	if err != nil {
		t.Fatalf("redial header: %v", err)
	}
	if got := redial.Get("Cookie"); got != "session_token=cookie-at-redial" {
		t.Errorf("redial Cookie = %q, want the replacement from storage", got)
	}

	if err := replacement.Logout(); err != nil {
		t.Fatalf("delete stored cookie: %v", err)
	}
	if _, err := authHeader(t.Context(), "https://app.hey.com", manager); err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("dial after deletion error = %v, want not authenticated", err)
	}
}

func TestDialWithoutCredentialsSaysSo(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "")

	recorded := &recordingTransport{dialed: make(chan struct{}, 1)}
	dialing, stopDialing := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer stopDialing()

	_, err := Dial(dialing, "https://app.hey.com", auth.NewManager("https://app.hey.com", http.DefaultClient, t.TempDir()),
		actioncable.WithTransport(recorded), actioncable.WithBackoff(time.Millisecond, time.Millisecond))
	if err == nil {
		t.Fatal("expected a dial with no credentials to fail")
	}
	// The upgrade request is never built without credentials, so the reason has to
	// come back with the error rather than being retried away out of sight.
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("error = %q, want it to name the credentials that could not be built", err.Error())
	}
	if dials := len(recorded.recorded()); dials != 0 {
		t.Errorf("dials = %d, want no upgrade request attempted without credentials", dials)
	}
}
