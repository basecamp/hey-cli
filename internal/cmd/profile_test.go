package cmd

import (
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
	"sort"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
)

// profileTestHome isolates a run: its own config, state and cache, file-backed
// credentials, and none of the variables that would pick a server, an account or a
// profile from the developer's environment.
func profileTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	t.Setenv("HEY_PROFILE", "")
	t.Setenv("HEY_NONINTERACTIVE", "1")
	t.Setenv("OMARCHY_PATH", "")
	t.Chdir(home)
	return home
}

func writeProfileTestConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func readProfileTestConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "hey-cli", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

// storedCredentialKeys lists the keys of the plaintext credential file.
func storedCredentialKeys(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "hey-cli", "credentials.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func runProfileCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetRootFlagsForTrustTest(t)
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func runProfileJSON(t *testing.T, args ...string) (output.Response, string, error) {
	t.Helper()
	stdout, stderr, err := runProfileCLI(t, append([]string{"--json"}, args...)...)
	var response output.Response
	if stdout != "" {
		if decodeErr := json.Unmarshal([]byte(stdout), &response); decodeErr != nil {
			t.Fatalf("decode %q: %v", stdout, decodeErr)
		}
	}
	return response, stderr, err
}

func noRequestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func errorMessage(err error) string {
	var cliErr *apierr.Error
	if errors.As(err, &cliErr) {
		return cliErr.Message
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func statusData(t *testing.T, response output.Response) map[string]any {
	t.Helper()
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", response.Data)
	}
	return data
}

func TestProfilesConfiguredButNoneSelectedRefuses(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"staging":{"base_url":"https://staging.hey.com"},"beta":{"base_url":"https://beta.hey.com"}}}`)

	_, _, err := runProfileJSON(t, "auth", "status")
	want := "multiple profiles configured but none selected; use --profile or set default_profile (available: beta, staging)"
	if got := errorMessage(err); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestProfileSelectionNamesWhatIsConfigured(t *testing.T) {
	home := profileTestHome(t)

	_, _, err := runProfileJSON(t, "-P", "staging", "auth", "status")
	if got, want := errorMessage(err), `profile "staging" specified via --profile but no profiles are configured; create one with: hey profile create`; got != want {
		t.Errorf("flag without profiles: %q, want %q", got, want)
	}
	t.Setenv("HEY_PROFILE", "staging")
	_, _, err = runProfileJSON(t, "auth", "status")
	if got, want := errorMessage(err), `profile "staging" specified via HEY_PROFILE but no profiles are configured; create one with: hey profile create`; got != want {
		t.Errorf("env without profiles: %q, want %q", got, want)
	}

	writeProfileTestConfig(t, home, `{"profiles":{"beta":{"base_url":"https://beta.hey.com"},"work":{"base_url":"https://app.hey.com"}}}`)
	_, _, err = runProfileJSON(t, "auth", "status")
	if got, want := errorMessage(err), `unknown profile "staging" from HEY_PROFILE (available: beta, work)`; got != want {
		t.Errorf("unknown env profile: %q, want %q", got, want)
	}
	t.Setenv("HEY_PROFILE", "")
	_, _, err = runProfileJSON(t, "--profile", "staging", "auth", "status")
	if got, want := errorMessage(err), `unknown profile "staging" (available: beta, work)`; got != want {
		t.Errorf("unknown flag profile: %q, want %q", got, want)
	}
}

// The order is basecamp-cli's: the flag, then HEY_PROFILE, then default_profile, then a
// lone profile used by itself.
func TestProfileResolutionOrder(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"default_profile":"work","profiles":{
		"work":{"base_url":"https://app.hey.com"},
		"beta":{"base_url":"https://beta.hey.com"},
		"staging":{"base_url":"https://staging.hey.com","account_id":"42"}}}`)

	for _, test := range []struct {
		name    string
		env     string
		args    []string
		profile string
		baseURL string
	}{
		{name: "default_profile", profile: "work", baseURL: "https://app.hey.com"},
		{name: "env over default", env: "beta", profile: "beta", baseURL: "https://beta.hey.com"},
		{name: "flag over env", env: "beta", args: []string{"-P", "staging"}, profile: "staging", baseURL: "https://staging.hey.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HEY_PROFILE", test.env)
			response, _, err := runProfileJSON(t, append(test.args, "auth", "status")...)
			if err != nil {
				t.Fatal(err)
			}
			data := statusData(t, response)
			if data["base_url"] != test.baseURL || data["profile"] != test.profile {
				t.Errorf("status = %#v, want profile %s at %s", data, test.profile, test.baseURL)
			}
			if response.Meta["profile"] != test.profile {
				t.Errorf("meta = %#v, want profile %s", response.Meta, test.profile)
			}
		})
	}

	writeProfileTestConfig(t, home, `{"profiles":{"solo":{"base_url":"https://staging.hey.com"}}}`)
	response, _, err := runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["profile"] != "solo" || data["base_url"] != "https://staging.hey.com" {
		t.Errorf("a lone profile was not used by itself: %#v", data)
	}
}

// A profile's credential is keyed by its name, so an override naming another server is
// refused before anything is sent there; the same server, spelled another way, and an
// account override still apply.
func TestAProfilesCredentialNeverGoesToAnOverridingServer(t *testing.T) {
	home := profileTestHome(t)
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		http.NotFound(w, r)
	}))
	t.Cleanup(other.Close)
	own := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"`+own.URL+`","account_id":"42"}}}`)
	store := auth.NewStore(filepath.Join(home, "hey-cli"))
	if err := store.Save("profile:work", &auth.Credentials{AccessToken: "work-access", RefreshToken: "work-refresh", OAuthType: "oauth"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HEY_BASE_URL", other.URL)
	_, _, err := runProfileJSON(t, "account", "list")
	if got := errorMessage(err); got != fmt.Sprintf("HEY_BASE_URL %s is not profile \"work\"'s server (%s)", other.URL, own.URL) {
		t.Errorf("env override: %q", got)
	}
	t.Setenv("HEY_BASE_URL", "")
	_, _, err = runProfileJSON(t, "--base-url", other.URL, "account", "list")
	if got := errorMessage(err); !strings.Contains(got, "--base-url "+other.URL+" is not profile \"work\"'s server") {
		t.Errorf("flag override: %q", got)
	}
	_, _, err = runProfileJSON(t, "--base-url", other.URL, "auth", "login", "--token", "other-token")
	if err == nil {
		t.Error("a sign-in at another server replaced the profile's credential")
	}
	if len(elsewhere) != 0 {
		t.Errorf("the overriding server was reached: %v", elsewhere)
	}

	t.Setenv("HEY_ACCOUNT_ID", "7")
	response, _, err := runProfileJSON(t, "--base-url", own.URL+"/", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["mail_account"] != "7" || data["account_source"] != "env" || data["profile"] != "work" {
		t.Errorf("status = %#v, want the env's account on the profile", data)
	}
	t.Setenv("HEY_ACCOUNT_ID", "")
	response, _, err = runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["mail_account"] != "42" || data["account_source"] != "profile" {
		t.Errorf("status = %#v, want the profile's account", data)
	}
}

// A trusted repository may add profiles, but not move a global one, whose credential
// is keyed by its name, to another server.
func TestARepositoryCannotMoveAGlobalProfile(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"`+server.URL+`"}}}`)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	local := `{"profiles":{"work":{"base_url":"https://other.example.com"},"extra":{"base_url":"https://extra.example.com"}}}`
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(local), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}

	response, stderr, err := runProfileJSON(t, "-P", "work", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["base_url"] != server.URL {
		t.Errorf("the repository moved work: %#v", data)
	}
	if !strings.Contains(stderr, `warning: ignoring profile "work" from local config at`) {
		t.Errorf("stderr = %q", stderr)
	}
	response, _, err = runProfileJSON(t, "-P", "extra", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["base_url"] != "https://extra.example.com" {
		t.Errorf("a new repository profile was not added: %#v", data)
	}
}

func TestProfileDeleteRevokesOnlyWhereTheSignInWasIssued(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"profiles":{
		"moved":{"base_url":"`+server.URL+`"},
		"plain":{"base_url":"http://staging.example.com"}}}`)
	store := auth.NewStore(filepath.Join(home, "hey-cli"))
	if err := store.Save("profile:moved", &auth.Credentials{RefreshToken: "moved-refresh", TokenEndpoint: "https://app.hey.com/oauth/tokens", OAuthType: "oauth"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("profile:plain", &auth.Credentials{RefreshToken: "plain-refresh", OAuthType: "oauth"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"moved", "plain"} {
		response, stderr, err := runProfileJSON(t, "profile", "delete", name)
		if err != nil {
			t.Fatal(err)
		}
		if data := statusData(t, response); data["revoked"] != false {
			t.Errorf("%s: %#v", name, data)
		}
		if !strings.Contains(stderr, "warning: not revoking profile") {
			t.Errorf("%s stderr = %q", name, stderr)
		}
	}
	if keys := storedCredentialKeys(t, home); len(keys) != 0 {
		t.Errorf("stored keys = %v", keys)
	}
}

func TestProfileCompletionRunsWithNoneSelected(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"beta":{"base_url":"https://beta.hey.com"},"work":{"base_url":"https://app.hey.com"}}}`)
	stdout, _, err := runProfileCLI(t, "__complete", "-P", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "beta\thttps://beta.hey.com") || !strings.Contains(stdout, "work\thttps://app.hey.com") {
		t.Errorf("completion = %q", stdout)
	}
}

// A profile's credential is stored under profile:<name>, and no other profile, nor the
// top-level configuration, reads it.
func TestAProfilesCredentialIsNeverReadByAnother(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"profiles":{
		"personal":{"base_url":"`+server.URL+`"},
		"work":{"base_url":"`+server.URL+`"}}}`)

	if _, _, err := runProfileJSON(t, "-P", "personal", "auth", "login", "--token", "personal-token"); err != nil {
		t.Fatal(err)
	}
	if keys := storedCredentialKeys(t, home); strings.Join(keys, ",") != "profile:personal" {
		t.Fatalf("stored keys = %v, want profile:personal alone", keys)
	}

	response, _, err := runProfileJSON(t, "-P", "work", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["authenticated"] != false {
		t.Errorf("work read personal's credential: %#v", data)
	}
	response, _, err = runProfileJSON(t, "-P", "personal", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["authenticated"] != true {
		t.Errorf("personal lost its credential: %#v", data)
	}
	if authMgr.CredentialKey() != "profile:personal" || mcpAuthMgr.CredentialKey() != "mcp:profile:personal" {
		t.Errorf("keys = %q, %q", authMgr.CredentialKey(), mcpAuthMgr.CredentialKey())
	}

	// With no profile chosen, the top-level credential is the origin's, as it always was.
	writeProfileTestConfig(t, home, `{}`)
	response, _, err = runProfileJSON(t, "--base-url", server.URL, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["authenticated"] != false {
		t.Errorf("the top-level configuration read a profile's credential: %#v", data)
	}
}

// A repository's .hey/config.json may name profiles only once it is trusted, through the
// same trusted_local_configs record that covers its base_url and account_id.
func TestRepositoryLocalProfilesNeedTrust(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"profiles":{"home":{"base_url":"`+server.URL+`"}}}`)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	local := `{"default_profile":"work","profiles":{"work":{"base_url":"https://work.example.com"}}}`
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(local), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	response, stderr, err := runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["profile"] != "home" || data["base_url"] != server.URL {
		t.Errorf("an untrusted repository chose the profile: %#v", data)
	}
	if !strings.Contains(stderr, "warning: ignoring profiles from local config at") ||
		!strings.Contains(stderr, "warning: ignoring default_profile \"work\" from local config at") ||
		!strings.Contains(stderr, "run `hey config trust-local` to allow") {
		t.Errorf("stderr = %q, want the ignored keys named", stderr)
	}
	_, _, err = runProfileJSON(t, "-P", "work", "auth", "status")
	if got, want := errorMessage(err), `unknown profile "work" (available: home)`; got != want {
		t.Errorf("untrusted -P work: %q, want %q", got, want)
	}

	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	response, stderr, err = runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["profile"] != "work" || data["base_url"] != "https://work.example.com" {
		t.Errorf("a trusted repository's profile was not used: %#v", data)
	}
	if strings.Contains(stderr, "ignoring") {
		t.Errorf("trusted repository still warned: %q", stderr)
	}

	// A change to the repository's profiles needs trust again.
	changed := `{"default_profile":"work","profiles":{"work":{"base_url":"https://elsewhere.example.com"}}}`
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	response, _, err = runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["profile"] != "home" {
		t.Errorf("a changed repository profile was used without trust: %#v", data)
	}
}

// With no profiles configured nothing changes: the configuration reads as it did, the
// credential is the origin's, and no answer names a profile.
func TestNoProfilesChangesNothing(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"base_url":"`+server.URL+`","account_defaults":{"`+server.URL+`":"9"}}`)

	if _, _, err := runProfileJSON(t, "auth", "login", "--token", "top-level-token"); err != nil {
		t.Fatal(err)
	}
	if keys := storedCredentialKeys(t, home); strings.Join(keys, ",") != server.URL {
		t.Errorf("stored keys = %v, want the origin %s", keys, server.URL)
	}

	response, _, err := runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	data := statusData(t, response)
	if _, named := data["profile"]; named || response.Meta["profile"] != nil {
		t.Errorf("status names a profile: %#v %#v", data, response.Meta)
	}
	if data["base_url"] != server.URL || data["mail_account"] != "9" || data["account_source"] != "global" || data["authenticated"] != true {
		t.Errorf("status = %#v", data)
	}

	response, _, err = runProfileJSON(t, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := response.Data.([]any)
	keys := make([]string, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		keys = append(keys, entry["key"].(string))
	}
	if strings.Join(keys, ",") != "base_url,account_id,onboarded" {
		t.Errorf("config show keys = %v", keys)
	}
	if authMgr.CredentialKey() != server.URL || mcpAuthMgr.CredentialKey() != "mcp::"+server.URL {
		t.Errorf("keys = %q, %q", authMgr.CredentialKey(), mcpAuthMgr.CredentialKey())
	}
}

// fakeProfileSignIn stands in for the browser sign-in profile create runs, storing a
// token under the profile's own key, or failing.
func fakeProfileSignIn(t *testing.T, fail error) *[]string {
	t.Helper()
	var keys []string
	previous := signInProfile
	signInProfile = func(_ context.Context, mgr *auth.Manager, _ auth.LoginOptions) error {
		keys = append(keys, mgr.CredentialKey())
		if fail != nil {
			return fail
		}
		return mgr.GetStore().Save(mgr.CredentialKey(), &auth.Credentials{AccessToken: "access", RefreshToken: "refresh", OAuthType: "oauth"})
	}
	t.Cleanup(func() { signInProfile = previous })
	return &keys
}

func TestProfileCreateSignsInThenRegisters(t *testing.T) {
	home := profileTestHome(t)
	keys := fakeProfileSignIn(t, nil)

	response, _, err := runProfileJSON(t, "profile", "create", "staging", "--base-url", "https://staging.example.com", "--account", "0042")
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary != `Created profile "staging"` {
		t.Errorf("summary = %q", response.Summary)
	}
	data := statusData(t, response)
	if data["name"] != "staging" || data["base_url"] != "https://staging.example.com" || data["account_id"] != "42" || data["default"] != true {
		t.Errorf("data = %#v", data)
	}
	if strings.Join(*keys, ",") != "profile:staging" {
		t.Errorf("signed in under %v", *keys)
	}
	written := readProfileTestConfig(t, home)
	if written["default_profile"] != "staging" {
		t.Errorf("the first profile did not become the default: %#v", written)
	}
	profiles, _ := written["profiles"].(map[string]any)
	if entry, _ := profiles["staging"].(map[string]any); entry["base_url"] != "https://staging.example.com" || entry["account_id"] != "42" {
		t.Errorf("profiles = %#v", profiles)
	}

	// A second profile is not made the default.
	response, _, err = runProfileJSON(t, "profile", "create", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["default"] != nil || data["base_url"] != "https://app.hey.com" {
		t.Errorf("second profile = %#v", data)
	}
	if written := readProfileTestConfig(t, home); written["default_profile"] != "staging" {
		t.Errorf("default moved: %#v", written)
	}
}

func TestProfileCreateRefusals(t *testing.T) {
	home := profileTestHome(t)
	keys := fakeProfileSignIn(t, errors.New("access denied"))

	_, _, err := runProfileJSON(t, "profile", "create", "bad name")
	if got, want := errorMessage(err), `Invalid profile name "bad name": use only letters, numbers, hyphens, and underscores`; got != want {
		t.Errorf("invalid name: %q, want %q", got, want)
	}
	_, _, err = runProfileJSON(t, "profile", "create", "plain", "--base-url", "http://staging.example.com")
	if got := errorMessage(err); !strings.Contains(got, "base URL must use HTTPS") {
		t.Errorf("insecure base URL: %q", got)
	}

	_, _, err = runProfileJSON(t, "profile", "create", "staging")
	if got := errorMessage(err); got != "login failed: access denied" {
		t.Errorf("failed sign-in: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(home, "hey-cli", "config.json")); !os.IsNotExist(statErr) {
		t.Errorf("a failed sign-in wrote the config: %v", statErr)
	}

	writeProfileTestConfig(t, home, `{"profiles":{"staging":{"base_url":"https://staging.example.com"}}}`)
	_, _, err = runProfileJSON(t, "profile", "create", "staging")
	if got, want := errorMessage(err), `Profile "staging" already exists`; got != want {
		t.Errorf("duplicate: %q, want %q", got, want)
	}

	// A file the writers would refuse to rewrite is refused before the sign-in.
	*keys = nil
	writeProfileTestConfig(t, home, `{"profiles":{"staging":{"base_url":"https://staging.example.com"}},"other":1}`)
	if err := os.WriteFile(filepath.Join(home, "hey-cli", "config.json"), []byte(`{"profiles":["staging"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = runProfileJSON(t, "profile", "create", "fresh")
	if err == nil || len(*keys) != 0 {
		t.Errorf("malformed profiles: err %v, signed in %v", err, *keys)
	}
}

func TestProfileListShowAndSetDefault(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"theme":"kept","profiles":{
		"work":{"base_url":"https://app.hey.com","account_id":"42","note":"kept"},
		"beta":{"base_url":"https://beta.hey.com"}}}`)

	// Several profiles and no default: profile commands still run, so the remedy
	// the refusal names can be taken.
	response, _, err := runProfileJSON(t, "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := response.Data.([]any)
	if len(rows) != 2 || response.Summary != "2 profile(s)" || response.Meta["profile"] != nil {
		t.Fatalf("list = %#v", response)
	}
	first, _ := rows[0].(map[string]any)
	second, _ := rows[1].(map[string]any)
	if first["name"] != "beta" || first["authenticated"] != false || first["active"] != nil ||
		second["name"] != "work" || second["account_id"] != "42" {
		t.Errorf("rows = %#v", rows)
	}

	_, _, err = runProfileJSON(t, "profile", "set-default", "nope")
	if got, want := errorMessage(err), `Profile "nope" not found`; got != want {
		t.Errorf("set-default unknown: %q, want %q", got, want)
	}
	response, _, err = runProfileJSON(t, "profile", "set-default", "work")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["status"] != "set_default" || response.Summary != `Default profile set to "work"` {
		t.Errorf("set-default = %#v", response)
	}
	written := readProfileTestConfig(t, home)
	profiles, _ := written["profiles"].(map[string]any)
	work, _ := profiles["work"].(map[string]any)
	if written["default_profile"] != "work" || written["theme"] != "kept" || work["note"] != "kept" {
		t.Errorf("set-default rewrote more than default_profile: %#v", written)
	}

	response, _, err = runProfileJSON(t, "profile", "show")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["name"] != "work" || data["default"] != true || data["account_id"] != "42" || data["authenticated"] != false {
		t.Errorf("show = %#v", data)
	}
	if response.Meta["profile"] != "work" {
		t.Errorf("meta = %#v", response.Meta)
	}
	response, _, err = runProfileJSON(t, "-P", "beta", "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = response.Data.([]any)
	if first, _ := rows[0].(map[string]any); first["active"] != true {
		t.Errorf("active profile not marked: %#v", rows)
	}
	_, _, err = runProfileJSON(t, "profile", "show", "nope")
	if got, want := errorMessage(err), `Profile "nope" not found`; got != want {
		t.Errorf("show unknown: %q, want %q", got, want)
	}
	// An explicit unknown -P is still refused, profile commands or not.
	_, _, err = runProfileJSON(t, "-P", "nope", "profile", "list")
	if got, want := errorMessage(err), `unknown profile "nope" (available: beta, work)`; got != want {
		t.Errorf("-P nope profile list: %q, want %q", got, want)
	}
}

func TestProfileListWithNoProfiles(t *testing.T) {
	profileTestHome(t)
	response, _, err := runProfileJSON(t, "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	if rows, ok := response.Data.([]any); !ok || len(rows) != 0 || response.Summary != "No profiles configured" {
		t.Errorf("list = %#v", response)
	}
}

func TestProfileDeleteRevokesAndForgets(t *testing.T) {
	home := profileTestHome(t)
	var revoked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/oauth/tokens" {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		}
		// ParseForm reads no body for DELETE.
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		revoked = append(revoked, form.Get("token"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	writeProfileTestConfig(t, home, `{"default_profile":"work","profiles":{
		"work":{"base_url":"`+server.URL+`"},
		"beta":{"base_url":"`+server.URL+`"}}}`)
	if _, _, err := runProfileJSON(t, "-P", "beta", "auth", "login", "--token", "pasted-token"); err != nil {
		t.Fatal(err)
	}
	store := auth.NewStore(filepath.Join(home, "hey-cli"))
	issuer := server.URL + "/oauth/tokens"
	if err := store.Save("profile:work", &auth.Credentials{AccessToken: "work-access", RefreshToken: "work-refresh", OAuthType: "oauth", TokenEndpoint: issuer}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("mcp:profile:work", &auth.Credentials{AccessToken: "mcp-access", RefreshToken: "mcp-refresh", OAuthType: "oauth", TokenEndpoint: issuer}); err != nil {
		t.Fatal(err)
	}
	profileCache := filepath.Join(home, "hey-cli", "profiles", "work", "http")
	if err := os.MkdirAll(profileCache, 0700); err != nil {
		t.Fatal(err)
	}

	_, _, err := runProfileJSON(t, "profile", "delete", "nope")
	if got, want := errorMessage(err), `Profile "nope" not found`; got != want {
		t.Errorf("delete unknown: %q, want %q", got, want)
	}

	response, _, err := runProfileJSON(t, "profile", "delete", "work")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["status"] != "deleted" || data["revoked"] != true || response.Summary != `Deleted profile "work"` {
		t.Errorf("delete = %#v", response)
	}
	sort.Strings(revoked)
	if strings.Join(revoked, ",") != "mcp-refresh,work-refresh" {
		t.Errorf("revoked %v, want both of work's sign-ins", revoked)
	}
	if keys := storedCredentialKeys(t, home); strings.Join(keys, ",") != "profile:beta" {
		t.Errorf("stored keys = %v, want beta's alone", keys)
	}
	written := readProfileTestConfig(t, home)
	if _, still := written["default_profile"]; still {
		t.Errorf("default_profile still names the deleted profile: %#v", written)
	}
	if entries, _ := os.ReadDir(profileCache); len(entries) != 0 {
		t.Errorf("cache left behind: %v", entries)
	}

	// A pasted token is deleted, not revoked: hey did not issue it.
	revoked = nil
	response, _, err = runProfileJSON(t, "profile", "delete", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["revoked"] != nil || len(revoked) != 0 {
		t.Errorf("delete beta = %#v, revoked %v", data, revoked)
	}
	if _, still := readProfileTestConfig(t, home)["profiles"]; still {
		t.Error("the last profile's deletion left an empty profiles key")
	}
	if keys := storedCredentialKeys(t, home); len(keys) != 0 {
		t.Errorf("stored keys = %v", keys)
	}
}

// A profile's answers say which profile gave them, its sign-in remedy names it, and
// config show, doctor and auth status report it.
func TestTheActiveProfileIsNamedWhereItMatters(t *testing.T) {
	home := profileTestHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity.json":
			_, _ = w.Write([]byte(`{"id":1,"accounts":[{"id":2,"status":"active"}]}`))
		case "/boxes.json":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	writeProfileTestConfig(t, home, `{"profiles":{"staging":{"base_url":"`+server.URL+`"}}}`)

	_, _, err := runProfileJSON(t, "box", "list")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Hint != "Run: hey auth login -P staging" {
		t.Errorf("logged-out hint = %#v", err)
	}
	if err := addressLoginHint(apierr.ErrAuth("hey mcp isn't signed in")); err != nil {
		if e := apierr.AsError(err); e.Hint != "Run: hey auth login -P staging" {
			t.Errorf("rewritten hint = %q", e.Hint)
		}
	}
	mcpHint := &apierr.Error{Code: apierr.CodeAuth, Message: "x", Hint: "Run: hey auth login --mcp"}
	if e := apierr.AsError(addressLoginHint(mcpHint)); e.Hint != "Run: hey auth login --mcp -P staging" {
		t.Errorf("mcp hint = %q", e.Hint)
	}

	t.Setenv("HEY_TOKEN", "env-token")
	response, _, err := runProfileJSON(t, "box", "list")
	if err != nil {
		t.Fatal(err)
	}
	if response.Meta["profile"] != "staging" {
		t.Errorf("box list meta = %#v", response.Meta)
	}

	response, _, err = runProfileJSON(t, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, raw := range response.Data.([]any) {
		entry, _ := raw.(map[string]any)
		if entry["key"] == "profile" {
			found = entry["value"] == "staging" && entry["source"] == "default"
		}
	}
	if !found {
		t.Errorf("config show = %#v", response.Data)
	}

	response, _, err = runProfileJSON(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, raw := range response.Data.([]any) {
		check, _ := raw.(map[string]any)
		if check["name"] == "Profile" {
			found = check["message"] == "staging ("+server.URL+")"
		}
	}
	if !found {
		t.Errorf("doctor = %#v", response.Data)
	}

	stdout, _, err := runProfileCLI(t, "--styled", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "Profile:   staging\nBase URL:  "+server.URL+"\n") {
		t.Errorf("styled status = %q", stdout)
	}
}

// hey account use under a profile saves the account on the profile, not in the
// top-level account defaults, which belong to the top-level sign-in.
func TestAccountUseUnderAProfileBindsTheProfile(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"account_defaults":{"`+server.URL+`":"9"},"profiles":{"work":{"base_url":"`+server.URL+`","note":"kept"}}}`)

	response, _, err := runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["mail_account"] != "all" || data["account_source"] != "default" {
		t.Errorf("a profile took the top-level account default: %#v", data)
	}

	if _, _, err := runProfileJSON(t, "account", "use", "all"); err != nil {
		t.Fatal(err)
	}
	written := readProfileTestConfig(t, home)
	profiles, _ := written["profiles"].(map[string]any)
	work, _ := profiles["work"].(map[string]any)
	defaults, _ := written["account_defaults"].(map[string]any)
	if work["account_id"] != "all" || work["note"] != "kept" || defaults[server.URL] != "9" {
		t.Errorf("config = %#v", written)
	}
}

func TestProfilePickerAtATerminal(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"beta":{"base_url":"https://beta.hey.com"},"work":{"base_url":"https://app.hey.com"}}}`)
	previousInteractive := interactiveStdio
	interactiveStdio = func() bool { return true }
	t.Cleanup(func() { interactiveStdio = previousInteractive })

	resetRootFlagsForTrustTest(t)
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader("2\n"))
	root.SetArgs([]string{"--styled", "auth", "status"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "[1] beta  https://beta.hey.com") || !strings.HasPrefix(stdout.String(), "Profile:   work\n") {
		t.Errorf("stderr %q, stdout %q", stderr.String(), stdout.String())
	}

	// Canceled, the refusal is the non-interactive one.
	resetRootFlagsForTrustTest(t)
	root = newRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader("\n"))
	root.SetArgs([]string{"--styled", "auth", "status"})
	if err := root.Execute(); !strings.Contains(errorMessage(err), "multiple profiles configured but none selected") {
		t.Errorf("canceled picker: %v", err)
	}

	// Machine output never prompts.
	_, _, err := runProfileJSON(t, "auth", "status")
	if !strings.Contains(errorMessage(err), "multiple profiles configured but none selected") {
		t.Errorf("--json picker: %v", err)
	}
}

func TestProfileCacheIsItsOwn(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"},"../escape":{"base_url":"https://app.hey.com"}}}`)
	if _, _, err := runProfileJSON(t, "-P", "work", "auth", "status"); err != nil {
		t.Fatal(err)
	}
	if got, want := httpCacheDir(), filepath.Join(home, "hey-cli", "profiles", "work", "http"); got != want {
		t.Errorf("cache = %q, want %q", got, want)
	}
	if _, _, err := runProfileJSON(t, "-P", "../escape", "auth", "status"); err != nil {
		t.Fatal(err)
	}
	if got := httpCacheDir(); got != "" {
		t.Errorf("an unsafe profile name got a cache path: %q", got)
	}
	writeProfileTestConfig(t, home, `{}`)
	if _, _, err := runProfileJSON(t, "auth", "status"); err != nil {
		t.Fatal(err)
	}
	if got, want := httpCacheDir(), filepath.Join(home, "hey-cli", "http"); got != want {
		t.Errorf("cache = %q, want %q", got, want)
	}
}

// A default profile hey refuses (plain HTTP to a remote host) refuses every command
// but the ones that repair it.
func TestABrokenDefaultProfileCanStillBeRepaired(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"default_profile":"broken","profiles":{
		"broken":{"base_url":"http://staging.example.com"},
		"work":{"base_url":"https://app.hey.com"}}}`)

	if _, _, err := runProfileJSON(t, "auth", "status"); !strings.Contains(errorMessage(err), "HTTPS") {
		t.Errorf("a broken default was used: %v", err)
	}
	_, stderr, err := runProfileJSON(t, "profile", "set-default", "work")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, `warning: not using profile "broken"`) {
		t.Errorf("stderr = %q", stderr)
	}
	if _, _, err := runProfileJSON(t, "profile", "delete", "broken"); err != nil {
		t.Fatal(err)
	}
	response, _, err := runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if data := statusData(t, response); data["profile"] != "work" {
		t.Errorf("status = %#v", data)
	}
}

// hey config set base_url sets the top-level URL, which no profile's credential uses,
// so a profile does not refuse it.
func TestConfigSetBaseURLUnderAProfile(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	if _, _, err := runProfileJSON(t, "config", "set", "base_url", "https://staging.example.com"); err != nil {
		t.Fatal(err)
	}
	if written := readProfileTestConfig(t, home); written["base_url"] != "https://staging.example.com" {
		t.Errorf("config = %#v", written)
	}
}

// A stored sign-in issued by another server than the profile's is refused, so an
// edited base_url never takes the old server's token somewhere new.
func TestAProfilesSignInIsRefusedAtAnotherServer(t *testing.T) {
	home := profileTestHome(t)
	server := noRequestServer(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"`+server.URL+`"}}}`)
	store := auth.NewStore(filepath.Join(home, "hey-cli"))
	if err := store.Save("profile:work", &auth.Credentials{AccessToken: "work-access", RefreshToken: "work-refresh", OAuthType: "oauth", TokenEndpoint: "https://app.hey.com/oauth/tokens"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := runProfileJSON(t, "account", "list")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || !strings.Contains(cliErr.Message, `profile "work"'s sign-in was issued by https://app.hey.com/oauth/tokens`) || cliErr.Hint != "Run: hey auth login -P work" {
		t.Errorf("err = %#v", err)
	}

	// A pasted token records its server too.
	if _, _, err := runProfileJSON(t, "auth", "login", "--token", "pasted"); err != nil {
		t.Fatal(err)
	}
	creds, err := store.Load("profile:work")
	if err != nil || creds.TokenEndpoint != server.URL+"/oauth/tokens" {
		t.Errorf("pasted token = %#v, %v", creds, err)
	}
}

func TestDeletingARepositoryOnlyProfileIsRefused(t *testing.T) {
	home := profileTestHome(t)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(`{"profiles":{"local":{"base_url":"https://local.example.com"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	_, _, err := runProfileJSON(t, "profile", "delete", "local")
	if got := errorMessage(err); !strings.Contains(got, `Profile "local" is set by `) || !strings.Contains(got, "not the global config; remove it there") {
		t.Errorf("delete = %q", got)
	}
}

// The commands that manage config files run with several profiles and none selected,
// so a repository's profiles can be trusted from there; config show still refuses.
func TestConfigCommandsRunWithNoProfileSelected(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"beta":{"base_url":"https://beta.hey.com"},"work":{"base_url":"https://app.hey.com"}}}`)
	if _, _, err := runProfileJSON(t, "config", "trusted-locals"); err != nil {
		t.Errorf("trusted-locals: %v", err)
	}
	if _, _, err := runProfileJSON(t, "config", "show"); !strings.Contains(errorMessage(err), "none selected") {
		t.Errorf("config show: %v", err)
	}
}

func TestLoggedOutStatusNamesTheProfilesLogin(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	response, _, err := runProfileJSON(t, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Breadcrumbs) != 1 || response.Breadcrumbs[0].Command != "hey auth login -P work" {
		t.Errorf("breadcrumbs = %#v", response.Breadcrumbs)
	}
}

func TestCompletionSurvivesARefusedProfile(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	t.Setenv("HEY_BASE_URL", "https://staging.example.com")
	stdout, _, err := runProfileCLI(t, "__complete", "-P", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "work\thttps://app.hey.com") {
		t.Errorf("completion = %q", stdout)
	}
}

// hey auth logout --mcp revokes a profile's grant only at the server that issued it,
// and still forgets it here.
func TestProfileMCPLogoutRevokesOnlyWhereTheSignInWasIssued(t *testing.T) {
	for name, endpoint := range map[string]string{"another server": "https://app.hey.com/oauth/tokens", "no server recorded": ""} {
		t.Run(name, func(t *testing.T) {
			home := profileTestHome(t)
			server := noRequestServer(t)
			writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"`+server.URL+`"}}}`)
			store := auth.NewStore(filepath.Join(home, "hey-cli"))
			if err := store.Save("mcp:profile:work", &auth.Credentials{AccessToken: "mcp-access", RefreshToken: "mcp-refresh", OAuthType: "oauth", TokenEndpoint: endpoint}); err != nil {
				t.Fatal(err)
			}
			response, stderr, err := runProfileJSON(t, "auth", "logout", "--mcp")
			if err != nil {
				t.Fatal(err)
			}
			if data, _ := response.Data.(map[string]any); data["revoked"] != false || !strings.Contains(stderr, "couldn't revoke") {
				t.Errorf("logout = %#v, stderr %q", response.Data, stderr)
			}
			if _, err := store.Load("mcp:profile:work"); !errors.Is(err, auth.ErrCredentialsNotFound) {
				t.Errorf("credential kept: %v", err)
			}
		})
	}
}

// A sign-in whose profile could not be written is revoked as well as forgotten, so no
// grant is left that nothing here names.
func TestAFailedProfileCreateRevokesItsSignIn(t *testing.T) {
	home := profileTestHome(t)
	var revoked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/oauth/tokens" {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		revoked = append(revoked, form.Get("token"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	previous := signInProfile
	signInProfile = func(_ context.Context, mgr *auth.Manager, _ auth.LoginOptions) error {
		if err := mgr.GetStore().Save(mgr.CredentialKey(), &auth.Credentials{AccessToken: "access", RefreshToken: "refresh", OAuthType: "oauth", TokenEndpoint: server.URL + "/oauth/tokens"}); err != nil {
			return err
		}
		// The config file breaks while the browser sign-in runs.
		writeProfileTestConfig(t, home, `{`)
		return nil
	}
	t.Cleanup(func() { signInProfile = previous })

	if _, _, err := runProfileJSON(t, "profile", "create", "work", "--base-url", server.URL); err == nil {
		t.Fatal("create succeeded on a broken config")
	}
	if len(revoked) != 1 || revoked[0] != "refresh" {
		t.Errorf("revoked = %q", revoked)
	}
	if _, err := auth.NewStore(filepath.Join(home, "hey-cli")).Load("profile:work"); !errors.Is(err, auth.ErrCredentialsNotFound) {
		t.Errorf("credential kept: %v", err)
	}
}

// A trusted repository entry for a global profile would bring it back, signed out,
// once the global entry went, so delete refuses and names the file.
func TestDeletingAProfileARepositoryAlsoSetsIsRefused(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(`{"profiles":{"work":{"base_url":"https://app.hey.com","account_id":"42"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	_, _, err := runProfileJSON(t, "profile", "delete", "work")
	if got := errorMessage(err); !strings.Contains(got, `Profile "work" is also set by `) {
		t.Errorf("delete = %q", got)
	}
	if _, ok := readProfileTestConfig(t, home)["profiles"].(map[string]any)["work"]; !ok {
		t.Error("the global entry went anyway")
	}
}

// A repository entry with no base_url is no profile, so it doesn't hold up a delete.
func TestARepositoryEntryWithNoServerDoesNotHoldUpADelete(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(`{"profiles":{"work":{"account_id":"42"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runProfileJSON(t, "profile", "delete", "work"); err != nil {
		t.Errorf("delete: %v", err)
	}
}

func TestTheTrustPromptPrintsANumericAccountWhole(t *testing.T) {
	previousCfg := cfg
	cfg = &config.Config{BaseURL: "https://app.hey.com"}
	t.Cleanup(func() { cfg = previousCfg })
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader("\n"))
	var output bytes.Buffer
	cmd.SetErr(&output)
	if _, err := promptForLocalConfigTrust(cmd, &config.LocalConfig{
		Path:         "/workspace/.hey/config.json",
		ProfileNames: []string{"blank", "work"},
		Profiles: map[string]any{
			"blank": map[string]any{"base_url": "https://app.hey.com", "account_id": ""},
			"work":  map[string]any{"base_url": "https://app.hey.com", "account_id": float64(4567890)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "mail account 4567890") || !strings.Contains(output.String(), "blank  https://app.hey.com\n") {
		t.Errorf("prompt = %q", output.String())
	}
}

// version, commands and shell completion read no server, so a profile hey refuses can't
// stop them, and several with none chosen ask nothing. upgrade takes the same path but
// reaches GitHub, so it isn't run here.
func TestCommandsThatReadNoServerSkipProfiles(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"default_profile":"broken","profiles":{"broken":{"base_url":"http://remote.example.com"},"work":{"base_url":"https://app.hey.com"}}}`)
	for _, args := range [][]string{{"version"}, {"commands"}, {"completion", "bash"}, {"shell-completion", "generate", "bash"}} {
		if _, _, err := runProfileCLI(t, args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	t.Setenv("HEY_PROFILE", "missing")
	if _, _, err := runProfileCLI(t, "version"); err != nil {
		t.Errorf("version with an unknown HEY_PROFILE: %v", err)
	}
}

func TestAFailedAnswerNamesTheProfile(t *testing.T) {
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	cfg = &config.Config{ActiveProfile: "work"}
	shared := &apierr.Error{Code: "not_found", Message: "resource not found", Meta: map[string]any{"status": 404}}
	got := apierr.AsError(withProfileMeta(shared))
	if got.Meta["profile"] != "work" || got.Meta["status"] != 404 {
		t.Errorf("meta = %#v", got.Meta)
	}
	if _, touched := shared.Meta["profile"]; touched {
		t.Error("the shared error was changed")
	}
	cfg = &config.Config{}
	if withProfileMeta(shared) != error(shared) {
		t.Error("an error with no profile active changed")
	}
}

// A credential store hey can't read or clear keeps the profile, so a retry can still
// reach its credential.
func TestProfileDeleteKeepsTheProfileWhenItsCredentialStays(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	// A directory where the credential file belongs: the store can neither read nor
	// rewrite it.
	if err := os.MkdirAll(filepath.Join(home, "hey-cli", "credentials.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runProfileJSON(t, "profile", "delete", "work"); !strings.Contains(errorMessage(err), `could not delete credentials for profile "work"`) {
		t.Errorf("delete = %v", err)
	}
	if _, ok := readProfileTestConfig(t, home)["profiles"].(map[string]any)["work"]; !ok {
		t.Error("the profile went while its credential could not")
	}
}

func TestAccountUseUnderARepositoryProfile(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(`{"profiles":{"work":{"base_url":"https://app.hey.com","account_id":"42"},"local":{"base_url":"https://app.hey.com"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.ApplyProfile("work", config.SourceFlag); err != nil {
		t.Fatal(err)
	}
	cfg = loaded
	if notice := accountOverrideNotice(config.SourceProfile); !strings.Contains(notice, "remains higher precedence") {
		t.Errorf("notice = %q", notice)
	}
	if err := loaded.ApplyProfile("local", config.SourceFlag); err != nil {
		t.Fatal(err)
	}
	if err := loaded.SaveAccountID("all"); !strings.Contains(errorMessage(err), `Profile "local" is set by `) || !strings.Contains(errorMessage(err), "set its account_id there") {
		t.Errorf("save = %v", err)
	}
}
