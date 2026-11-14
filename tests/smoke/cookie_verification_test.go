//go:build verification

package smoke_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This opt-in check needs a disposable development clearance. It compares the
// rejected browser-cookie write with the same decision through the CLI's OAuth
// grant, then reads the result independently in the browser.
func TestCookieRemovalLiveScreener(t *testing.T) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Hostname() != "app.hey.localhost" {
		t.Fatal("this verification requires the local HTTPS development server")
	}
	clearance := os.Getenv("HEY_VERIFY_CLEARANCE_ID")
	contact := os.Getenv("HEY_VERIFY_CONTACT_ID")
	for _, id := range []string{clearance, contact} {
		if parsed, err := strconv.ParseInt(id, 10, 64); err != nil || parsed <= 0 {
			t.Fatal("provide disposable HEY_VERIFY_CLEARANCE_ID and HEY_VERIFY_CONTACT_ID fixtures")
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, baseURL+"/clearances/"+clearance+".json", strings.NewReader(`{"status":"approved"}`))
	if err != nil {
		t.Fatal("could not build negative control")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal("HTTPS negative control failed to connect")
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie-only HTTPS write status = %d, want 403", response.StatusCode)
	}
	for _, decision := range []string{"approve", "deny", "approve"} {
		result := dataAs[[]struct {
			Status string `json:"status"`
		}](t, heyJSON(t, "screener", decision, clearance))
		want := "approved"
		if decision == "deny" {
			want = "denied"
		}
		if len(result) != 1 || result[0].Status != want {
			t.Fatalf("%s did not return one %s decision", decision, want)
		}
		page := browserPageText(t, baseURL+"/contacts/"+contact)
		screenedOut := strings.Contains(page, "is screened out")
		if !strings.Contains(page, "Morgan Shaw") || screenedOut != (decision == "deny") {
			t.Fatalf("browser did not independently reflect %s", decision)
		}
	}
}

// Establishing the real subscription verifies bearer authentication on Action
// Cable. Header rotation and reconnect behavior are covered by internal/cable;
// this check does not claim that a mailbox change has propagated end to end.
func TestCookieRemovalLiveWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "watch", "--timeout", "8s")
	cmd.Env = cliEnv()
	cmd.Dir = workDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	runErr := cmd.Run() // A timeout after readiness is the expected end of this check.
	for _, line := range strings.Split(stdout.String(), "\n") {
		var event struct {
			Change string `json:"change"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Change == "ready" {
			return
		}
	}
	// Report the process result without dumping server-supplied stderr.
	t.Fatalf("watch never established its subscription (process: %v; context: %v)", runErr, ctx.Err())
}
