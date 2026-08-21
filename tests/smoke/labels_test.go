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

type smokeFolder struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func TestLabelsAndLabel(t *testing.T) {
	response := heyJSON(t, "labels")
	folders := dataAs[[]smokeFolder](t, response)
	if response.Summary == "" {
		t.Error("labels response omitted summary")
	}
	if len(folders) == 0 {
		skipf(t, "no labels available for label detail validation")
	}

	folder := folders[0]
	detail := heyJSON(t, "label", strconv.FormatInt(folder.ID, 10), "--limit", "2")
	data := dataAs[struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}](t, detail)
	if data.ID != folder.ID || data.Name != folder.Name {
		t.Errorf("label detail = %+v, want %+v", data, folder)
	}

	page := html.UnescapeString(fetchHTML(t, baseURL+"/folders"))
	if !strings.Contains(page, folder.Name) {
		t.Errorf("folder page does not contain %q", folder.Name)
	}
}

func TestLabelMutations(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Disposable label test %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", "This disposable thread verifies label mutations.",
		"--json",
	)
	if code != 0 {
		skipf(t, "could not create a disposable thread (exit %d): %s", code, stderr)
	}
	t.Cleanup(func() { cleanupThreadBySubject(t, subject) })

	postingID, err := waitForPostingIDBySubject(t, subject)
	if err != nil {
		t.Fatalf("could not find disposable thread: %v", err)
	}
	if postingID == 0 {
		skipf(t, "disposable thread did not appear in Imbox")
	}

	labelName := "Smoke label " + uid
	_, stderr, code = hey(t, "label", "create", labelName, strconv.FormatInt(postingID, 10), "--json")
	if code != 0 {
		skipf(t, "label creation unavailable (exit %d): %s", code, stderr)
	}
	t.Cleanup(func() { cleanupLabelByName(t, labelName, postingID) })

	labelID, err := findLabelIDByName(t, labelName)
	if err != nil {
		t.Fatalf("could not list created label: %v", err)
	}
	if labelID == 0 {
		t.Fatalf("created label %q was not listed", labelName)
	}

	assertLabelContainsPosting(t, labelID, postingID, true)
	labelWriteJSON(t, "label", "remove", strconv.FormatInt(postingID, 10), "--from", strconv.FormatInt(labelID, 10))
	assertLabelContainsPosting(t, labelID, postingID, false)
	labelWriteJSON(t, "label", "add", strconv.FormatInt(postingID, 10), "--to", strconv.FormatInt(labelID, 10))
	assertLabelContainsPosting(t, labelID, postingID, true)
}

func labelWriteJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		skipf(t, "label write unavailable (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("invalid label response: %v: %s", err, stdout)
	}
	return response
}

func assertLabelContainsPosting(t *testing.T, labelID, postingID int64, want bool) {
	t.Helper()
	label := dataAs[struct {
		Postings []struct {
			ID int64 `json:"id"`
		} `json:"postings"`
	}](t, heyJSON(t, "label", strconv.FormatInt(labelID, 10), "--all"))
	found := false
	for _, item := range label.Postings {
		if item.ID == postingID {
			found = true
			break
		}
	}
	if found != want {
		t.Errorf("label %d contains posting %d = %v, want %v", labelID, postingID, found, want)
	}
}

func waitForPostingIDBySubject(t *testing.T, subject string) (int64, error) {
	t.Helper()
	var lastErr error
	for range 10 {
		postingID, err := findPostingIDBySubject(t, subject)
		if err == nil && postingID != 0 {
			return postingID, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return 0, lastErr
}

func findPostingIDBySubject(t *testing.T, subject string) (int64, error) {
	t.Helper()
	stdout, stderr, code := hey(t, "box", "imbox", "--all", "--json")
	if code != 0 {
		return 0, fmt.Errorf("list Imbox (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return 0, fmt.Errorf("decode Imbox response: %w", err)
	}
	var box struct {
		Postings []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"postings"`
	}
	if err := json.Unmarshal(response.Data, &box); err != nil {
		return 0, fmt.Errorf("decode Imbox data: %w", err)
	}
	for _, posting := range box.Postings {
		if posting.Name == subject {
			return posting.ID, nil
		}
	}
	return 0, nil
}

func findLabelIDByName(t *testing.T, name string) (int64, error) {
	t.Helper()
	stdout, stderr, code := hey(t, "labels", "--all", "--json")
	if code != 0 {
		return 0, fmt.Errorf("list labels (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return 0, fmt.Errorf("decode labels response: %w", err)
	}
	var labels []smokeFolder
	if err := json.Unmarshal(response.Data, &labels); err != nil {
		return 0, fmt.Errorf("decode labels data: %w", err)
	}
	for _, label := range labels {
		if label.Name == name {
			return label.ID, nil
		}
	}
	return 0, nil
}

func cleanupThreadBySubject(t *testing.T, subject string) {
	t.Helper()
	postingID, err := waitForPostingIDBySubject(t, subject)
	if err != nil {
		t.Errorf("could not locate disposable thread %q for cleanup: %v", subject, err)
		return
	}
	if postingID == 0 {
		t.Errorf("disposable thread %q remained unresolved during cleanup", subject)
		return
	}
	if _, stderr, code := hey(t, "trash", strconv.FormatInt(postingID, 10), "--json"); code != 0 {
		t.Errorf("could not trash disposable thread %d (exit %d): %s", postingID, code, stderr)
	}
}

func cleanupLabelByName(t *testing.T, name string, postingID int64) {
	t.Helper()
	labelID, err := findLabelIDByName(t, name)
	if err != nil {
		t.Errorf("could not locate disposable label %q for cleanup: %v", name, err)
		return
	}
	if labelID == 0 {
		return
	}
	if _, stderr, code := hey(t, "label", "remove", strconv.FormatInt(postingID, 10), "--from", "all", "--json"); code != 0 {
		t.Errorf("could not remove disposable label filings (exit %d): %s", code, stderr)
	}

	var deleteErr error
	for range 2 {
		deleteErr = deleteLabelInBrowser(t, labelID)
		remainingID, listErr := findLabelIDByName(t, name)
		if listErr == nil && remainingID == 0 {
			return
		}
		if listErr != nil {
			deleteErr = listErr
		}
	}
	t.Errorf("disposable label %q (%d) remains after cleanup: %v", name, labelID, deleteErr)
}

func deleteLabelInBrowser(t *testing.T, labelID int64) error {
	t.Helper()
	ctx, ctxCancel, allocCancel := newBrowserContext()
	defer ctxCancel()
	defer allocCancel()

	tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parse base URL: %w", err)
	}
	selector := fmt.Sprintf(`form[action$="/folders/%d"] button`, labelID)
	if err := chromedp.Run(tCtx,
		network.SetCookie("session_token", sessionCookie).
			WithDomain(parsed.Hostname()).
			WithPath("/").
			WithHTTPOnly(true),
		chromedp.Navigate(baseURL+"/folders"),
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.WaitNotPresent(selector, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("delete label %d in browser: %w", labelID, err)
	}
	return nil
}

func TestLabelMutationValidation(t *testing.T) {
	for _, args := range [][]string{
		{"label", "add", "101"},
		{"label", "add", "invalid", "--to", "12"},
		{"label", "create", "Receipts", "invalid"},
		{"label", "remove", "101"},
		{"label", "remove", "invalid", "--from", "all"},
	} {
		heyFail(t, args...)
	}
}
