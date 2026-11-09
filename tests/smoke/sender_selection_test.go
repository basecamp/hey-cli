package smoke_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type smokeSender struct {
	ID        int64  `json:"id"`
	AccountID int64  `json:"account_id"`
	Email     string `json:"email"`
	Default   bool   `json:"default"`
}

type senderPreview struct {
	AccountID int64 `json:"account_id"`
	From      struct {
		ID    int64  `json:"id"`
		Email string `json:"email_address"`
	} `json:"from"`
	To  []string `json:"to"`
	CC  []string `json:"cc"`
	BCC []string `json:"bcc"`
}

type senderSent struct {
	ID      int64 `json:"id"`
	TopicID int64 `json:"topic_id"`
}

// TestSenderSelection requires an explicitly chosen non-default sender; it does not
// detect external SMTP/Gmail/Outlook routes. Configure local SMTP senders as README.md
// describes before running it.
func TestSenderSelection(t *testing.T) {
	from := os.Getenv("HEY_SMOKE_FROM")
	if from == "" {
		skipf(t, "set HEY_SMOKE_FROM to a locally deliverable custom-domain sender")
	}
	account := os.Getenv("HEY_SMOKE_SENDER_ACCOUNT")
	senders := dataAs[[]smokeSender](t, heyJSON(t, "account", "senders"))
	var selected, primary, other smokeSender
	for _, sender := range senders {
		if strings.EqualFold(sender.Email, from) && (account == "" || strconv.FormatInt(sender.AccountID, 10) == account) {
			if selected.ID != 0 {
				t.Fatal("HEY_SMOKE_FROM is ambiguous; set HEY_SMOKE_SENDER_ACCOUNT")
			}
			selected = sender
		}
	}
	if selected.ID == 0 {
		t.Fatalf("configured sender %q not found", from)
	}
	account = strconv.FormatInt(selected.AccountID, 10)
	for _, sender := range senders {
		if sender.AccountID == selected.AccountID && (primary.ID == 0 || sender.Default) {
			primary = sender
		}
		if sender.AccountID != selected.AccountID {
			other = sender
		}
	}
	if primary.ID == 0 || primary.ID == selected.ID {
		t.Fatal("sender verification needs a different account-default sender")
	}
	if other.ID == 0 {
		skipf(t, "sender verification needs another linked account for boundary checks")
	}

	subject := "Kitchen renovation schedule " + uniqueID()
	t.Cleanup(func() {
		rows := dataAs[[]struct {
			ID      int64  `json:"id"`
			Subject string `json:"subject"`
		}](t, heyJSON(t, "search", "--exact", subject, "--all"))
		args := []string{"trash"}
		for _, row := range rows {
			if row.ID > 0 && strings.TrimPrefix(row.Subject, "Re: ") == subject {
				args = append(args, strconv.FormatInt(row.ID, 10))
			}
		}
		if len(args) > 1 {
			heyOK(t, args...)
		}
	})
	origin := dataAs[senderSent](t, heyJSON(t, "compose", "--account", account,
		"--from", strconv.FormatInt(primary.ID, 10), "--to", "alice@example.com",
		"--cc", "bob@example.org", "--bcc", "carol@example.org", "--subject", subject,
		"-m", "Can we review the cabinet delivery on Friday?"))
	if origin.ID <= 0 || origin.TopicID <= 0 {
		t.Fatalf("origin send did not identify its message and thread: %+v", origin)
	}
	topic := strconv.FormatInt(origin.TopicID, 10)
	waitForSenderMessage(t, origin, primary.Email)
	baseline := dataAs[senderPreview](t, heyJSON(t, "reply", topic, "--dry-run"))
	if baseline.From.ID == 0 || baseline.From.Email == "" || baseline.AccountID != selected.AccountID {
		t.Fatalf("incomplete default reply envelope: %+v", baseline)
	}
	beforeDrafts := draftListedIDs(t)
	for _, selector := range []string{from, strconv.FormatInt(selected.ID, 10)} {
		preview := dataAs[senderPreview](t, heyJSON(t, "reply", topic, "--from", selector, "--dry-run"))
		if preview.From.ID != selected.ID || !strings.EqualFold(preview.From.Email, from) || preview.AccountID != selected.AccountID {
			t.Fatalf("wrong selected preview: %+v", preview)
		}
		if !reflect.DeepEqual(preview.To, baseline.To) || !reflect.DeepEqual(preview.CC, baseline.CC) || !reflect.DeepEqual(preview.BCC, baseline.BCC) {
			t.Fatalf("sender selection changed recipients: before=%+v after=%+v", baseline, preview)
		}
	}
	if !reflect.DeepEqual(beforeDrafts, draftListedIDs(t)) {
		t.Fatal("dry runs created or removed drafts")
	}

	type outcome struct {
		name, email, body string
		sent              senderSent
		want              senderPreview
	}
	outcomes := []outcome{{name: "origin", email: primary.Email, body: "Can we review the cabinet delivery on Friday?", sent: origin,
		want: senderPreview{To: []string{"alice@example.com"}, CC: []string{"bob@example.org"}, BCC: []string{"carol@example.org"}}}}
	for _, command := range []string{"compose", "reply", "compose-thread"} {
		for _, selector := range []string{from, strconv.FormatInt(selected.ID, 10)} {
			for _, draft := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/draft=%v", command, selector, draft)
				t.Run(name, func(t *testing.T) {
					body := "The cabinet delivery is confirmed for Friday. " + uniqueID()
					want := senderPreview{To: []string{"alice@example.com"}}
					if command != "compose" {
						want = dataAs[senderPreview](t, heyJSON(t, "reply", topic, "--dry-run"))
					}
					args := []string{command}
					switch command {
					case "compose":
						args = append(args, "--account", account, "--to", "alice@example.com", "--subject", subject)
					case "reply":
						args = append(args, topic)
					case "compose-thread":
						args = []string{"compose", "--thread-id", topic}
					}
					args = append(args, "--from", selector, "-m", body)
					var sent senderSent
					if draft {
						d := dataAs[draftData](t, heyJSON(t, append(args, "--draft")...))
						id := strconv.FormatInt(d.ID, 10)
						delivered := false
						t.Cleanup(func() {
							if !delivered {
								_, _, _ = hey(t, "draft", "delete", id)
							}
						})
						state := dataAs[struct {
							From string `json:"from"`
						}](t, heyJSON(t, "draft", "show", id))
						if !strings.EqualFold(state.From, from) {
							t.Fatalf("saved draft sender = %q, want %q", state.From, from)
						}
						assertBrowserDraftSender(t, d.ID, selected.ID)
						sent = dataAs[senderSent](t, heyJSON(t, "draft", "send", id))
						delivered = true
					} else {
						sent = dataAs[senderSent](t, heyJSON(t, args...))
					}
					if sent.ID <= 0 || sent.TopicID <= 0 {
						t.Fatalf("send did not identify its message and thread: %+v", sent)
					}
					outcomes = append(outcomes, outcome{name: name, email: from, body: body, sent: sent, want: want})
				})
			}
		}
	}

	t.Run("saved-default-account", func(t *testing.T) {
		accounts := dataAs[[]struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		}](t, heyJSON(t, "account", "list"))
		previous := "all"
		for _, row := range accounts {
			if row.Active {
				previous = row.ID
			}
		}
		heyOK(t, "account", "use", strconv.FormatInt(other.AccountID, 10))
		t.Cleanup(func() { heyOK(t, "account", "use", previous) })
		preview := dataAs[senderPreview](t, heyJSON(t, "reply", topic, "--from", from, "--dry-run"))
		if preview.AccountID != selected.AccountID || preview.From.ID != selected.ID {
			t.Fatalf("saved account overrode reply preview: %+v", preview)
		}
		for _, command := range [][]string{{"reply", topic}, {"compose", "--thread-id", topic}} {
			want := dataAs[senderPreview](t, heyJSON(t, "reply", topic, "--dry-run"))
			body := "Please keep the Friday cabinet delivery. " + uniqueID()
			args := append(append([]string{}, command...), "--from", from, "-m", body)
			sent := dataAs[senderSent](t, heyJSON(t, args...))
			if sent.ID <= 0 || sent.TopicID <= 0 {
				t.Fatalf("send did not identify message and thread: %+v", sent)
			}
			outcomes = append(outcomes, outcome{name: command[0] + "/saved-default-account", email: from, body: body, sent: sent, want: want})
		}
	})

	// Omitting --from must retain HEY's default selection on both reply paths.
	for _, command := range [][]string{{"reply", topic}, {"compose", "--thread-id", topic}, {"compose", "--account", account, "--subject", subject, "--to", "alice@example.com"}} {
		current := dataAs[senderPreview](t, heyJSON(t, "reply", topic, "--dry-run"))
		email := current.From.Email
		if command[0] == "compose" && command[1] == "--account" {
			email = primary.Email
			current = senderPreview{To: []string{"alice@example.com"}}
		}
		body := "Friday still works for the renovation review. " + uniqueID()
		sent := dataAs[senderSent](t, heyJSON(t, append(command, "-m", body)...))
		outcomes = append(outcomes, outcome{name: strings.Join(command, " ") + "/default", email: email, body: body, sent: sent, want: current})
	}

	t.Run("invalid-senders", func(t *testing.T) {
		before := draftListedIDs(t)
		for _, command := range [][]string{{"reply", topic}, {"compose", "--thread-id", topic}} {
			for _, selector := range []string{" ", "unconfigured@example.org", strconv.FormatInt(other.ID, 10)} {
				args := append(append([]string{}, command...), "--from", selector, "--draft", "--attach", "/missing/renovation.pdf")
				_, stderr := heyFail(t, args...)
				if strings.Contains(stderr, "attachment") {
					t.Errorf("attachment handling preceded sender validation: %s", stderr)
				}
			}
			args := append(append([]string{}, command...), "--account", strconv.FormatInt(other.AccountID, 10), "--from", from, "--draft", "-m", "Friday works.")
			heyFail(t, args...)
		}
		if !reflect.DeepEqual(before, draftListedIDs(t)) {
			t.Fatal("invalid sender created a draft")
		}
	})

	if len(outcomes) != 18 {
		t.Errorf("created %d of 18 expected sender scenarios", len(outcomes))
	}
	// Verify after queuing all sends, so their Undo Send windows elapse together.
	for _, result := range outcomes {
		t.Run("read-back/"+result.name, func(t *testing.T) {
			waitForSenderMessage(t, result.sent, result.email)
			assertSavedRecipients(t, result.sent, result.want)
			assertCapturedSender(t, subject, result.body, result.email)
		})
	}
}

func waitForSenderMessage(t *testing.T, sent senderSent, wantFrom string) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for {
		stdout, stderr, code := hey(t, "thread", "read", strconv.FormatInt(sent.TopicID, 10), "--json")
		if code == 0 {
			var response struct {
				Data []struct {
					ID      int64 `json:"id"`
					Creator struct {
						Email string `json:"email_address"`
					} `json:"creator"`
					Sender *struct {
						Email string `json:"email_address"`
					} `json:"sender"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &response); err != nil {
				t.Fatal(err)
			}
			for _, entry := range response.Data {
				if entry.ID == sent.ID {
					actual := entry.Creator.Email
					if entry.Sender != nil {
						actual = entry.Sender.Email
					}
					if !strings.EqualFold(actual, wantFrom) {
						t.Fatalf("saved message %d From = %q, want %q", sent.ID, actual, wantFrom)
					}
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("message %d did not appear after Undo Send: exit=%d stderr=%s", sent.ID, code, stderr)
		}
		time.Sleep(time.Second)
	}
}

func assertSavedRecipients(t *testing.T, sent senderSent, want senderPreview) {
	t.Helper()
	out := heyOK(t, "thread", "read", strconv.FormatInt(sent.TopicID, 10), "--jq",
		fmt.Sprintf(".data[] | select(.id == %d) | .recipients", sent.ID))
	var recipients map[string][]struct {
		Email string `json:"email_address"`
	}
	if err := json.Unmarshal([]byte(out), &recipients); err != nil {
		t.Fatal(err)
	}
	for line, expected := range map[string][]string{"to": want.To, "cc": want.CC, "bcc": want.BCC} {
		actual := make([]string, 0, len(recipients[line]))
		for _, recipient := range recipients[line] {
			actual = append(actual, recipient.Email)
		}
		if strings.Join(actual, ",") != strings.Join(expected, ",") {
			t.Errorf("saved %s = %v, want %v", line, actual, expected)
		}
	}
}

func assertBrowserDraftSender(t *testing.T, draftID, senderID int64) {
	t.Helper()
	ctx, cancel, allocCancel := newBrowserContext()
	defer cancel()
	defer allocCancel()
	ctx, timeout := context.WithTimeout(ctx, 30*time.Second)
	defer timeout()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	var selected string
	if err := chromedp.Run(ctx,
		network.SetCookie("session_token", sessionCookie).WithDomain(parsed.Hostname()).WithPath("/").WithHTTPOnly(true),
		chromedp.Navigate(fmt.Sprintf("%s/messages/%d/edit", baseURL, draftID)),
		chromedp.WaitReady(`[name="acting_sender_id"]`, chromedp.ByQuery),
		chromedp.Value(`[name="acting_sender_id"]`, &selected, chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
	if selected != strconv.FormatInt(senderID, 10) {
		t.Fatalf("browser draft From selected sender %q, want %d", selected, senderID)
	}
}

// Mailpit captures the real SMTP DATA, independently of HEY's JSON and HTML views.
func assertCapturedSender(t *testing.T, subject, body, wantFrom string) {
	t.Helper()
	capture := os.Getenv("HEY_SMOKE_MAILPIT_URL")
	if capture == "" {
		skipf(t, "set HEY_SMOKE_MAILPIT_URL to verify actual outgoing From headers")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) []byte {
		t.Helper()
		resp, err := client.Get(strings.TrimRight(capture, "/") + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Mailpit %s: HTTP %d", path, resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	deadline := time.Now().Add(40 * time.Second)
	for {
		var listing struct {
			Messages []struct{ ID, Subject string } `json:"messages"`
		}
		if err := json.Unmarshal(get("/api/v1/search?query="+url.QueryEscape(`subject:"`+subject+`"`)+"&limit=200"), &listing); err != nil {
			t.Fatal(err)
		}
		for _, item := range listing.Messages {
			if !strings.Contains(item.Subject, subject) {
				continue
			}
			path := "/api/v1/message/" + url.PathEscape(item.ID)
			var detail struct{ Text string }
			if err := json.Unmarshal(get(path), &detail); err != nil {
				t.Fatal(err)
			}
			// HEY wraps plain text at word boundaries (including a hyphen in the
			// unique marker). Match only the leading body, never a quoted ancestor.
			if !strings.HasPrefix(strings.Join(strings.Fields(detail.Text), ""), strings.Join(strings.Fields(body), "")) {
				continue
			}
			message, err := mail.ReadMessage(strings.NewReader(string(get(path + "/raw"))))
			if err != nil {
				t.Fatal(err)
			}
			from, err := mail.ParseAddress(message.Header.Get("From"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.EqualFold(from.Address, wantFrom) {
				t.Fatalf("captured From = %q, want %q", from.Address, wantFrom)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no captured SMTP message with subject %q and body %q (searched %d messages)", subject, body, len(listing.Messages))
		}
		time.Sleep(time.Second)
	}
}
