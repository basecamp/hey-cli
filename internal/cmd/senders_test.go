package cmd

import (
	"encoding/json"
	"fmt"
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
		case r.Method == http.MethodPost && r.URL.Path == "/messages.json", r.Method == http.MethodPut && r.URL.Path == "/messages/12345.json":
			if r.URL.Query().Get("filtered_account_id") != "9" {
				t.Error("message write was not account scoped")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			*writes = append(*writes, draftWrite{Method: r.Method, Path: r.URL.Path, Body: body})
			entry, _ := body["entry"].(map[string]any)
			if r.Method == http.MethodPut || entry["status"] == "drafted" {
				saved = body
			}
			if entry["status"] == "drafted" {
				w.Header().Set("Location", "/messages/12345")
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/messages/12345/edit.json":
			state := map[string]any{"id": 12345, "subject": "Board update", "content": "<p>Numbers.</p><br><div>Personal</div>", "creator": map[string]any{"id": 77, "account_id": 9}, "sender": map[string]any{"id": 77, "account_id": 9, "email_address": "personal@example.org"}, "addressed": map[string]any{"directly": []any{map[string]any{"email_address": "maria@example.com"}}}}
			if saved != nil {
				message := saved["message"].(map[string]any)
				state["subject"] = message["subject"]
				state["content"] = message["content"]
				state["sender"] = map[string]any{"id": saved["acting_sender_id"], "account_id": 9, "email_address": "billing@example.org"}
				entry := saved["entry"].(map[string]any)
				addressed := map[string]any{}
				for kind, values := range entry["addressed"].(map[string]any) {
					contacts := []any{}
					if addresses, ok := values.([]any); ok {
						for _, address := range addresses {
							contacts = append(contacts, map[string]any{"email_address": address})
						}
					}
					addressed[kind] = contacts
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
			if len(writes) != 1 {
				t.Fatalf("writes: %+v", writes)
			}
			write := writes[0]
			if write.Method != http.MethodPost || write.Body["acting_sender_id"] != float64(88) {
				t.Fatalf("wrong sender write: %+v", write)
			}
			if write.Body["message"].(map[string]any)["content"] != "<p>Numbers.</p><br><div>Billing</div>" {
				t.Fatalf("wrong body: %+v", write)
			}
			entry := write.Body["entry"].(map[string]any)
			if draft && entry["status"] != "drafted" {
				t.Fatalf("draft was sent: %+v", write)
			}
			if !draft {
				if _, exists := entry["status"]; exists {
					t.Fatalf("send was saved as a draft: %+v", write)
				}
				addressed := entry["addressed"].(map[string]any)
				if len(addressed["directly"].([]any)) != 1 || len(addressed["copied"].([]any)) != 1 || len(addressed["blindcopied"].([]any)) != 1 {
					t.Fatalf("recipients missing: %+v", write)
				}
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

func TestDraftEditFromPreservesFieldsAndQuote(t *testing.T) {
	var writes []draftWrite
	quote := `<div><br><br></div><div>On Friday, Maria wrote:</div><blockquote><div>Personal</div></blockquote>`
	alter := func(state map[string]any) {
		state["content"] = `<p>Numbers.</p><br><div>Personal</div>` + quote
		state["scheduled_delivery_at"] = "2026-10-01T12:00:00Z"
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
		{"missing-account", "billing@example.org", func(state map[string]any) {
			state["creator"] = map[string]any{"id": 77}
			state["sender"] = map[string]any{"id": 77}
		}},
		{"wrong-account", "billing@example.org", func(state map[string]any) { state["creator"] = map[string]any{"id": 77, "account_id": 8} }},
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

func TestComposeFromNoNameTagAndSenderID(t *testing.T) {
	for _, draft := range []bool{true, false} {
		t.Run(fmt.Sprint(draft), func(t *testing.T) {
			var writes []draftWrite
			args := []string{"compose", "--from", "88", "--subject", "Board update", "--to", "maria@example.com", "-m", "Numbers.", "--no-name-tag"}
			if draft {
				args = append(args, "--draft")
			}
			_, err := runJSONCommand(t, senderServer(t, senderIdentity, &writes, nil), args...)
			if err != nil || len(writes) != 1 {
				t.Fatalf("err=%v writes=%+v", err, writes)
			}
			write := writes[0]
			if write.Body["acting_sender_id"] != float64(88) || write.Body["message"].(map[string]any)["content"] != "<p>Numbers.</p>" {
				t.Fatalf("unexpected payload: %+v", write)
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

func TestComposeFromDoesNotRetryDelivery(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var writes []draftWrite
			handler := senderServer(t, senderIdentity, &writes, nil)
			sends := 0
			wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/messages.json" {
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
			if err == nil || sends != 1 || len(writes) != 0 {
				t.Fatalf("err=%v sends=%d writes=%v", err, sends, writes)
			}
		})
	}
}

func TestDraftEditFromKeepsWrappedBody(t *testing.T) {
	var writes []draftWrite
	original := `<div><figure data-trix-attachment='{"contentType":"text/html","content":"<shadow-content><template><p>Numbers.</p><br><div>Personal</div></template></shadow-content>"}'></figure></div>`
	alter := func(state map[string]any) { state["content"] = original }
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

func TestComposeFromSendsMarkdownDirectly(t *testing.T) {
	for _, nameTag := range []bool{false, true} {
		t.Run(fmt.Sprint(nameTag), func(t *testing.T) {
			var writes []draftWrite
			args := []string{"compose", "--from", "88", "--subject", "Board update", "--to", "maria@example.com", "-m", "One.\n\nTwo."}
			want := "<p>One.</p>\n<p>Two.</p>"
			if nameTag {
				want += "<br><div>Billing</div>"
			} else {
				args = append(args, "--no-name-tag")
			}
			handler := senderServer(t, senderIdentity, &writes, nil)
			editReads := 0
			wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/messages/12345/edit.json" {
					editReads++
				}
				handler.ServeHTTP(w, r)
			})
			_, err := runJSONCommand(t, wrapped, args...)
			if err != nil || len(writes) != 1 || editReads != 0 {
				t.Fatalf("err=%v writes=%v edit reads=%d", err, writes, editReads)
			}
			if writes[0].Method != http.MethodPost || writes[0].Body["message"].(map[string]any)["content"] != want {
				t.Fatalf("unexpected direct send: %+v", writes[0])
			}
		})
	}
}

func TestDraftWritesPreserveCreatorSender(t *testing.T) {
	identity := strings.Replace(senderIdentity, `"senders":[`, `"senders":[{"id":42,"account_id":9,"email_address":"default@example.org","default":true},`, 1)
	for _, args := range [][]string{{"draft", "edit", "12345", "--subject", "Updated board figures"}, {"draft", "send", "12345"}} {
		t.Run(args[1], func(t *testing.T) {
			var writes []draftWrite
			_, err := runJSONCommand(t, senderServer(t, identity, &writes, func(state map[string]any) { delete(state, "sender") }), append(args, "--account", "9")...)
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

func TestComposeFromUploadsAttachmentAndSendsDirectly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.pdf")
	if err := os.WriteFile(path, []byte("Report figures"), 0600); err != nil {
		t.Fatal(err)
	}
	var writes []draftWrite
	handler := senderServer(t, senderIdentity, &writes, nil)
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
	if err != nil || uploads != 1 || len(writes) != 1 {
		t.Fatalf("err=%v uploads=%d writes=%v", err, uploads, writes)
	}
	write := writes[0]
	content := write.Body["message"].(map[string]any)["content"].(string)
	for _, want := range []string{`sgid="sgid-report"`, `filename="board.pdf"`, `filesize="14"`, `<br><div>Billing</div>`} {
		if !strings.Contains(content, want) {
			t.Fatalf("content %q missing %q", content, want)
		}
	}
	if write.Method != http.MethodPost || write.Body["acting_sender_id"] != float64(88) {
		t.Fatalf("unexpected direct send: %+v", write)
	}
}
