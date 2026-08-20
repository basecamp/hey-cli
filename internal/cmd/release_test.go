package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsUpdateAvailable(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "newer stable release", current: "1.0.0", latest: "1.1.0", want: true},
		{name: "same version", current: "1.0.0", latest: "1.0.0", want: false},
		{name: "older latest suppressed", current: "1.1.0", latest: "1.0.0", want: false},
		{name: "pseudo current newer than older release", current: "0.4.1-0.20260313174735-243815fa23b2", latest: "0.4.0", want: false},
		{name: "release newer than pseudo prerelease of same base", current: "0.4.1-0.20260313174735-243815fa23b2", latest: "0.4.1", want: true},
		{name: "v prefix tolerated", current: "v1.0.0", latest: "1.0.1", want: true},
		{name: "invalid fallback treats different as update", current: "custom-build", latest: "1.0.0", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUpdateAvailable(tt.current, tt.latest); got != tt.want {
				t.Fatalf("isUpdateAvailable(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestIsReleaseVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.3":                               true,
		"v1.2.3":                              true,
		"0.4.1-0.20260313174735-243815fa23b2": true,
		"dev":                                 false,
		"":                                    false,
		"abc1234":                             false,
	} {
		if got := isReleaseVersion(v); got != want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func stubReleasesAPIBase(t *testing.T, url string) {
	t.Helper()
	orig := releasesAPIBase
	releasesAPIBase = url
	t.Cleanup(func() { releasesAPIBase = orig })
}

func TestFetchLatestReleaseParsesTagAndAssets(t *testing.T) {
	var gotAccept, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.4.0",
			"assets": [
				{"name": "hey_1.4.0_linux_amd64.tar.gz", "browser_download_url": "https://example.com/hey_1.4.0_linux_amd64.tar.gz"},
				{"name": "checksums.txt", "browser_download_url": "https://example.com/checksums.txt"}
			]
		}`))
	}))
	t.Cleanup(srv.Close)
	stubReleasesAPIBase(t, srv.URL)
	t.Setenv("GITHUB_TOKEN", "ghp_example_token")

	release, err := fetchLatestRelease(context.Background())
	mustNoError(t, err)
	if release.Version != "1.4.0" {
		t.Errorf("version = %q, want 1.4.0 (v stripped)", release.Version)
	}
	asset, ok := release.asset("checksums.txt")
	if !ok || asset.DownloadURL != "https://example.com/checksums.txt" {
		t.Errorf("asset lookup = %+v, %v", asset, ok)
	}
	if _, ok := release.asset("checksums.txt.bundle"); ok {
		t.Error("asset lookup must be exact")
	}
	if gotAccept != "application/vnd.github.v3+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotAuth != "" {
		t.Errorf("token must never be sent to a non-GitHub host, got Authorization %q", gotAuth)
	}
}

func TestFetchReleaseByTagUsesTagPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"tag_name": "v1.5.0-rc.1", "assets": []}`))
	}))
	t.Cleanup(srv.Close)
	stubReleasesAPIBase(t, srv.URL)

	release, err := fetchReleaseByTag(context.Background(), "1.5.0-rc.1")
	mustNoError(t, err)
	if gotPath != "/tags/v1.5.0-rc.1" {
		t.Errorf("path = %q, want /tags/v1.5.0-rc.1", gotPath)
	}
	if release.Version != "1.5.0-rc.1" {
		t.Errorf("version = %q, want 1.5.0-rc.1", release.Version)
	}
}

func TestFetchLatestReleaseNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	stubReleasesAPIBase(t, srv.URL)

	_, err := fetchLatestRelease(context.Background())
	assertErrorContains(t, err, "unexpected status: 403")
}

func TestAttachGitHubAuthPrecedenceAndHostGuard(t *testing.T) {
	t.Setenv("GH_TOKEN", "gho_from_gh")
	t.Setenv("GITHUB_TOKEN", "ghp_from_actions")

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.com/repos/basecamp/hey-cli/releases/latest", nil)
	attachGitHubAuth(req)
	if got := req.Header.Get("Authorization"); got != "Bearer gho_from_gh" {
		t.Errorf("GH_TOKEN should win: got %q", got)
	}

	t.Setenv("GH_TOKEN", "")
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.com/repos/basecamp/hey-cli/releases/latest", nil)
	attachGitHubAuth(req)
	if got := req.Header.Get("Authorization"); got != "Bearer ghp_from_actions" {
		t.Errorf("GITHUB_TOKEN fallback: got %q", got)
	}

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://objects.githubusercontent.com/asset", nil)
	attachGitHubAuth(req)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("token leaked to another host: %q", got)
	}
}
