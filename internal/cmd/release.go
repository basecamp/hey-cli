package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/basecamp/hey-cli/internal/version"
)

// releaseAsset is a downloadable file attached to a GitHub release.
type releaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

// releaseInfo describes the latest GitHub release: its version (tag without
// the "v" prefix) and downloadable assets.
type releaseInfo struct {
	Version string
	Assets  []releaseAsset
}

// asset returns the release asset with the given exact name.
func (r releaseInfo) asset(name string) (releaseAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// releasesLatestURL is swappable so tests can point release fetching at a
// local httptest server.
var releasesLatestURL = "https://api.github.com/repos/basecamp/hey-cli/releases/latest"

// releaseFetcher is the seam through which upgrade and doctor look up the
// latest release; tests replace it.
var releaseFetcher = fetchLatestRelease

// fetchLatestRelease fetches the latest release metadata from GitHub. Callers
// bound ctx themselves: doctor's best-effort check is short, upgrade's is not.
func fetchLatestRelease(ctx context.Context) (releaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesLatestURL, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", version.UserAgent())
	attachGitHubAuth(req)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var release struct {
		TagName string         `json:"tag_name"`
		Assets  []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil { // 1 MB limit
		return releaseInfo{}, err
	}

	return releaseInfo{
		Version: strings.TrimPrefix(release.TagName, "v"),
		Assets:  release.Assets,
	}, nil
}

// attachGitHubAuth adds a bearer token to api.github.com requests when the
// environment carries one (GH_TOKEN, then GITHUB_TOKEN — the gh CLI's
// precedence). Anonymous requests are rate-limited per source IP, which
// bites shared CI runner egress. Host-guarded so a token is never sent to
// any other host (tests point releasesLatestURL at local servers).
func attachGitHubAuth(req *http.Request) {
	if req.URL.Host != "api.github.com" {
		return
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := os.Getenv(name); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			return
		}
	}
}

// isUpdateAvailable reports whether latest is newer than current.
//
// Versions are compared as semantic versions with an optional leading "v".
// This correctly handles prerelease/pseudo versions such as
// "0.4.1-0.20260313174735-243815fa23b2", which should compare newer than
// "0.4.0".
func isUpdateAvailable(current, latest string) bool {
	current = normalizeSemver(current)
	latest = normalizeSemver(latest)
	if !semver.IsValid(current) || !semver.IsValid(latest) {
		return latest != "" && latest != current
	}
	return semver.Compare(latest, current) > 0
}

// isReleaseVersion reports whether v is a semantic version a release could
// have stamped — "dev" and any other non-semver string are not.
func isReleaseVersion(v string) bool {
	return semver.IsValid(normalizeSemver(v))
}

func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
