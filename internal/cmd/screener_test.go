package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

const clearancesHTMLTemplate = `<!DOCTYPE html>
<html>
<head><title>The Screener</title></head>
<body>
<div data-controller="clearances">
%s
</div>
</body>
</html>`

func clearanceItemHTML(postingID, senderEmail, subject, feedBoxID, trailBoxID string) string {
	return fmt.Sprintf(`
<div data-clearances-target="clearance" data-clearance-id="%[1]s">
  <span>%[3]s</span>
  <span>%[2]s</span>
  <form action="/clearances/%[1]s" method="post">
    <input type="hidden" name="_method" value="patch">
    <input type="hidden" name="status" value="approved">
    <button data-clearances-target="screenInButton">Screen in</button>
  </form>
  <form action="/clearances/%[1]s" method="post">
    <input type="hidden" name="_method" value="patch">
    <input type="hidden" name="status" value="approved">
    <input type="hidden" name="designation_box_id" value="%[4]s">
    <button data-clearances-target="feedboxButton">Screen in to Feed</button>
  </form>
  <form action="/clearances/%[1]s" method="post">
    <input type="hidden" name="_method" value="patch">
    <input type="hidden" name="status" value="approved">
    <input type="hidden" name="designation_box_id" value="%[5]s">
    <button data-clearances-target="trailboxButton">Screen in to Paper Trail</button>
  </form>
  <form action="/clearances/%[1]s" method="post">
    <input type="hidden" name="_method" value="patch">
    <input type="hidden" name="status" value="denied">
    <button data-clearances-target="screenOutButton">No</button>
  </form>
  <form action="/clearances/%[1]s" method="post">
    <input type="hidden" name="_method" value="patch">
    <input type="hidden" name="status" value="denied">
    <input type="hidden" name="spam" value="true">
    <button data-clearances-target="spamButton">Spam</button>
  </form>
</div>`,
		postingID, senderEmail, subject, feedBoxID, trailBoxID)
}

type clearanceTestItem struct {
	PostingID string
	Sender    string
	Subject   string
}

func screenerServer(t *testing.T, items []clearanceTestItem) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/clearances.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"pending_clearances_count":%d}`, len(items))

		case r.Method == "GET" && r.URL.Path == "/clearances":
			var itemsHTML string
			for _, item := range items {
				itemsHTML += clearanceItemHTML(item.PostingID, item.Sender, item.Subject, "4848561", "4848564")
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, clearancesHTMLTemplate, itemsHTML)

		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/clearances/"):
			_ = r.ParseForm()
			w.Header().Set("Location", "/clearances")
			w.WriteHeader(302)

		case r.Method == "GET" && r.URL.Path == "/me.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 1}`))

		default:
			w.WriteHeader(404)
		}
	}))
}

func runScreener(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"screener", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestScreenerListEmpty(t *testing.T) {
	server := screenerServer(t, nil)
	defer server.Close()

	resp, err := runScreener(t, server)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "0 pending" {
		t.Errorf("summary = %q, want %q", resp.Summary, "0 pending")
	}
}

func TestScreenerListWithItems(t *testing.T) {
	items := []clearanceTestItem{
		{PostingID: "123456", Sender: "promo@example.com", Subject: "Your order is ready"},
		{PostingID: "789012", Sender: "noreply@shop.com", Subject: "Welcome to our store"},
	}
	server := screenerServer(t, items)
	defer server.Close()

	resp, err := runScreener(t, server)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "2 pending" {
		t.Errorf("summary = %q, want %q", resp.Summary, "2 pending")
	}
	if resp.Data == nil {
		t.Fatal("expected data, got nil")
	}
	dataSlice, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", resp.Data)
	}
	if len(dataSlice) != 2 {
		t.Errorf("expected 2 items, got %d", len(dataSlice))
	}
}

func TestScreenerApprove(t *testing.T) {
	items := []clearanceTestItem{
		{PostingID: "123456", Sender: "promo@example.com", Subject: "Your order"},
	}
	server := screenerServer(t, items)
	defer server.Close()

	resp, err := runScreener(t, server, "approve", "123456")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", resp.Data)
	}
	if data["posting_id"] != "123456" {
		t.Errorf("posting_id = %v, want %q", data["posting_id"], "123456")
	}
	if action, _ := data["action"].(string); action != "Screened in to Imbox" {
		t.Errorf("action = %q, want %q", action, "Screened in to Imbox")
	}
}

func TestScreenerDeny(t *testing.T) {
	items := []clearanceTestItem{
		{PostingID: "123456", Sender: "spam@bad.com", Subject: "You won!"},
	}
	server := screenerServer(t, items)
	defer server.Close()

	resp, err := runScreener(t, server, "deny", "123456")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", resp.Data)
	}
	if action, _ := data["action"].(string); action != "Screened out" {
		t.Errorf("action = %q, want %q", action, "Screened out")
	}
}

func TestScreenerSpam(t *testing.T) {
	items := []clearanceTestItem{
		{PostingID: "123456", Sender: "spam@bad.com", Subject: "Buy now!"},
	}
	server := screenerServer(t, items)
	defer server.Close()

	resp, err := runScreener(t, server, "spam", "123456")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", resp.Data)
	}
	if action, _ := data["action"].(string); action != "Marked as spam" {
		t.Errorf("action = %q, want %q", action, "Marked as spam")
	}
}

func TestScreenerFeed(t *testing.T) {
	items := []clearanceTestItem{
		{PostingID: "123456", Sender: "newsletter@blog.com", Subject: "Weekly digest"},
	}
	server := screenerServer(t, items)
	defer server.Close()

	resp, err := runScreener(t, server, "feed", "123456")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", resp.Data)
	}
	if action, _ := data["action"].(string); action != "Screened in to Feed" {
		t.Errorf("action = %q, want %q", action, "Screened in to Feed")
	}
}

func TestScreenerTrail(t *testing.T) {
	items := []clearanceTestItem{
		{PostingID: "123456", Sender: "receipts@store.com", Subject: "Your receipt"},
	}
	server := screenerServer(t, items)
	defer server.Close()

	resp, err := runScreener(t, server, "trail", "123456")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", resp.Data)
	}
	if action, _ := data["action"].(string); action != "Screened in to Paper Trail" {
		t.Errorf("action = %q, want %q", action, "Screened in to Paper Trail")
	}
}

func TestScreenerApproveNoArgs(t *testing.T) {
	server := screenerServer(t, nil)
	defer server.Close()

	_, err := runScreener(t, server, "approve")
	if err == nil {
		t.Fatal("expected error for missing posting ID")
	}
}

func TestScreenerDenyNoArgs(t *testing.T) {
	server := screenerServer(t, nil)
	defer server.Close()

	_, err := runScreener(t, server, "deny")
	if err == nil {
		t.Fatal("expected error for missing posting ID")
	}
}

func TestScreenerSpamNoArgs(t *testing.T) {
	server := screenerServer(t, nil)
	defer server.Close()

	_, err := runScreener(t, server, "spam")
	if err == nil {
		t.Fatal("expected error for missing posting ID")
	}
}

func TestScreenerFeedNoArgs(t *testing.T) {
	server := screenerServer(t, nil)
	defer server.Close()

	_, err := runScreener(t, server, "feed")
	if err == nil {
		t.Fatal("expected error for missing posting ID")
	}
}

func TestScreenerTrailNoArgs(t *testing.T) {
	server := screenerServer(t, nil)
	defer server.Close()

	_, err := runScreener(t, server, "trail")
	if err == nil {
		t.Fatal("expected error for missing posting ID")
	}
}
