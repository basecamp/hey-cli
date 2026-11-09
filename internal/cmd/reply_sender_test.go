package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const replySenderIdentity = `{"accounts":[{"id":9,"status":"active"},{"id":8,"status":"active"}],"senders":[{"id":42,"account_id":9,"email_address":"jane@example.com","default":true},{"id":88,"account_id":9,"email_address":"billing@example.org"},{"id":99,"account_id":8,"email_address":"billing@example.org"}]}`

const replySenderPrefill = `{"subject":"Re: Weekly sync","sender":{"id":42,"email_address":"jane@example.com"},"addressed":{"directly":[{"email_address":"rick@example.com"}],"copied":[{"email_address":"maria@example.com"}],"blindcopied":[{"email_address":"sam@example.org"}]}}`

func replyFromServer(t *testing.T, identity, prefill string, writes *[]draftWrite) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "identity"):
			fmt.Fprint(w, identity)
		case r.Method == http.MethodGet && r.URL.Path == "/topics/7.json":
			fmt.Fprint(w, `{"id":7,"account_id":9,"name":"Weekly sync","entries":[{"id":12,"kind":"message"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/entries/12/replies/new.json":
			if prefill == "" {
				http.NotFound(w, r)
			} else {
				fmt.Fprint(w, prefill)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/messages/12.json":
			fmt.Fprint(w, messageAddressedToJane)
		case r.Method == http.MethodPost && r.URL.Path == "/entries/12/replies.json":
			if r.URL.Query().Get("filtered_account_id") != "9" {
				t.Error("reply not scoped to the thread's account")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			*writes = append(*writes, draftWrite{Method: r.Method, Path: r.URL.Path, Body: body})
			if body["entry"].(map[string]any)["status"] == "drafted" {
				w.Header().Set("Location", "/messages/12345")
				w.WriteHeader(http.StatusNoContent)
			} else {
				fmt.Fprint(w, `{}`)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	})
}

func TestReplyFromSendDraftAndPreview(t *testing.T) {
	for _, command := range [][]string{{"reply", "7"}, {"compose", "--thread-id", "7"}} {
		for _, from := range []string{" Billing@Example.ORG ", "88"} {
			for _, mode := range []string{"send", "draft", "preview"} {
				if command[0] == "compose" && mode == "preview" {
					continue // compose has no dry-run flag
				}
				t.Run(command[0]+"/"+from+"/"+mode, func(t *testing.T) {
					var writes []draftWrite
					args := append([]string{}, command...)
					args = append(args, "--from", from)
					if mode == "preview" {
						args = append(args, "--dry-run", "--attach", "/missing/invoice.pdf")
					} else {
						args = append(args, "-m", "Friday works for me.")
						if mode == "draft" {
							args = append(args, "--draft")
						}
					}
					response, err := runJSONCommand(t, replyFromServer(t, replySenderIdentity, replySenderPrefill, &writes), args...)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "preview" {
						if len(writes) != 0 {
							t.Fatal("preview wrote a message")
						}
						data := response.Data.(map[string]any)
						from := data["from"].(map[string]any)
						if from["id"] != float64(88) || from["email_address"] != "billing@example.org" || data["account_id"] != float64(9) {
							t.Fatalf("wrong preview: %v", data)
						}
						for line, want := range map[string]string{"to": "rick@example.com", "cc": "maria@example.com", "bcc": "sam@example.org"} {
							if !reflect.DeepEqual(data[line], []any{want}) {
								t.Errorf("preview %s = %v, want %s", line, data[line], want)
							}
						}
						return
					}
					if len(writes) != 1 {
						t.Fatalf("writes = %v, want one", writes)
					}
					body := writes[0].Body
					message := body["message"].(map[string]any)
					entry := body["entry"].(map[string]any)
					if body["acting_sender_id"] != float64(88) || message["subject"] != "Re: Weekly sync" || message["content"] != "<p>Friday works for me.</p>" {
						t.Fatalf("wrong reply payload: %v", body)
					}
					if (entry["status"] == "drafted") != (mode == "draft") {
						t.Fatalf("wrong delivery status: %v", entry)
					}
					addressed := entry["addressed"].(map[string]any)
					for line, want := range map[string]string{"directly": "rick@example.com", "copied": "maria@example.com", "blindcopied": "sam@example.org"} {
						if !reflect.DeepEqual(addressed[line], []any{want}) {
							t.Errorf("%s = %v, want %s", line, addressed[line], want)
						}
					}
				})
			}
		}
	}
}

func TestReplyFromRefusesInvalidSendersBeforeInputOrUpload(t *testing.T) {
	for _, command := range [][]string{{"reply", "7"}, {"compose", "--thread-id", "7"}} {
		for _, tc := range []struct {
			name, from, account, identity string
		}{
			{name: "empty", from: " "},
			{name: "unknown", from: "unknown@example.org"},
			{name: "other-account-sender", from: "99"},
			{name: "wrong-account-flag", from: "88", account: "8"},
			{name: "ambiguous-in-thread-account", from: "billing@example.org", identity: strings.Replace(replySenderIdentity, `"id":42,"account_id":9,"email_address":"jane@example.com"`, `"id":42,"account_id":9,"email_address":"billing@example.org"`, 1)},
			{name: "unavailable-account", from: "88", identity: strings.Replace(replySenderIdentity, `"id":9,"status":"active"`, `"id":9,"status":"disabled"`, 1)},
		} {
			for _, terminal := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/terminal=%v", command[0], tc.name, terminal), func(t *testing.T) {
					previous := stdinIsTerminal
					stdinIsTerminal = func() bool { return terminal }
					t.Cleanup(func() { stdinIsTerminal = previous })
					marker := filepath.Join(t.TempDir(), "editor-ran")
					script := filepath.Join(t.TempDir(), "editor")
					if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf 'Friday works.' > \"$1\"\n"), 0700); err != nil {
						t.Fatal(err)
					}
					t.Setenv("EDITOR", script)
					t.Setenv("VISUAL", script)
					input, err := os.CreateTemp(t.TempDir(), "stdin")
					if err != nil {
						t.Fatal(err)
					}
					defer input.Close()
					if _, err := input.WriteString("Friday works."); err != nil {
						t.Fatal(err)
					}
					if _, err := input.Seek(0, 0); err != nil {
						t.Fatal(err)
					}
					oldStdin := os.Stdin
					os.Stdin = input
					t.Cleanup(func() { os.Stdin = oldStdin })
					var writes []draftWrite
					identity := tc.identity
					if identity == "" {
						identity = replySenderIdentity
					}
					args := append(append([]string{}, command...), "--from", tc.from, "--draft")
					if tc.account != "" {
						args = append(args, "--account", tc.account)
					}
					handler := replyFromServer(t, identity, replySenderPrefill, &writes)
					for _, attach := range []bool{false, true} {
						attempt := append([]string{}, args...)
						if attach {
							attempt = append(attempt, "--attach", "/missing/invoice.pdf")
						}
						_, err = runJSONCommand(t, handler, attempt...)
						if err == nil || strings.Contains(err.Error(), "attachment") || len(writes) != 0 {
							t.Fatalf("sender validation: err=%v writes=%v", err, writes)
						}
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
	}
}

func TestReplyFromKeepsRecipientOverridesAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, prefill string
		flags         []string
		wantTo        []any
	}{
		{"merge", replySenderPrefill, []string{"--to", "support@example.com"}, []any{"rick@example.com", "support@example.com"}},
		{"replace-without-prefill", "", []string{"--replace-recipients", "--to", "support@example.com", "--dry-run"}, []any{"support@example.com"}},
		{"fallback", "", nil, []any{"jane@example.com", "rick@example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes []draftWrite
			args := append([]string{"reply", "7", "--from", "88", "-m", "Friday works."}, tc.flags...)
			response, err := runJSONCommand(t, replyFromServer(t, replySenderIdentity, tc.prefill, &writes), args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(writes) == 0 {
				data := response.Data.(map[string]any)
				if data["from"].(map[string]any)["id"] != float64(88) || !reflect.DeepEqual(data["to"], tc.wantTo) {
					t.Fatalf("wrong preview: %v", data)
				}
				return
			}
			body := writes[0].Body
			if body["acting_sender_id"] != float64(88) || !reflect.DeepEqual(body["entry"].(map[string]any)["addressed"].(map[string]any)["directly"], tc.wantTo) {
				t.Fatalf("wrong reply: %v", body)
			}
		})
	}
}

func TestReplyWithoutFromKeepsHEYsSender(t *testing.T) {
	for _, command := range [][]string{{"reply", "7"}, {"compose", "--thread-id", "7"}} {
		for _, tc := range []struct {
			name, prefill string
			want          float64
		}{
			{"prefill-default", replySenderPrefill, 42},
			{"prefill-alternate", strings.Replace(replySenderPrefill, `"id":42,"email_address":"jane@example.com"`, `"id":88,"email_address":"billing@example.org"`, 1), 88},
			{"fallback", "", 42},
		} {
			for _, draft := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/draft=%v", command[0], tc.name, draft), func(t *testing.T) {
					var writes []draftWrite
					args := append(append([]string{}, command...), "-m", "Friday works.")
					if draft {
						args = append(args, "--draft")
					}
					_, err := runJSONCommand(t, replyFromServer(t, replySenderIdentity, tc.prefill, &writes), args...)
					if err != nil || len(writes) != 1 {
						t.Fatalf("err=%v writes=%v", err, writes)
					}
					if writes[0].Body["acting_sender_id"] != tc.want {
						t.Fatalf("wrong automatic sender: %v", writes[0])
					}
				})
			}
		}
	}
}

func TestReplyFromWithAccountSelection(t *testing.T) {
	for _, command := range [][]string{{"reply", "7"}, {"compose", "--thread-id", "7"}} {
		for _, account := range []string{"9", "all"} {
			t.Run(command[0]+"/"+account, func(t *testing.T) {
				var writes []draftWrite
				args := append(append([]string{}, command...), "--account", account, "--from", "billing@example.org", "-m", "Friday works.")
				_, err := runJSONCommand(t, replyFromServer(t, replySenderIdentity, replySenderPrefill, &writes), args...)
				if err != nil || len(writes) != 1 {
					t.Fatalf("err=%v writes=%v", err, writes)
				}
				if writes[0].Body["acting_sender_id"] != float64(88) {
					t.Fatalf("sender resolved outside thread account: %v", writes[0])
				}
			})
		}
	}
}

func TestReplyFromUsesThreadAccountInsteadOfSavedDefault(t *testing.T) {
	for _, command := range [][]string{{"reply", "7"}, {"compose", "--thread-id", "7"}} {
		for _, mode := range []string{"send", "draft", "preview"} {
			if command[0] == "compose" && mode == "preview" {
				continue
			}
			for _, account := range []string{"", "9", "all", "8"} {
				t.Run(command[0]+"/"+mode+"/account="+account, func(t *testing.T) {
					var writes []draftWrite
					server := httptest.NewServer(replyFromServer(t, replySenderIdentity, replySenderPrefill, &writes))
					t.Cleanup(server.Close)
					dir := t.TempDir()
					if err := os.MkdirAll(filepath.Join(dir, "hey-cli"), 0700); err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(map[string]any{"account_defaults": map[string]string{server.URL: "8"}})
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(filepath.Join(dir, "hey-cli", "config.json"), data, 0600); err != nil {
						t.Fatal(err)
					}
					args := append(append([]string{}, command...), "--from", "billing@example.org")
					if account != "" {
						args = append(args, "--account", account)
					}
					if mode == "preview" {
						args = append(args, "--dry-run")
					} else {
						args = append(args, "-m", "Friday works.")
						if mode == "draft" {
							args = append(args, "--draft")
						}
					}
					out, err := runAccountsCLIWithConfig(t, server, dir, args...)
					if account == "8" {
						if err == nil || !strings.Contains(err.Error(), "different account") || len(writes) != 0 {
							t.Fatalf("explicit mismatch: err=%v writes=%v", err, writes)
						}
						return
					}
					if err != nil {
						t.Fatalf("saved default vetoed thread sender: %v", err)
					}
					if mode == "preview" {
						var response struct {
							Data replyPreview `json:"data"`
						}
						if err := json.Unmarshal([]byte(out), &response); err != nil {
							t.Fatal(err)
						}
						if len(writes) != 0 || response.Data.AccountID != 9 || response.Data.From.ID != 88 {
							t.Fatalf("wrong preview: %s writes=%v", out, writes)
						}
					} else if len(writes) != 1 || writes[0].Body["acting_sender_id"] != float64(88) {
						t.Fatalf("sender resolved outside thread account: %v", writes)
					}
				})
			}
		}
	}
}

func TestReplyFromUploadsInThreadAccount(t *testing.T) {
	for _, command := range [][]string{{"reply", "7"}, {"compose", "--thread-id", "7"}} {
		t.Run(command[0], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invoice.pdf")
			if err := os.WriteFile(path, []byte("Invoice figures"), 0600); err != nil {
				t.Fatal(err)
			}
			var writes []draftWrite
			handler := replyFromServer(t, replySenderIdentity, replySenderPrefill, &writes)
			uploads := 0
			wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rails/active_storage/direct_uploads.json":
					if r.URL.Query().Get("filtered_account_id") != "9" {
						t.Error("upload not scoped to thread account")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"signed_id":"signed-invoice","attachable_sgid":"sgid-invoice","direct_upload":{"url":%q,"headers":{}}}`, "http://"+r.Host+"/storage/upload")
				case "/storage/upload":
					uploads++
					w.WriteHeader(http.StatusNoContent)
				default:
					handler.ServeHTTP(w, r)
				}
			})
			args := append(append([]string{}, command...), "--from", "88", "--attach", path)
			_, err := runJSONCommand(t, wrapped, args...)
			if err != nil || uploads != 1 || len(writes) != 1 {
				t.Fatalf("err=%v uploads=%d writes=%v", err, uploads, writes)
			}
			body := writes[0].Body
			if body["acting_sender_id"] != float64(88) || !strings.Contains(body["message"].(map[string]any)["content"].(string), `sgid="sgid-invoice"`) {
				t.Fatalf("wrong attachment reply: %v", body)
			}
		})
	}
}
