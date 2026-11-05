package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/output"
)

type originRequest struct {
	method        string
	path          string
	query         string
	authorization string
	cookie        string
	form          url.Values
}

// originServer is one origin of the pair, hearing everything sent to it.
type originServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []originRequest
}

func newOriginServer(t *testing.T, respond http.HandlerFunc) *originServer {
	t.Helper()
	origin := &originServer{}
	origin.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		origin.mu.Lock()
		origin.requests = append(origin.requests, originRequest{
			method:        r.Method,
			path:          r.URL.Path,
			query:         r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"),
			cookie:        r.Header.Get("Cookie"),
			form:          form,
		})
		origin.mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(origin.Close)
	return origin
}

func (o *originServer) heard() []originRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]originRequest{}, o.requests...)
}

func answerTokens(access, refresh string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/oauth/tokens" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 3600})
	}
}

// answerBoxes serves the box list to one token and a 401 to any other.
func answerBoxes(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="https://api.hey.com/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":1,"kind":"imbox","name":"Imbox"}]`)
	}
}

type originPair struct {
	login      *originServer
	api        *originServer
	configHome string
	manager    *auth.Manager
}

func newOriginPair(t *testing.T, loginAnswer, apiAnswer http.HandlerFunc) *originPair {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_ACCOUNT_ID", "")
	t.Setenv("HEY_CABLE_URL", "")
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", configHome)

	pair := &originPair{configHome: configHome}
	pair.login = newOriginServer(t, loginAnswer)
	pair.api = newOriginServer(t, apiAnswer)
	t.Setenv("HEY_BASE_URL", pair.login.URL)
	t.Setenv("HEY_API_URL", pair.api.URL)
	pair.manager = auth.NewManager(pair.login.URL, pair.login.Client(), filepath.Join(configHome, "hey-cli"))
	return pair
}

func (p *originPair) signIn(t *testing.T, creds *auth.Credentials) {
	t.Helper()
	if err := p.manager.GetStore().Save(p.manager.CredentialKey(), creds); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
}

// signedIn is what a sign-in at the login origin leaves behind.
func (p *originPair) signedIn(access, refresh string, expiresAt time.Time) *auth.Credentials {
	return &auth.Credentials{
		AccessToken:   access,
		RefreshToken:  refresh,
		OAuthType:     "oauth",
		TokenEndpoint: p.login.URL + "/oauth/tokens",
		ExpiresAt:     expiresAt.Unix(),
	}
}

func (p *originPair) storedAtLogin(t *testing.T) *auth.Credentials {
	t.Helper()
	creds, err := p.manager.GetStore().Load(p.login.URL)
	if err != nil {
		t.Fatalf("load credentials at the login origin: %v", err)
	}
	return creds
}

func (p *originPair) assertNothingStoredAtAPI(t *testing.T) {
	t.Helper()
	if creds, err := p.manager.GetStore().Load(p.api.URL); !errors.Is(err, auth.ErrCredentialsNotFound) {
		t.Errorf("credentials at the API origin = %+v, %v; want none", creds, err)
	}
}

func (p *originPair) run(t *testing.T, args ...string) (string, output.Response, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--json"}, args...))
	// A sign-in nobody answers waits minutes for its browser, and a failure here shouldn't.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	err := root.ExecuteContext(ctx)

	var response output.Response
	if stdout.Len() > 0 {
		_ = json.Unmarshal(stdout.Bytes(), &response)
	}
	return stdout.String(), response, err
}

func assertBearerOnly(t *testing.T, requests []originRequest, token string) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("the API origin heard nothing")
	}
	for _, request := range requests {
		if request.authorization != "Bearer "+token {
			t.Errorf("%s %s carried %q, want the token %q", request.method, request.path, request.authorization, token)
		}
		if strings.HasPrefix(request.path, "/oauth") {
			t.Errorf("the API origin was asked %s %s", request.method, request.path)
		}
	}
}

func assertOneRefreshAtLogin(t *testing.T, requests []originRequest, refreshToken string) {
	t.Helper()
	if len(requests) != 1 {
		t.Fatalf("the login origin heard %+v, want one refresh", requests)
	}
	refresh := requests[0]
	if refresh.method != http.MethodPost || refresh.path != "/oauth/tokens" {
		t.Errorf("the login origin heard %s %s", refresh.method, refresh.path)
	}
	if refresh.form.Get("grant_type") != "refresh_token" || refresh.form.Get("refresh_token") != refreshToken {
		t.Errorf("refresh form = %v, want the refresh token %q", refresh.form, refreshToken)
	}
	if refresh.authorization != "" {
		t.Errorf("the refresh carried Authorization %q", refresh.authorization)
	}
}

func TestAPIOriginCarriesTheTokenAndTheLoginOriginHearsNothing(t *testing.T) {
	pair := newOriginPair(t, answerTokens("unused", "unused"), answerBoxes("signed-in-access"))
	pair.signIn(t, pair.signedIn("signed-in-access", "signed-in-refresh", time.Now().Add(time.Hour)))

	if _, _, err := pair.run(t, "box", "list"); err != nil {
		t.Fatalf("box list: %v", err)
	}

	assertBearerOnly(t, pair.api.heard(), "signed-in-access")
	if heard := pair.login.heard(); len(heard) != 0 {
		t.Errorf("the login origin heard %+v", heard)
	}
	pair.assertNothingStoredAtAPI(t)
}

func TestAPIOriginTakesTheEnvironmentsTokenWithoutRefreshing(t *testing.T) {
	pair := newOriginPair(t, answerTokens("unused", "unused"), answerBoxes("first-test-token"))
	t.Setenv("HEY_TOKEN", "first-test-token")

	if _, _, err := pair.run(t, "box", "list"); err != nil {
		t.Fatalf("box list: %v", err)
	}

	assertBearerOnly(t, pair.api.heard(), "first-test-token")
	if heard := pair.login.heard(); len(heard) != 0 {
		t.Errorf("the login origin heard %+v", heard)
	}
}

func TestAnExpiringTokenRefreshesAtTheLoginOriginOnly(t *testing.T) {
	pair := newOriginPair(t, answerTokens("rotated-access", "rotated-refresh"), answerBoxes("rotated-access"))
	pair.signIn(t, pair.signedIn("old-access", "old-refresh", time.Now().Add(time.Minute)))

	if _, _, err := pair.run(t, "box", "list"); err != nil {
		t.Fatalf("box list: %v", err)
	}

	assertOneRefreshAtLogin(t, pair.login.heard(), "old-refresh")
	assertBearerOnly(t, pair.api.heard(), "rotated-access")
	if stored := pair.storedAtLogin(t); stored.AccessToken != "rotated-access" || stored.RefreshToken != "rotated-refresh" {
		t.Errorf("stored at the login origin = %+v", stored)
	}
	pair.assertNothingStoredAtAPI(t)
}

func TestARefreshWithNoStoredEndpointStaysOnTheLoginOrigin(t *testing.T) {
	pair := newOriginPair(t, answerTokens("rotated-access", "rotated-refresh"), answerBoxes("rotated-access"))
	stored := pair.signedIn("old-access", "old-refresh", time.Now().Add(-time.Hour))
	stored.TokenEndpoint = ""
	pair.signIn(t, stored)

	if _, _, err := pair.run(t, "box", "list"); err != nil {
		t.Fatalf("box list: %v", err)
	}

	assertOneRefreshAtLogin(t, pair.login.heard(), "old-refresh")
	assertBearerOnly(t, pair.api.heard(), "rotated-access")
}

// The token had life left and the API origin refused it anyway, naming its own issuer in
// the challenge. The refresh still goes where the grant was made, and the read is resent.
func TestAnAPIOrigin401RefreshesAtTheLoginOriginAndResends(t *testing.T) {
	pair := newOriginPair(t, answerTokens("rotated-access", "rotated-refresh"), answerBoxes("rotated-access"))
	pair.signIn(t, pair.signedIn("refused-access", "old-refresh", time.Now().Add(time.Hour)))

	if _, _, err := pair.run(t, "box", "list"); err != nil {
		t.Fatalf("box list: %v", err)
	}

	assertOneRefreshAtLogin(t, pair.login.heard(), "old-refresh")
	heard := pair.api.heard()
	if len(heard) != 2 || heard[0].authorization != "Bearer refused-access" || heard[1].authorization != "Bearer rotated-access" {
		t.Errorf("the API origin heard %+v, want the refused token and then the new one", heard)
	}
	if stored := pair.storedAtLogin(t); stored.AccessToken != "rotated-access" {
		t.Errorf("stored at the login origin = %+v", stored)
	}
	pair.assertNothingStoredAtAPI(t)
}

func TestAnAPIOrigin401ThatPersistsEndsInAnAuthFailureAfterOneRefresh(t *testing.T) {
	pair := newOriginPair(t, answerTokens("rotated-access", "rotated-refresh"), answerBoxes("no-token-of-ours"))
	pair.signIn(t, pair.signedIn("refused-access", "old-refresh", time.Now().Add(time.Hour)))

	_, _, err := pair.run(t, "box", "list")

	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want an auth failure", err)
	}
	assertOneRefreshAtLogin(t, pair.login.heard(), "old-refresh")
	if stored := pair.storedAtLogin(t); stored.RefreshToken != "rotated-refresh" {
		t.Errorf("stored at the login origin = %+v, want the login kept", stored)
	}
}

func TestARefreshRefusedAtTheLoginOriginClearsTheLogin(t *testing.T) {
	refuse := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}
	pair := newOriginPair(t, refuse, answerBoxes("no-token-of-ours"))
	pair.signIn(t, pair.signedIn("refused-access", "dead-refresh", time.Now().Add(time.Hour)))

	_, _, err := pair.run(t, "box", "list")

	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want an auth failure", err)
	}
	assertOneRefreshAtLogin(t, pair.login.heard(), "dead-refresh")
	if _, loadErr := pair.manager.GetStore().Load(pair.login.URL); !errors.Is(loadErr, auth.ErrCredentialsNotFound) {
		t.Errorf("credentials after invalid_grant: %v", loadErr)
	}
}

// hey2 answers an identity it hasn't admitted with a 403. That rejects no credential, so
// nothing is rotated for it.
func TestNotAdmittedAtTheAPIOriginRefreshesNothing(t *testing.T) {
	notAdmitted := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"not_admitted","reason":"not_enrolled"}`)
	}
	pair := newOriginPair(t, answerTokens("unused", "unused"), notAdmitted)
	pair.signIn(t, pair.signedIn("signed-in-access", "signed-in-refresh", time.Now().Add(time.Hour)))

	_, _, err := pair.run(t, "box", "list")

	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeForbidden {
		t.Fatalf("error = %v, want forbidden", err)
	}
	if heard := pair.login.heard(); len(heard) != 0 {
		t.Errorf("the login origin heard %+v", heard)
	}
	if stored := pair.storedAtLogin(t); stored.AccessToken != "signed-in-access" {
		t.Errorf("stored at the login origin = %+v", stored)
	}
}

func TestCredentialsUnderTheAPIOriginAreNotFound(t *testing.T) {
	pair := newOriginPair(t, answerTokens("unused", "unused"), answerBoxes("api-issued"))
	if err := pair.manager.GetStore().Save(pair.api.URL, &auth.Credentials{AccessToken: "api-issued", RefreshToken: "api-refresh", TokenEndpoint: pair.api.URL + "/oauth/tokens"}); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}

	_, _, err := pair.run(t, "box", "list")

	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want not logged in", err)
	}
	if heard := pair.api.heard(); len(heard) != 0 {
		t.Errorf("the API origin heard %+v", heard)
	}
	if heard := pair.login.heard(); len(heard) != 0 {
		t.Errorf("the login origin heard %+v", heard)
	}
}

// A sign-in is the browser's and the login origin's alone, API origin or no.
func TestSigningInGoesToTheLoginOriginAndIsKeptThere(t *testing.T) {
	pair := newOriginPair(t, answerTokens("signed-in-access", "signed-in-refresh"), answerBoxes("signed-in-access"))

	realStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = realStderr })

	browser := make(chan error, 1)
	go func() { browser <- playBrowser(t.Context(), pair.login.URL, reader) }()

	_, _, loginErr := pair.run(t, "auth", "login", "--no-browser")
	_ = writer.Close()
	if loginErr != nil {
		t.Fatalf("auth login: %v", loginErr)
	}
	if browserErr := <-browser; browserErr != nil {
		t.Fatal(browserErr)
	}

	heard := pair.login.heard()
	if len(heard) != 1 || heard[0].path != "/oauth/tokens" || heard[0].form.Get("grant_type") != "authorization_code" {
		t.Errorf("the login origin heard %+v, want the code exchanged", heard)
	}
	for _, request := range pair.api.heard() {
		if strings.HasPrefix(request.path, "/oauth") {
			t.Errorf("the API origin was asked %s %s", request.method, request.path)
		}
	}
	stored := pair.storedAtLogin(t)
	if stored.AccessToken != "signed-in-access" || stored.TokenEndpoint != pair.login.URL+"/oauth/tokens" {
		t.Errorf("stored at the login origin = %+v", stored)
	}
	pair.assertNothingStoredAtAPI(t)
}

// playBrowser reads the printed authorization URL and answers the CLI's callback as the
// login origin's redirect would. It answers even when the URL names the wrong origin, so
// that failure is reported rather than left waiting on the browser.
func playBrowser(ctx context.Context, loginOrigin string, printed io.Reader) error {
	scanner := bufio.NewScanner(printed)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, "/oauth/authorizations/new") {
			continue
		}
		authorization, err := url.Parse(line)
		if err != nil {
			return fmt.Errorf("authorization URL %q: %w", line, err)
		}
		query := authorization.Query()
		callback := query.Get("redirect_uri") + "?code=browser-code&state=" + url.QueryEscape(query.Get("state"))
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, callback, nil)
		if err != nil {
			return fmt.Errorf("callback: %w", err)
		}
		response, err := http.DefaultClient.Do(request) //nolint:gosec // G704: the test's own loopback callback
		if err != nil {
			return fmt.Errorf("callback: %w", err)
		}
		_ = response.Body.Close()
		_, _ = io.Copy(io.Discard, printed)
		if origin := authorization.Scheme + "://" + authorization.Host; origin != loginOrigin {
			return fmt.Errorf("authorization went to %s, want %s", origin, loginOrigin)
		}
		return nil
	}
	return errors.New("the CLI printed no authorization URL")
}

func TestARedirectOffTheAPIOriginDropsTheCredentials(t *testing.T) {
	credentials := map[string]func(*originPair) *auth.Credentials{
		"a token": func(*originPair) *auth.Credentials {
			return &auth.Credentials{AccessToken: "signed-in-access", OAuthType: "token"}
		},
		"a cookie": func(*originPair) *auth.Credentials {
			return &auth.Credentials{SessionCookie: "signed-in-cookie", OAuthType: "cookie"}
		},
		// The one that would refresh if a 401 from the hop were taken for its own.
		"a sign-in": func(pair *originPair) *auth.Credentials {
			return pair.signedIn("signed-in-access", "signed-in-refresh", time.Now().Add(time.Hour))
		},
	}
	for name, credential := range credentials {
		t.Run(name, func(t *testing.T) {
			var loginOrigin string
			pair := newOriginPair(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/oauth/tokens" {
						answerTokens("rotated-access", "rotated-refresh")(w, r)
						return
					}
					w.WriteHeader(http.StatusUnauthorized)
				},
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Location", loginOrigin+r.URL.Path)
					w.WriteHeader(http.StatusFound)
				})
			loginOrigin = pair.login.URL
			pair.signIn(t, credential(pair))

			if _, _, err := pair.run(t, "box", "list"); err == nil {
				t.Error("a 401 from the redirect's target was taken for an answer")
			}

			if heard := pair.api.heard(); len(heard) != 1 {
				t.Errorf("the API origin heard %+v, want one attempt", heard)
			}
			heard := pair.login.heard()
			if len(heard) == 0 {
				t.Fatal("the redirect was not followed, so it shows nothing")
			}
			for _, request := range heard {
				if request.authorization != "" || request.cookie != "" {
					t.Errorf("the login origin was sent credentials: %+v", request)
				}
				if strings.HasPrefix(request.path, "/oauth") {
					t.Errorf("a 401 off the API origin asked for a refresh: %s", request.path)
				}
			}
		})
	}
}

// A next page HEY names on another origin is never asked of that origin, whichever way
// the listing pages: a box by the cursor in next_history_url, a label by the Link header.
// Each listing goes on to its second page at the API origin, so the origin it named was
// passed over rather than never reached.
func TestAPageNamedOnTheLoginOriginIsNeverReadThere(t *testing.T) {
	var loginOrigin string
	pair := newOriginPair(t,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) },
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			page := r.URL.Query().Get("page")
			switch {
			case strings.HasPrefix(r.URL.Path, "/boxes.json"):
				_, _ = io.WriteString(w, `[{"id":1,"kind":"imbox","name":"Imbox"}]`)
			case strings.HasPrefix(r.URL.Path, "/imbox") && page == "":
				_, _ = fmt.Fprintf(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[{"id":101,"kind":"topic"}],"next_history_url":%q}`, loginOrigin+r.URL.Path+"?page=box-cursor")
			case strings.HasPrefix(r.URL.Path, "/imbox") && page == "box-cursor":
				_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[{"id":201,"kind":"topic"}]}`)
			case strings.HasPrefix(r.URL.Path, "/folders/") && page == "":
				w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=label-cursor>; rel="next"`, loginOrigin, r.URL.Path))
				_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":102,"kind":"topic"}]}`)
			case strings.HasPrefix(r.URL.Path, "/folders/") && page == "label-cursor":
				_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":202,"kind":"topic"}]}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
	loginOrigin = pair.login.URL
	pair.signIn(t, pair.signedIn("signed-in-access", "signed-in-refresh", time.Now().Add(time.Hour)))

	listings := map[string][]string{
		"201": {"box", "view", "imbox", "--all"},
		"202": {"label", "12", "--all"},
	}
	for secondPagePosting, args := range listings {
		stdout, _, err := pair.run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(stdout, secondPagePosting) {
			t.Errorf("%v shows no second page: %s", args, stdout)
		}
	}

	if heard := pair.login.heard(); len(heard) != 0 {
		t.Errorf("the login origin heard %+v", heard)
	}
	assertBearerOnly(t, pair.api.heard(), "signed-in-access")
	heard := pair.api.heard()
	queries := make([]string, 0, len(heard))
	for _, request := range heard {
		queries = append(queries, request.query)
	}
	for _, cursor := range []string{"page=box-cursor", "page=label-cursor"} {
		if !strings.Contains(strings.Join(queries, " "), cursor) {
			t.Errorf("queries at the API origin = %q, want %s", queries, cursor)
		}
	}
}

func TestTheConfiguredAPIOriginIsShownWhereTheServerIs(t *testing.T) {
	pair := newOriginPair(t, answerTokens("unused", "unused"), answerBoxes("unused"))
	t.Setenv("HEY_API_URL", "")

	_, status, err := pair.run(t, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if data := status.Data.(map[string]any); data["api_url"] != nil {
		t.Errorf("api_url with none set = %v", data["api_url"])
	}

	if _, _, err = pair.run(t, "config", "set", "api_url", pair.api.URL+"/"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	_, status, err = pair.run(t, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if data := status.Data.(map[string]any); data["api_url"] != pair.api.URL || data["base_url"] != pair.login.URL {
		t.Errorf("auth status = %v", data)
	}

	stdout, _, err := pair.run(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(stdout, pair.api.URL) || !strings.Contains(stdout, `"api_url"`) {
		t.Errorf("config show = %s", stdout)
	}

	pair.signIn(t, pair.signedIn("signed-in-access", "signed-in-refresh", time.Now().Add(time.Hour)))
	if _, _, err = pair.run(t, "config", "set", "api_url", ""); err != nil {
		t.Fatalf("config set empty: %v", err)
	}
	_, _, _ = pair.run(t, "box", "list")
	if len(pair.login.heard()) == 0 {
		t.Error("clearing api_url left API requests off the base URL")
	}
}

func TestAnAPIURLThatIsNoOriginIsRefused(t *testing.T) {
	pair := newOriginPair(t, answerTokens("unused", "unused"), answerBoxes("unused"))
	for _, value := range []string{"http://api.hey.example", "https://api.hey.example/v1", "https://someone:secret@api.hey.example", "api.hey.example"} {
		t.Run(value, func(t *testing.T) {
			if _, _, err := pair.run(t, "config", "set", "api_url", value); err == nil {
				t.Errorf("config set api_url %q was accepted", value)
			}
		})
	}
}
