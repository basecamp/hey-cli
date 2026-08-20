// Package cable connects to HEY's Action Cable server, so commands can be told
// when something changed instead of polling for it.
package cable

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/basecamp/actioncable-go"

	"github.com/basecamp/hey-cli/internal/auth"
)

// Dial connects to the cable server for a HEY base URL, authorizing the upgrade
// request with the same credentials the SDK sends on an API request.
func Dial(ctx context.Context, baseURL string, authMgr *auth.Manager, options ...actioncable.Option) (*actioncable.Client, error) {
	cableURL, err := URL(baseURL)
	if err != nil {
		return nil, err
	}

	header, err := authHeader(ctx, baseURL, authMgr)
	if err != nil {
		return nil, err
	}

	client := actioncable.New(cableURL, append([]actioncable.Option{actioncable.WithHeader(header)}, options...)...)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}

	return client, nil
}

// URL is the cable endpoint for a base URL: https://app.hey.com becomes
// wss://app.hey.com/cable. HEY_CABLE_URL overrides it outright.
func URL(baseURL string) (string, error) {
	if override := os.Getenv("HEY_CABLE_URL"); override != "" {
		return override, nil
	}

	parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("could not read base URL %q: %w", baseURL, err)
	}

	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("base URL %q is neither http nor https", baseURL)
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/cable"
	parsed.RawQuery = ""

	return parsed.String(), nil
}

func authHeader(ctx context.Context, baseURL string, authMgr *auth.Manager) (http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}

	if err := authMgr.AuthenticateRequest(ctx, request); err != nil {
		return nil, err
	}

	return request.Header, nil
}
