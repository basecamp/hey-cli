package cmd

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const senderIdentity = `{"accounts":[{"id":9,"status":"active"},{"id":8,"status":"active"}],"senders":[{"id":77,"account_id":9,"email_address":"personal@example.org","name_tag":"<div>Personal</div>"},{"id":88,"account_id":9,"email_address":"billing@example.org","name_tag":"<div>Billing</div>"}]}`

func senderServer(t *testing.T, identity string, writes *[]draftWrite, alter func(map[string]any)) http.Handler {
	t.Helper()
	var saved map[string]any
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "identity"):
			fmt.Fprint(w, identity)
		case r.Method == "POST" && r.URL.Path == "/messages.json", r.Method == "PUT" && r.URL.Path == "/messages/12345.json":
			if r.URL.Query().Get("filtered_account_id") != "9" {
				t.Error("creation was not account scoped")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			*writes = append(*writes, draftWrite{Method: r.Method, Path: r.URL.Path, Body: body})
			saved = body
			w.Header().Set("Location", "/messages/12345")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "GET" && r.URL.Path == "/messages/12345/edit.json":
			state := map[string]any{"id": 12345, "subject": "Board update", "content": "<p>Numbers.</p><br><div>Personal</div>", "creator": map[string]any{"id": 77, "account_id": 9}, "sender": map[string]any{"id": 77, "account_id": 9, "email_address": "personal@example.org"}, "addressed": map[string]any{"directly": []any{map[string]any{"email_address": "maria@example.com"}}}}
			if saved != nil {
				message := saved["message"].(map[string]any)
				state["subject"] = message["subject"]
				state["content"] = message["content"]
				state["sender"] = map[string]any{"id": saved["acting_sender_id"], "account_id": 9, "email_address": "billing@example.org"}
				entry := saved["entry"].(map[string]any)
				addressed := map[string]any{}
				for k, v := range entry["addressed"].(map[string]any) {
					contacts := []any{}
					if values, ok := v.([]any); ok {
						for _, a := range values {
							contacts = append(contacts, map[string]any{"email_address": a})
						}
					}
					addressed[k] = contacts
				}
				state["addressed"] = addressed
			}
			if alter != nil {
				alter(state)
			}
			if err := json.NewEncoder(w).Encode(state); err != nil {
				t.Fatal(err)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func TestComposeFromDraftAndSend(t *testing.T) {
	for _, draft := range []bool{true, false} {
		t.Run(fmt.Sprint(draft), func(t *testing.T) {
			var writes []draftWrite
			args := []string{"compose", "--from", " Billing@Example.ORG ", "--subject", "Board update", "--to", "maria@example.com", "--cc", "priya@example.org", "--bcc", "sam@example.org", "-m", "Numbers."}
			if draft {
				args = append(args, "--draft")
			}
			_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, nil), args...)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if draft {
				want = 1
			}
			if len(writes) != want {
				t.Fatalf("writes: %+v", writes)
			}
			for _, write := range writes {
				if write.Body["acting_sender_id"] != float64(88) {
					t.Fatalf("wrong sender: %+v", write)
				}
				if write.Body["message"].(map[string]any)["content"] != "<p>Numbers.</p><br><div>Billing</div>" {
					t.Fatalf("wrong body: %+v", write)
				}
			}
			if writes[0].Body["entry"].(map[string]any)["status"] != "drafted" {
				t.Fatal("creation sent")
			}
			if !draft && writes[1].Body["entry"].(map[string]any)["status"] == "drafted" {
				t.Fatal("delivery not requested")
			}
		})
	}
}

func TestComposeFromRejectsUnsafeSenderBeforeUpload(t *testing.T) {
	for _, tc := range []struct {
		name, identity, from string
		account              string
	}{
		{"unknown", senderIdentity, "unknown@example.org", ""},
		{"empty", senderIdentity, " ", ""},
		{"duplicate", strings.Replace(senderIdentity, `"id":77,"account_id":9,"email_address":"personal@example.org"`, `"id":77,"account_id":9,"email_address":"billing@example.org"`, 1), "billing@example.org", ""},
		{"wrong-account", senderIdentity, "billing@example.org", "8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes []draftWrite
			args := []string{"compose", "--from", tc.from, "--subject", "Board update", "-m", "Numbers.", "--draft", "--attach", "/missing/attachment.pdf"}
			if tc.account != "" {
				args = append(args, "--account", tc.account)
			}
			_, err := runJSONCommand(t, senderServer(t, tc.identity, &writes, nil), args...)
			if err == nil || strings.Contains(err.Error(), "attachment") {
				t.Fatalf("sender preflight failed: %v", err)
			}
			if len(writes) != 0 {
				t.Fatal("unsafe write")
			}
		})
	}
}

func TestComposeFromRefusesChangedReadback(t *testing.T) {
	for _, field := range []string{"id", "sender", "subject", "content", "addressed", "is_reply", "scheduled_delivery_at"} {
		t.Run(field, func(t *testing.T) {
			var writes []draftWrite
			alter := func(s map[string]any) {
				switch field {
				case "id":
					s[field] = 54321
				case "sender":
					s[field] = map[string]any{"id": 77}
				case "addressed":
					s[field] = map[string]any{}
				case "is_reply":
					s[field] = true
				case "scheduled_delivery_at":
					s[field] = "2026-10-01T12:00:00Z"
				default:
					s[field] = "changed"
				}
			}
			_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, alter), "compose", "--from", "billing@example.org", "--subject", "Board update", "--to", "maria@example.com", "-m", "Numbers.")
			if err == nil || len(writes) != 1 {
				t.Fatalf("err=%v writes=%+v", err, writes)
			}
		})
	}
}

func TestDraftEditFromPreservesFieldsAndQuote(t *testing.T) {
	var writes []draftWrite
	quote := `<div><br><br></div><div>On Friday, Maria wrote:</div><blockquote><div>Personal</div></blockquote>`
	alter := func(s map[string]any) {
		s["content"] = `<p>Numbers.</p><br><div>Personal</div>` + quote
		s["scheduled_delivery_at"] = "2026-10-01T12:00:00Z"
	}
	_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, alter), "draft", "edit", "12345", "--from", "billing@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes=%+v", writes)
	}
	body := writes[0].Body
	if body["acting_sender_id"] != float64(88) {
		t.Fatal("sender not changed")
	}
	message := body["message"].(map[string]any)
	if message["content"] != `<p>Numbers.</p><br><div>Personal</div>`+quote || message["subject"] != "Board update" {
		t.Fatalf("content changed: %+v", message)
	}
	entry := body["entry"].(map[string]any)
	if entry["status"] != "drafted" || entry["scheduled_delivery_at_date"] != "2026-10-01" || entry["scheduled_delivery_at_hour"] != "12" {
		t.Fatalf("schedule changed: %+v", entry)
	}
}

func TestDraftEditFromAccountRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, from string
		alter      func(map[string]any)
	}{

		{"unknown", "unknown@example.org", nil},
		{"missing-account", "billing@example.org", func(s map[string]any) {
			s["creator"] = map[string]any{"id": 77}
			s["sender"] = map[string]any{"id": 77}
		}},
		{"wrong-account", "billing@example.org", func(s map[string]any) { s["creator"] = map[string]any{"id": 77, "account_id": 8} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes []draftWrite
			_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, tc.alter), "draft", "edit", "12345", "--from", tc.from)
			if err == nil || len(writes) != 0 {
				t.Fatalf("err=%v writes=%+v", err, writes)
			}
		})
	}
}

func TestSavedHTMLLosslessEnvelope(t *testing.T) {
	expected := `<p>Please <a href="https://example.org/review">review</a>.</p>`
	envelope := func(body string) string {
		data, err := json.Marshal(map[string]string{"contentType": "text/html", "content": "<shadow-content><template>" + body + "</template></shadow-content>"})
		if err != nil {
			t.Fatal(err)
		}
		return `<div><figure data-trix-attachment="` + stdhtml.EscapeString(string(data)) + `"></figure></div>`
	}
	if !sameSavedMessageHTML(expected, envelope(expected)) {
		t.Fatal("lossless envelope refused")
	}
	if sameSavedMessageHTML(expected, envelope(strings.ReplaceAll(expected, "example.org", "other.example.org"))) {
		t.Fatal("changed link accepted")
	}
	if sameSavedMessageHTML(expected, strings.Replace(envelope(expected), "</figure>", "<p>Unexpected</p></figure>", 1)) {
		t.Fatal("extra figure content accepted")
	}
	if sameSavedMessageHTML(expected, envelope(expected)+"<p>Unexpected</p>") {
		t.Fatal("extra body accepted")
	}
}

func TestComposeFromAcceptsLosslessEnvelope(t *testing.T) {
	var writes []draftWrite
	alter := func(s map[string]any) {
		data, err := json.Marshal(map[string]string{"contentType": "text/html", "content": "<shadow-content><template>" + s["content"].(string) + "</template></shadow-content>"})
		if err != nil {
			t.Fatal(err)
		}
		s["content"] = `<div><figure data-trix-attachment="` + stdhtml.EscapeString(string(data)) + `"></figure></div>`
	}
	_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, alter), "compose", "--from", "billing@example.org", "--subject", "Board update", "--to", "maria@example.com", "-m", "Numbers.")
	if err != nil || len(writes) != 2 {
		t.Fatalf("err=%v writes=%+v", err, writes)
	}
}

func TestComposeFromNoNameTagAndSenderID(t *testing.T) {
	for _, draft := range []bool{true, false} {
		t.Run(fmt.Sprint(draft), func(t *testing.T) {
			var writes []draftWrite
			args := []string{"compose", "--from", "88", "--subject", "Board update", "--to", "maria@example.com", "-m", "Numbers.", "--no-name-tag"}
			if draft {
				args = append(args, "--draft")
			}
			_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, nil), args...)
			if err != nil {
				t.Fatal(err)
			}
			for _, write := range writes {
				if write.Body["acting_sender_id"] != float64(88) || write.Body["message"].(map[string]any)["content"] != "<p>Numbers.</p>" {
					t.Fatalf("unexpected payload: %+v", write)
				}
			}
		})
	}
}

func TestSenderListingAndScopedResolution(t *testing.T) {
	identity := strings.Replace(senderIdentity, `"senders":[`, `"senders":[{"id":99,"account_id":8,"email_address":"billing@example.org","default":true,"name_tag":"<div>Private tag</div>"},`, 1)
	for _, tc := range []struct {
		account string
		count   int
	}{{"", 3}, {"9", 2}, {"8", 1}} {
		t.Run(tc.account, func(t *testing.T) {
			var writes []draftWrite
			args := []string{"account", "senders"}
			if tc.account != "" {
				args = append(args, "--account", tc.account)
			}
			response, err := runJSONCommand(t, senderServer(t, identity, &writes, nil), args...)
			if err != nil {
				t.Fatal(err)
			}
			rows := response.Data.([]any)
			if len(rows) != tc.count || len(writes) != 0 {
				t.Fatalf("rows=%v writes=%v", rows, writes)
			}
			for _, row := range rows {
				item := row.(map[string]any)
				if len(item) != 4 || item["id"] == nil || item["account_id"] == nil || item["email"] == nil || item["default"] == nil {
					t.Fatalf("unexpected listing: %v", item)
				}
			}
		})
	}
	for _, account := range []string{"", "9"} {
		t.Run("resolve-"+account, func(t *testing.T) {
			var writes []draftWrite
			args := []string{"compose", "--from", "billing@example.org", "--subject", "Board update", "-m", "Numbers.", "--draft"}
			if account != "" {
				args = append(args, "--account", account)
			}
			_, err := runJSONCommand(t, senderServer(t, identity, &writes, nil), args...)
			if account == "" {
				if err == nil || len(writes) != 0 {
					t.Fatalf("ambiguous sender accepted: %v", err)
				}
			} else if err != nil || len(writes) != 1 {
				t.Fatalf("scoped sender refused: %v", err)
			}
		})
	}
}

func TestSenderListingEmptyAndUnavailable(t *testing.T) {
	identity := `{"accounts":[{"id":9,"status":"disabled"}],"senders":[{"id":88,"account_id":9,"email_address":"billing@example.org"}]}`
	var writes []draftWrite
	response, err := runJSONCommand(t, senderServer(t, identity, &writes, nil), "account", "senders")
	if err != nil || len(response.Data.([]any)) != 0 {
		t.Fatalf("response=%v err=%v", response, err)
	}
	_, err = runJSONCommand(t, senderServer(t, identity, &writes, nil), "compose", "--from", "88", "--subject", "Board update", "-m", "Numbers.", "--draft")
	if err == nil || len(writes) != 0 {
		t.Fatal("unavailable account accepted")
	}
}

func TestComposeFromDoesNotRetryAmbiguousDelivery(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var writes []draftWrite
			handler := senderServer(t, senderIdentity, &writes, nil)
			sends := 0
			wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					sends++
					if r.URL.Query().Get("filtered_account_id") != "9" {
						t.Error("delivery was not account scoped")
					}
					w.Header().Set("Retry-After", "1")
					http.Error(w, "delivery uncertain", status)
					return
				}
				handler.ServeHTTP(w, r)
			})
			_, err := runJSONCommand(t, wrapped, "compose", "--from", "billing@example.org", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers.")
			if err == nil || !strings.Contains(err.Error(), "12345") || sends != 1 || len(writes) != 1 {
				t.Fatalf("err=%v sends=%d writes=%v", err, sends, writes)
			}
		})
	}
}

func TestDraftEditFromKeepsWrappedBody(t *testing.T) {
	var writes []draftWrite
	original := `<div><figure data-trix-attachment='{"contentType":"text/html","content":"<shadow-content><template><p>Numbers.</p><br><div>Personal</div></template></shadow-content>"}'></figure></div>`
	alter := func(s map[string]any) { s["content"] = original }
	_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, alter), "draft", "edit", "12345", "--from", "88")
	if err != nil || len(writes) != 1 {
		t.Fatalf("err=%v writes=%v", err, writes)
	}
	body := writes[0].Body
	if body["acting_sender_id"] != float64(88) || body["message"].(map[string]any)["content"] != original {
		t.Fatal("sender or body changed unexpectedly")
	}
}

func TestDraftShowIncludesSelectedSender(t *testing.T) {
	var writes []draftWrite
	response, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, nil), "draft", "show", "12345")
	if err != nil {
		t.Fatal(err)
	}
	if response.Data.(map[string]any)["from"] != "personal@example.org" {
		t.Fatalf("missing sender: %+v", response.Data)
	}
}

func TestComposeFromCanonicalMarkdown(t *testing.T) {
	var writes []draftWrite
	actual := `<div>Numbers.</div><br><div>Billing</div>`
	_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, func(s map[string]any) { s["content"] = actual }),
		"compose", "--from", "88", "--subject", "Board update", "--to", "maria@example.com", "-m", "Numbers.")
	if err != nil || len(writes) != 2 {
		t.Fatalf("err=%v writes=%v", err, writes)
	}
	if writes[1].Method != "PUT" || writes[1].Body["message"].(map[string]any)["content"] != actual {
		t.Fatal("delivery must use verified server content")
	}
}

func TestDraftWritesPreserveCreatorSender(t *testing.T) {
	identity := strings.Replace(senderIdentity, `"senders":[`, `"senders":[{"id":42,"account_id":9,"email_address":"default@example.org","default":true},`, 1)
	for _, args := range [][]string{{"draft", "edit", "12345", "--subject", "Updated board figures"}, {"draft", "send", "12345"}} {
		t.Run(args[1], func(t *testing.T) {
			var writes []draftWrite
			_, err := runJSONCommand(t, senderServer(t, identity, &writes, func(s map[string]any) { delete(s, "sender") }), append(args, "--account", "9")...)
			if err != nil || len(writes) != 1 {
				t.Fatalf("err=%v writes=%v", err, writes)
			}
			if writes[0].Body["acting_sender_id"] != float64(77) {
				t.Fatalf("creator sender lost: %v", writes[0])
			}
		})
	}
}

func TestComposeFromValidatesBeforeInput(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			previous := stdinIsTerminal
			stdinIsTerminal = func() bool { return terminal }
			t.Cleanup(func() { stdinIsTerminal = previous })
			marker := filepath.Join(t.TempDir(), "editor-ran")
			script := filepath.Join(t.TempDir(), "editor")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf 'Numbers.' > \"$1\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("EDITOR", script)
			t.Setenv("VISUAL", script)
			input, err := os.CreateTemp(t.TempDir(), "stdin")
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if _, err := input.WriteString("Numbers."); err != nil {
				t.Fatal(err)
			}
			if _, err := input.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			old := os.Stdin
			os.Stdin = input
			t.Cleanup(func() { os.Stdin = old })
			var writes []draftWrite
			_, err = runJSONCommand(t, senderServer(t, senderIdentity, &writes, nil), "compose", "--from", "unknown@example.org", "--subject", "Board update", "--draft")
			if err == nil || !strings.Contains(err.Error(), "--from") || len(writes) != 0 {
				t.Fatalf("err=%v writes=%v", err, writes)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("editor opened before sender validation")
			}
			if offset, err := input.Seek(0, 1); err != nil || offset != 0 {
				t.Fatalf("stdin consumed before validation: %d, %v", offset, err)
			}
		})
	}
}

func TestSavedHTMLCanonicalization(t *testing.T) {
	upload := `<action-text-attachment sgid="sgid-report" content-type="application/pdf" filename="board.pdf" filesize="14"></action-text-attachment>`
	figure := `<figure data-trix-attachment='{"sgid":"sgid-report","contentType":"application/pdf","filename":"board.pdf","filesize":14,"url":"/rails/active_storage/blobs/report/board.pdf"}'></figure>`
	for _, tc := range []struct {
		name, expected, actual string
		same                   bool
	}{
		{"paragraph", `<p>Numbers.</p>`, `<div>Numbers.</div>`, true},
		{"paragraphs", "<p>Numbers.</p>\n<p>Details.</p>", `<div>Numbers.</div><div>Details.</div>`, true},
		{"formatting", `<p><strong>Numbers</strong> and <a href="https://example.org">details</a>.</p>`, `<div><strong>Numbers</strong> and <a href="https://example.org">details</a>.</div>`, true},
		{"upload", upload, figure, true},
		{"missing-upload", upload, "", false},
		{"different-upload", upload, strings.Replace(figure, "sgid-report", "sgid-other", 1), false},
		{"renamed-upload", upload, strings.Replace(figure, `"filename":"board.pdf"`, `"filename":"other.pdf"`, 1), false},
		{"changed-size", upload, strings.Replace(figure, `"filesize":14`, `"filesize":15`, 1), false},
		{"missing-size", upload, strings.Replace(figure, `"filesize":14,`, "", 1), false},
		{"changed-type", upload, strings.Replace(figure, "application/pdf", "image/png", 1), false},
		{"extra-figure-content", upload, strings.Replace(figure, "</figure>", "<p>Unexpected</p></figure>", 1), false},
		{"extra-attachment-content", upload, strings.Replace(figure, `"filesize":14`, `"filesize":14,"content":"Unexpected"`, 1), false},
		{"extra-attachment-caption", upload, strings.Replace(figure, `"filesize":14`, `"filesize":14,"caption":"Unexpected"`, 1), false},
		{"extra-body", `<p>Numbers.</p>`, `<div>Numbers.</div><div>Unexpected</div>`, false},
		{"changed-link", `<p><a href="https://example.org">details</a></p>`, `<div><a href="https://other.example.org">details</a></div>`, false},
		{"lost-emphasis", `<p><strong>Numbers.</strong></p>`, `<div>Numbers.</div>`, false},
		{"inline-space", `<strong>Board</strong> <em>figures</em>`, `<strong>Board</strong><em>figures</em>`, false},
		{"preformatted-block-space", "<pre><div>Numbers.</div>\n<div>Details.</div></pre>", "<pre><div>Numbers.</div><div>Details.</div></pre>", false},
		{"preformatted-space", "<pre>\n\n</pre>", "<pre></pre>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameSavedMessageHTML(tc.expected, tc.actual); got != tc.same {
				t.Fatalf("equivalent=%v, want %v", got, tc.same)
			}
		})
	}
}

func TestComposeFromCanonicalUploadedAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.pdf")
	if err := os.WriteFile(path, []byte("Report figures"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			var writes []draftWrite
			figure := `<figure data-trix-attachment='{"sgid":"sgid-report","contentType":"application/pdf","filename":"board.pdf","filesize":14,"url":"/rails/active_storage/blobs/report/board.pdf"}'></figure>`
			if changed {
				figure = strings.Replace(figure, "sgid-report", "sgid-other", 1)
			}
			actual := `<div>Numbers.</div><br>` + figure + `<br><div>Billing</div>`
			handler := senderServer(t, senderIdentity, &writes, func(s map[string]any) { s["content"] = actual })
			uploads := 0
			wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rails/active_storage/direct_uploads.json":
					if r.URL.Query().Get("filtered_account_id") != "9" {
						t.Error("upload not account scoped")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"signed_id":"signed-report","attachable_sgid":"sgid-report","direct_upload":{"url":%q,"headers":{}}}`, "http://"+r.Host+"/storage/upload")
				case "/storage/upload":
					uploads++
					w.WriteHeader(http.StatusNoContent)
				default:
					handler.ServeHTTP(w, r)
				}
			})
			_, err := runJSONCommand(t, wrapped, "compose", "--from", "88", "--subject", "Board update", "--to", "maria@example.com", "-m", "Numbers.", "--attach", path)
			if uploads != 1 {
				t.Fatalf("uploads=%d", uploads)
			}
			if changed {
				if err == nil || len(writes) != 1 {
					t.Fatalf("changed attachment delivered: err=%v writes=%v", err, writes)
				}
			} else if err != nil || len(writes) != 2 || writes[1].Method != "PUT" || writes[1].Body["message"].(map[string]any)["content"] != actual {
				t.Fatalf("err=%v writes=%v", err, writes)
			}
		})
	}
}
