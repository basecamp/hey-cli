package cable

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	actioncable "github.com/basecamp/actioncable-go"

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

	// Connect's deadline only bounds its wait; Dial owns and stops a client that never
	// connected so no retry goroutine is left behind after the error.
	dialsAtReturn := len(headers)
	time.Sleep(10 * time.Millisecond)
	if got := len(recorded.recorded()); got != dialsAtReturn {
		t.Errorf("dials after return = %d, want the %d attempts already made", got, dialsAtReturn)
	}
}

func TestDialWithoutCredentialsFailsBeforeConnecting(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "")

	_, err := Dial(t.Context(), "https://app.hey.com", auth.NewManager("https://app.hey.com", http.DefaultClient, t.TempDir()))
	if err == nil {
		t.Fatal("expected a dial with no credentials to fail")
	}
}
