package smoke_test

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	stateDir      string
	homeDir       string
	cacheDir      string
	workDir       string
	sessionCookie string
	smokeEmail    string
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
	os.Exit(runSmoke(m.Run))
}

func runSmoke(run func() int) int {
	if os.Getenv("HEY_SMOKE_OFFLINE") == "1" {
		flag.Parse()
		if flag.Lookup("test.run").Value.String() != "^TestHarness" {
			fmt.Fprintln(os.Stderr, "offline mode only permits -run '^TestHarness'; it is not live verification")
			return 1
		}
		fmt.Println("Offline smoke-harness regressions only; no live HEY verification")
		return run()
	}
	baseURL = envOr("HEY_SMOKE_BASE_URL", "http://app.hey.localhost:3003")
	smokeEmail = envOr("HEY_SMOKE_EMAIL", "david@basecamp.com")
	password := envOr("HEY_SMOKE_PASSWORD", "secret123456")
	var err error
	binaryPath, err = filepath.Abs(envOr("HEY_BINARY", filepath.Join(findProjectRoot(), "bin", "hey")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve CLI binary: %v\n", err)
		return 1
	}
	if _, err := os.Stat(binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "CLI binary not found at %s — run 'make build' first\n", binaryPath)
		return 1
	}
	if !serverReachable(baseURL) {
		fmt.Fprintf(os.Stderr, "Server not reachable at %s — start the dev server first\n", baseURL)
		return 1
	}
	return withSmokeState(func() int {
		// Cookies stay in the browser; the CLI gets an actual OAuth grant.
		var err error
		sessionCookie, err = browserLogin(baseURL, smokeEmail, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Browser login failed: %v\n", err)
			return 1
		}
		if err := setupCLI("auth", "login"); err != nil {
			fmt.Fprintf(os.Stderr, "CLI setup failed: %v\n", err)
			return 1
		}
		// All cleanup callbacks/readbacks finish before m.Run returns.
		return run()
	})
}

// withSmokeState also cleans partial setup failures. No CLI state goes under
// the developer's HOME, cache or working directory. Smoke tests share these
// package globals and a backend identity; they must not use t.Parallel.
func withSmokeState(run func() int) (code int) {
	previous := []string{configDir, stateDir, homeDir, cacheDir, workDir}
	defer func() {
		configDir, stateDir, homeDir, cacheDir, workDir = previous[0], previous[1], previous[2], previous[3], previous[4]
	}()
	code = 1
	scratch, err := os.MkdirTemp("", "hey-smoke-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create smoke state: %v\n", err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(scratch); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove owned smoke state %s: %v\n", scratch, err)
			code = 1
		}
	}()
	configDir, stateDir, homeDir = filepath.Join(scratch, "config"), filepath.Join(scratch, "state"), filepath.Join(scratch, "home")
	cacheDir, workDir = filepath.Join(scratch, "cache"), filepath.Join(scratch, "work")
	for _, path := range []string{configDir, stateDir, homeDir, cacheDir, workDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create smoke state directory: %v\n", err)
			return 1
		}
	}
	return run()
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
	cmd.Dir = workDir
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
	if err := recordSmokeCommand(t.Name(), args, outBuf.String(), errBuf.String(), exitCode); err != nil {
		t.Errorf("record command evidence: %v", err)
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

// Only the harness supplies HEY settings to child CLIs. Caller defaults must
// not replace its OAuth grant, account, origin or cable endpoint. Harness
// options stay in the parent; HEY_SMOKE_ACCOUNT_ID explicitly scopes the child.
// HOME and OMARCHY_PATH also prevent desktop/agent setup from reaching the
// developer's session.
func cliEnv() []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HEY_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "OMARCHY_PATH=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HEY_BASE_URL="+baseURL,
		"HEY_ACCOUNT_ID="+os.Getenv("HEY_SMOKE_ACCOUNT_ID"),
		"HOME="+homeDir,
		"OMARCHY_PATH=",
		"XDG_CONFIG_HOME="+configDir,
		"XDG_STATE_HOME="+stateDir,
		"XDG_CACHE_HOME="+cacheDir,
		"HEY_NO_KEYRING=1",
		"NO_COLOR=1",
		"TERM=dumb",
	)
	return env
}

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

// skipf skips a test the server cannot support — or fails it, under HEY_SMOKE_STRICT=1,
// where a check that did not run is not a check that passed.
func skipf(t *testing.T, format string, args ...any) {
	t.Helper()
	if strict, _ := strconv.ParseBool(os.Getenv("HEY_SMOKE_STRICT")); strict {
		t.Fatalf("strict: "+format, args...)
	}
	t.Skipf(format, args...)
}

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

// setupCLI exercises the CLI's actual PKCE callback and token exchange. The browser
// alone uses the account session; no browser cookie is handed to the CLI. Keep all
// CLI progress private because it contains OAuth state and the authorization URL.
func setupCLI(loginPath ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append(append([]string{}, loginPath...), "--no-browser")
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Env = cliEnv()
	cmd.Dir = workDir
	cmd.Stdout = io.Discard
	progress, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open CLI OAuth progress pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start CLI OAuth login: %w", err)
	}
	defer func() {
		cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Wait()
		}
	}()

	authorization := make(chan string, 1)
	go func() {
		defer close(authorization)
		scanner := bufio.NewScanner(progress)
		found := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			u, err := url.Parse(line)
			base, baseErr := url.Parse(baseURL)
			if !found && err == nil && baseErr == nil && u.Scheme == base.Scheme && u.Host == base.Host && u.Path == "/oauth/authorizations/new" && u.Query().Get("code_challenge_method") == "S256" {
				authorization <- line
				found = true
			}
		}
	}()

	var authURL string
	select {
	case authURL = <-authorization:
		if authURL == "" {
			return fmt.Errorf("CLI did not offer a PKCE authorization URL; check the registered CLI OAuth app")
		}
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for CLI OAuth authorization")
	}

	browser, browserCancel, allocCancel := newBrowserContext()
	defer allocCancel()
	defer browserCancel()
	browser, timeoutCancel := context.WithTimeout(browser, 45*time.Second)
	defer timeoutCancel()
	base, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid smoke server base URL")
	}
	if err := chromedp.Run(browser,
		network.SetCookie("session_token", sessionCookie).WithDomain(base.Hostname()).WithPath("/").WithHTTPOnly(true),
		chromedp.Navigate(authURL),
		chromedp.WaitVisible(`form[action="/oauth/authorizations"] input[type="submit"]`, chromedp.ByQuery),
		// Submit the consent form directly: a coordinate click can miss while
		// the page's styles settle, leaving the CLI waiting for its callback.
		chromedp.Submit(`form[action="/oauth/authorizations"] input[type="submit"]`, chromedp.ByQuery),
	); err != nil {
		// Browser errors can contain the URL, including OAuth codes or state.
		return fmt.Errorf("browser OAuth consent failed; check the registered CLI app and server")
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("CLI OAuth login did not complete successfully")
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
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("failed to parse baseURL %q: %v", baseURL, err)
	}
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
