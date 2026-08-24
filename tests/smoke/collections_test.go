package smoke_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type smokeCollection struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func TestCollectionsAndCollection(t *testing.T) {
	response := heyJSON(t, "collection", "list")
	collections := dataAs[[]smokeCollection](t, response)
	if response.Summary == "" {
		t.Error("collections response omitted summary")
	}
	if len(collections) == 0 {
		skipf(t, "no collections available for collection detail validation")
	}

	collection := collections[0]
	detail := heyJSON(t, "collection", strconv.FormatInt(collection.ID, 10), "--limit", "2")
	data := dataAs[struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Postings   []any  `json:"postings"`
		TotalCount int    `json:"total_count"`
	}](t, detail)
	if data.ID != collection.ID || data.Name != collection.Name || data.Postings == nil {
		t.Errorf("collection detail = %+v, want %+v with postings", data, collection)
	}

	page := html.UnescapeString(fetchHTML(t, baseURL+"/collections/"+strconv.FormatInt(collection.ID, 10)))
	if !strings.Contains(page, collection.Name) {
		t.Errorf("collection page does not contain %q", collection.Name)
	}
}

func TestCollectionOutputFormats(t *testing.T) {
	collections := dataAs[[]smokeCollection](t, heyJSON(t, "collection", "list"))
	if len(collections) == 0 {
		skipf(t, "no collections available for output validation")
	}
	id := strconv.FormatInt(collections[0].ID, 10)
	for _, args := range [][]string{
		{"collection", "list", "--quiet"},
		{"collection", "list", "--ids-only"},
		{"collection", "list", "--count"},
		{"collection", "list", "--markdown"},
		{"collection", "list", "--styled"},
		{"collection", id, "--quiet"},
		{"collection", id, "--ids-only"},
		{"collection", id, "--count"},
		{"collection", id, "--markdown"},
		{"collection", id, "--styled"},
	} {
		_, stderr, code := hey(t, args...)
		if code != 0 {
			t.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
		}
	}
}

func TestCollectionMutations(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Disposable collection test %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", "This disposable thread verifies collection membership.",
		"--json",
	)
	if code != 0 {
		skipf(t, "could not create a disposable thread (exit %d): %s", code, stderr)
	}
	t.Cleanup(func() { cleanupThreadBySubject(t, subject) })

	postingID, topicID, accountID, err := waitForPostingAndTopicIDsBySubject(t, subject)
	if err != nil {
		t.Fatalf("could not find disposable thread: %v", err)
	}
	if postingID == 0 || topicID == 0 || accountID == 0 {
		skipf(t, "disposable thread did not expose posting, topic, and account IDs")
	}

	collectionName := "Smoke collection " + uid
	_, stderr, code = hey(t, "collection", "create", collectionName, "--summary", "Disposable collection smoke coverage", "--account", strconv.FormatInt(accountID, 10), "--json")
	if code != 0 {
		skipf(t, "collection creation unavailable (exit %d): %s", code, stderr)
	}

	collectionID, err := findCollectionIDByName(t, collectionName)
	if err != nil {
		t.Fatalf("could not list created collection: %v", err)
	}
	if collectionID == 0 {
		t.Fatalf("created collection %q was not listed", collectionName)
	}
	t.Cleanup(func() { cleanupCollection(t, collectionID) })

	updatedName := collectionName + " updated"
	collectionWriteJSON(t, "collection", "update", strconv.FormatInt(collectionID, 10), "--name", updatedName)
	if updatedID, err := findCollectionIDByName(t, updatedName); err != nil || updatedID != collectionID {
		t.Fatalf("updated collection lookup = %d, %v; want %d", updatedID, err, collectionID)
	}

	collectionWriteJSON(t, "collection", "add", strconv.FormatInt(topicID, 10), "--to", strconv.FormatInt(collectionID, 10))
	assertCollectionContainsPosting(t, collectionID, postingID, topicID, true)
	collectionWriteJSON(t, "collection", "remove", strconv.FormatInt(topicID, 10), "--from", strconv.FormatInt(collectionID, 10))
	assertCollectionContainsPosting(t, collectionID, postingID, topicID, false)
}

func collectionWriteJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		skipf(t, "collection write unavailable (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("invalid collection response: %v: %s", err, stdout)
	}
	return response
}

func assertCollectionContainsPosting(t *testing.T, collectionID, postingID, topicID int64, want bool) {
	t.Helper()
	collection := dataAs[struct {
		Postings []struct {
			ID      int64 `json:"id"`
			TopicID int64 `json:"topic_id"`
		} `json:"postings"`
	}](t, heyJSON(t, "collection", strconv.FormatInt(collectionID, 10), "--all"))
	found := false
	for _, item := range collection.Postings {
		if item.ID == postingID && item.TopicID == topicID {
			found = true
			break
		}
	}
	if found != want {
		t.Errorf("collection %d contains posting %d/topic %d = %v, want %v", collectionID, postingID, topicID, found, want)
	}
}

func waitForPostingAndTopicIDsBySubject(t *testing.T, subject string) (int64, int64, int64, error) {
	t.Helper()
	var lastErr error
	for range 10 {
		postingID, topicID, accountID, err := findPostingAndTopicIDsBySubject(t, subject)
		if err == nil && postingID != 0 && topicID != 0 && accountID != 0 {
			return postingID, topicID, accountID, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return 0, 0, 0, lastErr
}

func findPostingAndTopicIDsBySubject(t *testing.T, subject string) (int64, int64, int64, error) {
	t.Helper()
	stdout, stderr, code := hey(t, "box", "imbox", "--all", "--json")
	if code != 0 {
		return 0, 0, 0, fmt.Errorf("list Imbox (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return 0, 0, 0, fmt.Errorf("decode Imbox response: %w", err)
	}
	var box struct {
		Postings []struct {
			ID        int64  `json:"id"`
			AccountID int64  `json:"account_id"`
			Name      string `json:"name"`
			AppURL    string `json:"app_url"`
		} `json:"postings"`
	}
	if err := json.Unmarshal(response.Data, &box); err != nil {
		return 0, 0, 0, fmt.Errorf("decode Imbox data: %w", err)
	}
	for _, posting := range box.Postings {
		if posting.Name != subject {
			continue
		}
		topicID, err := topicIDFromAppURL(posting.AppURL)
		if err != nil {
			return posting.ID, 0, posting.AccountID, err
		}
		return posting.ID, topicID, posting.AccountID, nil
	}
	return 0, 0, 0, nil
}

func topicIDFromAppURL(appURL string) (int64, error) {
	parsed, err := url.Parse(appURL)
	if err != nil {
		return 0, err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "topics" {
			return strconv.ParseInt(parts[i+1], 10, 64)
		}
	}
	return 0, fmt.Errorf("posting app URL %q has no topic ID", appURL)
}

func findCollectionIDByName(t *testing.T, name string) (int64, error) {
	t.Helper()
	stdout, stderr, code := hey(t, "collection", "list", "--all", "--json")
	if code != 0 {
		return 0, fmt.Errorf("list collections (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return 0, fmt.Errorf("decode collections response: %w", err)
	}
	var collections []smokeCollection
	if err := json.Unmarshal(response.Data, &collections); err != nil {
		return 0, fmt.Errorf("decode collections data: %w", err)
	}
	for _, collection := range collections {
		if collection.Name == name {
			return collection.ID, nil
		}
	}
	return 0, nil
}

func cleanupCollection(t *testing.T, collectionID int64) {
	t.Helper()
	ctx, ctxCancel, allocCancel := newBrowserContext()
	defer ctxCancel()
	defer allocCancel()

	tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Errorf("parse base URL: %v", err)
		return
	}
	path := fmt.Sprintf("/collections/%d", collectionID)
	selector := fmt.Sprintf(`form[action$="%s"] input[type="submit"]`, path)
	if err := chromedp.Run(tCtx,
		network.SetCookie("session_token", sessionCookie).
			WithDomain(parsed.Hostname()).
			WithPath("/").
			WithHTTPOnly(true),
		chromedp.Navigate(baseURL+path+"/status"),
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.WaitNotPresent(selector, chromedp.ByQuery),
	); err != nil {
		t.Errorf("could not delete disposable collection %d: %v", collectionID, err)
		return
	}
	for range 5 {
		collections := dataAs[[]smokeCollection](t, heyJSON(t, "collection", "list", "--all"))
		found := false
		for _, collection := range collections {
			if collection.ID == collectionID {
				found = true
				break
			}
		}
		if !found {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("disposable collection %d remains after cleanup", collectionID)
}

func TestCollectionMutationValidation(t *testing.T) {
	for _, args := range [][]string{
		{"collection", "create", "   "},
		{"collection", "update", "12"},
		{"collection", "add", "501"},
		{"collection", "add", "invalid", "--to", "12"},
		{"collection", "remove", "501"},
		{"collection", "remove", "invalid", "--from", "12"},
	} {
		heyFail(t, args...)
	}
}
