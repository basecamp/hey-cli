package smoke_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestShareAndUnshare(t *testing.T) {
	subject := fmt.Sprintf("Sharing link smoke test %s", uniqueID())
	stdout, stderr, code := hey(t, "compose",
		"--to", "alex@example.com",
		"--subject", subject,
		"-m", "This controlled thread verifies sharing link management.",
		"--json",
	)
	if code != 0 {
		t.Skipf("could not create a controlled thread (exit %d): %s", code, stderr)
	}

	var composeResp Response
	if err := json.Unmarshal([]byte(stdout), &composeResp); err != nil {
		t.Fatalf("failed to parse compose response: %v", err)
	}

	threadID, err := findSentThread(subject)
	if err != nil {
		t.Fatalf("could not find controlled thread: %v", err)
	}
	needsCleanup := false
	cleanupMustSucceed := false
	t.Cleanup(func() {
		if !needsCleanup {
			return
		}
		_, cleanupErr, cleanupCode := hey(t, "unshare", threadID, "--json")
		if cleanupCode != 0 && cleanupMustSucceed {
			t.Errorf("could not turn off controlled sharing link (exit %d): %s", cleanupCode, cleanupErr)
		}
	})

	// The SDK publishes first and reads the link back second. Enable cleanup before
	// running the command so a failed readback cannot leave the controlled thread shared.
	needsCleanup = true
	stdout, stderr, code = hey(t, "share", threadID, "--json")
	if code != 0 {
		t.Skipf("sharing links are unavailable on this server (exit %d): %s", code, stderr)
	}
	cleanupMustSucceed = true
	var shareResp Response
	if err := json.Unmarshal([]byte(stdout), &shareResp); err != nil {
		t.Fatalf("failed to parse share response: %v", err)
	}
	sharing := dataAs[struct {
		Published bool   `json:"published"`
		URL       string `json:"url"`
	}](t, shareResp)
	if !sharing.Published || sharing.URL == "" {
		t.Errorf("sharing = %+v, want a sharing link", sharing)
	}

	stdout, stderr, code = hey(t, "unshare", threadID, "--json")
	if code != 0 {
		t.Fatalf("turning off the sharing link failed (exit %d): %s", code, stderr)
	}
	needsCleanup = false
	var unshareResp Response
	if err := json.Unmarshal([]byte(stdout), &unshareResp); err != nil {
		t.Fatalf("failed to parse unshare response: %v", err)
	}
	if unshareResp.Summary != "Sharing link turned off" {
		t.Errorf("summary = %q, want Sharing link turned off", unshareResp.Summary)
	}
}

func TestSharingCommandsRequireThreadID(t *testing.T) {
	heyFail(t, "share", "--json")
	heyFail(t, "unshare", "--json")
}

func findSentThread(subject string) (string, error) {
	for range 10 {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/topics/sent.json", nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		var sent struct {
			Topics []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"topics"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&sent)
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("GET /topics/sent.json returned HTTP %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return "", decodeErr
		}
		for _, topic := range sent.Topics {
			if topic.Name == subject {
				return fmt.Sprint(topic.ID), nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("sent thread %q did not appear", subject)
}
