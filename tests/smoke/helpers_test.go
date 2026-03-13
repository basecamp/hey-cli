package smoke_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Shared test state set up once in TestMain.
var (
	binaryPath    string
	baseURL       string
	configDir     string
	sessionCookie string
)

// Response mirrors the CLI's JSON envelope.
type Response struct {
	OK          bool            `json:"ok"`
	Data        json.RawMessage `json:"data,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Notice      string          `json:"notice,omitempty"`
	Breadcrumbs []Breadcrumb    `json:"breadcrumbs,omitempty"`
	Meta        map[string]any  `json:"meta,omitempty"`
}

type ErrorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Code  string `json:"code"`
	Hint  string `json:"hint,omitempty"`
}

type Breadcrumb struct {
	Action      string `json:"action"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

func TestMain(m *testing.M) {
	baseURL = envOr("HEY_SMOKE_BASE_URL", "http://app.hey.localhost:3003")
	email := envOr("HEY_SMOKE_EMAIL", "david@basecamp.com")
	password := envOr("HEY_SMOKE_PASSWORD", "secret123456")

	// Locate the pre-built binary.
	root := findProjectRoot()
	binaryPath = envOr("HEY_BINARY", filepath.Join(root, "bin", "hey"))
	if _, err := os.Stat(binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "CLI binary not found at %s — run 'make build' first\n", binaryPath)
		os.Exit(1)
	}

	// Check that the server is reachable.
	if !serverReachable(baseURL) {
		fmt.Fprintf(os.Stderr, "Server not reachable at %s — start the dev server first\n", baseURL)
		os.Exit(1)
	}

	// Create an isolated config directory so tests don't touch the real config.
	var err error
	configDir, err = os.MkdirTemp("", "hey-smoke-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create temp config dir: %v\n", err)
		os.Exit(1)
	}
	// Launch headless Chrome browser and log in to obtain a session cookie.
	sessionCookie, err = browserLogin(baseURL, email, password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Browser login failed: %v\n", err)
		os.Exit(1)
	}

	// Authenticate the CLI with the obtained session cookie.
	if err := setupCLI(sessionCookie); err != nil {
		fmt.Fprintf(os.Stderr, "CLI setup failed: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(configDir)
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// CLI runner helpers
// ---------------------------------------------------------------------------

// hey runs the CLI binary with the given arguments and returns stdout, stderr,
// and the exit code.
func hey(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = cliEnv()
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("failed to run hey: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// heyOK runs the CLI expecting exit code 0 and returns stdout.
func heyOK(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		t.Fatalf("hey %s failed (exit %d):\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

// heyJSON runs the CLI with --json, expects success, and returns the parsed
// Response envelope.
func heyJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout := heyOK(t, args...)
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw: %s", err, stdout)
	}
	if !resp.OK {
		t.Fatalf("hey %s returned ok=false: %s", strings.Join(args, " "), stdout)
	}
	return resp
}

// heyFail runs the CLI and expects a non-zero exit code.
func heyFail(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	out, errOut, code := hey(t, args...)
	if code == 0 {
		t.Fatalf("expected hey %s to fail, but it succeeded:\nstdout: %s",
			strings.Join(args, " "), out)
	}
	return out, errOut
}

// dataAs unmarshals the Data field from a Response into the given type.
func dataAs[T any](t *testing.T, resp Response) T {
	t.Helper()
	var v T
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return v
	}
	if err := json.Unmarshal(resp.Data, &v); err != nil {
		t.Fatalf("failed to unmarshal data as %T: %v\nraw: %s", v, err, string(resp.Data))
	}
	return v
}

// cliEnv returns the environment variables used for all CLI invocations.
func cliEnv() []string {
	env := os.Environ()
	env = append(env,
		"HEY_BASE_URL="+baseURL,
		"XDG_CONFIG_HOME="+configDir,
		"HEY_NO_KEYRING=1",
		"NO_COLOR=1",
		"TERM=dumb",
	)
	return env
}

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func findProjectRoot() string {
	dir, _ := os.Getwd()
	for {
		gomod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			data, _ := os.ReadFile(gomod)
			// Match the root module exactly, not sub-modules like tests/smoke.
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "module github.com/basecamp/hey-cli" {
					return dir
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback: assume two directories up from tests/smoke.
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..")
}

func serverReachable(base string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/up")
	if err != nil {
		// Try the base URL directly as a fallback.
		resp, err = client.Get(base)
		if err != nil {
			return false
		}
	}
	resp.Body.Close()
	return true
}

func setupCLI(cookie string) error {
	cmd := exec.Command(binaryPath, "auth", "login", "--cookie", cookie)
	cmd.Env = cliEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hey auth login --cookie failed: %v\noutput: %s", err, out)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Browser helpers (chromedp)
// ---------------------------------------------------------------------------

func newBrowserContext() (context.Context, context.CancelFunc, context.CancelFunc) {
	headless := os.Getenv("HEADLESS") != "0"
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-features", "TranslateUI,ServiceWorker"),
		chromedp.Flag("js-flags", "--max-old-space-size=512"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	return ctx, ctxCancel, allocCancel
}

// browserLogin launches a temporary headless Chrome, logs in, and extracts the
// session_token cookie value.
func browserLogin(base, email, password string) (string, error) {
	ctx, ctxCancel, allocCancel := newBrowserContext()
	defer ctxCancel()
	defer allocCancel()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Pre-check: make sure the sign-in page is actually servable.
	preCheck := &http.Client{Timeout: 5 * time.Second}
	preResp, preErr := preCheck.Get(base + "/sign_in")
	if preErr != nil {
		return "", fmt.Errorf("sign-in page unreachable: %w", preErr)
	}
	preResp.Body.Close()
	if preResp.StatusCode >= 500 {
		return "", fmt.Errorf("sign-in page returned HTTP %d — is the server healthy?", preResp.StatusCode)
	}

	var cookies []*network.Cookie
	err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/sign_in"),
		chromedp.WaitVisible(`input[name="email_address"]`, chromedp.ByQuery),
		chromedp.Clear(`input[name="email_address"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="email_address"]`, email, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="password"]`, password, chromedp.ByQuery),
		chromedp.Click(`input[type="submit"]`, chromedp.ByQuery),
		// Wait for login redirect. The sign-in page layout also has main#main-content,
		// so we poll until the URL changes away from /sign_in.
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 30; i++ {
				time.Sleep(500 * time.Millisecond)
				var loc string
				if err := chromedp.Location(&loc).Do(ctx); err != nil {
					return err
				}
				if !strings.Contains(loc, "/sign_in") {
					return nil
				}
			}
			return fmt.Errorf("login did not redirect from /sign_in within 15s")
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().Do(ctx)
			return err
		}),
	)
	if err != nil {
		return "", fmt.Errorf("browser login flow: %w", err)
	}

	for _, c := range cookies {
		if c.Name == "session_token" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("session_token cookie not found after login")
}

// browserLoginCtx logs in inside an existing chromedp context.
func browserLoginCtx(ctx context.Context, base, email, password string) error {
	tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return chromedp.Run(tCtx,
		chromedp.Navigate(base+"/sign_in"),
		chromedp.WaitVisible(`input[name="email_address"]`, chromedp.ByQuery),
		chromedp.Clear(`input[name="email_address"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="email_address"]`, email, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="password"]`, password, chromedp.ByQuery),
		chromedp.Click(`input[type="submit"]`, chromedp.ByQuery),
		// Wait for login redirect. The sign-in page layout also has main#main-content,
		// so we poll until the URL changes away from /sign_in.
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 30; i++ {
				time.Sleep(500 * time.Millisecond)
				var loc string
				if err := chromedp.Location(&loc).Do(ctx); err != nil {
					return err
				}
				if !strings.Contains(loc, "/sign_in") {
					return nil
				}
			}
			return fmt.Errorf("login did not redirect from /sign_in within 15s")
		}),
	)
}

// browserPageText navigates to the given URL and returns the page's innerText.
// It creates a fresh browser context and sets the session cookie directly.
func browserPageText(t *testing.T, pageURL string) string {
	t.Helper()

	ctx, ctxCancel, allocCancel := newBrowserContext()
	defer ctxCancel()
	defer allocCancel()

	tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Set session cookie directly instead of doing a full login flow.
	// This avoids loading the heavy post-login homepage which can crash Chrome.
	parsed, _ := url.Parse(baseURL)
	cookieDomain := parsed.Hostname()
	if err := chromedp.Run(tCtx,
		network.SetCookie("session_token", sessionCookie).
			WithDomain(cookieDomain).
			WithPath("/").
			WithHTTPOnly(true),
	); err != nil {
		t.Fatalf("failed to set session cookie: %v", err)
	}

	var text string
	if err := chromedp.Run(tCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.Text(`body`, &text, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser navigate to %s failed: %v", pageURL, err)
	}
	return text
}

// ---------------------------------------------------------------------------
// Server-side verification helpers
// ---------------------------------------------------------------------------

// fetchHTML fetches a page via HTTP using the session cookie and returns the
// raw HTML body. This is a lightweight alternative to browserPageText that
// avoids launching Chrome — suitable for cross-verifying server state after
// CLI mutations.
func fetchHTML(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("could not create request for %s: %v", url, err)
	}
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("GET %s returned HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("could not read body from %s: %v", url, err)
	}
	return string(body)
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q, got:\n%s", needle, truncOutput(haystack, 500))
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected output NOT to contain %q, got:\n%s", needle, truncOutput(haystack, 500))
	}
}

func truncOutput(s string, max int) string {
	if len(s) > max {
		return s[:max] + "... (truncated)"
	}
	return s
}

// uniqueID returns a unique string suitable for identifying test-created data.
func uniqueID() string {
	return fmt.Sprintf("inttest-%d", time.Now().UnixNano())
}
