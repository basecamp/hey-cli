package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
)

const testIdentityJSON = `{"email_address":"user@hey.com","id":1,"senders":[{"id":42,"default":true}],"primary_contact":{"id":42}}`

func TestComposeDraftNewMessageUsesDraftEndpoint(t *testing.T) {
	recorder := newDraftEndpointRecorder(t)
	cmd := newComposeCommand().cmd
	runCommandWithServer(t, cmd, recorder.handler, "--draft", "--to", "alice@example.com", "--subject", "Hello", "-m", "Draft body")

	recorder.assertHit(t, "POST /messages", 1)
	recorder.assertHit(t, "POST /messages.json", 0)
}

func TestDraftCreateAllowsExplicitEmptyMessage(t *testing.T) {
	recorder := newDraftEndpointRecorder(t)
	cmd := newDraftCreateCommand()
	runCommandWithServer(t, cmd, recorder.handler, "--to", "alice@example.com", "--subject", "Hello", "--message", "")

	recorder.assertHit(t, "POST /messages", 1)
	recorder.assertFormValue(t, "POST /messages", "message[content]", "<div><br></div>")
}

func TestComposeSendsNewMessageWithoutDraft(t *testing.T) {
	recorder := newDraftEndpointRecorder(t)
	cmd := newComposeCommand().cmd
	runCommandWithServer(t, cmd, recorder.handler, "--to", "alice@example.com", "--subject", "Hello", "-m", "Send body")

	recorder.assertHit(t, "POST /messages.json", 1)
	recorder.assertHit(t, "POST /messages", 0)
}

func TestComposeDraftThreadMessageUsesReplyDraftEndpoint(t *testing.T) {
	recorder := newDraftEndpointRecorder(t)
	cmd := newComposeCommand().cmd
	runCommandWithServer(t, cmd, recorder.handler, "--draft", "--thread-id", "123", "--subject", "Re: Hello", "-m", "Draft body")

	recorder.assertHit(t, "POST /entries/456/replies", 1)
	recorder.assertHit(t, "POST /topics/123/entries.json", 0)
}

func TestReplyDraftUsesReplyDraftEndpoint(t *testing.T) {
	recorder := newDraftEndpointRecorder(t)
	cmd := newReplyCommand().cmd
	runCommandWithServer(t, cmd, recorder.handler, "123", "--draft", "-m", "Draft reply")

	recorder.assertHit(t, "POST /entries/456/replies", 1)
	recorder.assertHit(t, "POST /entries/456/replies.json", 0)
}

func TestReplySendsWithoutDraft(t *testing.T) {
	recorder := newDraftEndpointRecorder(t)
	cmd := newReplyCommand().cmd
	runCommandWithServer(t, cmd, recorder.handler, "123", "-m", "Send reply")

	recorder.assertHit(t, "POST /entries/456/replies.json", 1)
	recorder.assertHit(t, "POST /entries/456/replies", 0)
}

type draftEndpointRecorder struct {
	mu   sync.Mutex
	hits map[string]int
	form map[string]url.Values
}

func newDraftEndpointRecorder(t *testing.T) *draftEndpointRecorder {
	t.Helper()
	return &draftEndpointRecorder{
		hits: map[string]int{},
		form: map[string]url.Values{},
	}
}

func (r *draftEndpointRecorder) handler(w http.ResponseWriter, req *http.Request) {
	key := req.Method + " " + req.URL.Path
	_ = req.ParseForm()
	r.mu.Lock()
	r.hits[key]++
	if len(req.PostForm) > 0 {
		r.form[key] = req.PostForm
	}
	r.mu.Unlock()

	switch key {
	case "GET /identity.json":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(testIdentityJSON))
	case "GET /messages/new", "GET /entries/456/replies/new":
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(testDraftFormHTML()))
	case "GET /topics/123/entries":
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<article id="entry_456" data-entry-id="456"></article>`))
	case "POST /messages":
		w.Header().Set("Location", "https://app.hey.test/messages/9001")
		w.WriteHeader(http.StatusCreated)
	case "POST /entries/456/replies":
		w.Header().Set("Location", "https://app.hey.test/messages/9002")
		w.WriteHeader(http.StatusCreated)
	case "POST /messages.json", "POST /topics/123/entries.json", "POST /entries/456/replies.json":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, fmt.Sprintf("unexpected request %s", key), http.StatusNotFound)
	}
}

func (r *draftEndpointRecorder) assertHit(t *testing.T, key string, want int) {
	t.Helper()
	r.mu.Lock()
	got := r.hits[key]
	r.mu.Unlock()
	if got != want {
		t.Fatalf("%s hit %d times, want %d", key, got, want)
	}
}

func (r *draftEndpointRecorder) assertFormValue(t *testing.T, key, name, want string) {
	t.Helper()
	r.mu.Lock()
	got := r.form[key].Get(name)
	r.mu.Unlock()
	if got != want {
		t.Fatalf("%s form %s = %q, want %q", key, name, got, want)
	}
}

func runCommandWithServer(t *testing.T, cmd *cobra.Command, handler http.HandlerFunc, args ...string) string {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	oldCfg, oldAuthMgr, oldHTTPClient, oldSDK, oldWriter := cfg, authMgr, httpClient, sdk, writer
	t.Cleanup(func() {
		cfg, authMgr, httpClient, sdk, writer = oldCfg, oldAuthMgr, oldHTTPClient, oldSDK, oldWriter
	})

	t.Setenv("HEY_TOKEN", "test-token")
	cfg = &config.Config{BaseURL: server.URL}
	httpClient = server.Client()
	authMgr = auth.NewManager(cfg.BaseURL, httpClient, t.TempDir())
	sdk = hey.NewClient(
		&hey.Config{BaseURL: server.URL, CacheEnabled: false},
		nil,
		hey.WithAuthStrategy(&cliAuthStrategy{mgr: authMgr}),
		hey.WithHTTPClient(httpClient),
	)

	var buf bytes.Buffer
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &buf, Stderr: &buf})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

func testDraftFormHTML() string {
	return `
<meta name="csrf-token" content="csrf-123" />
<input value="Re: Hello" name="message[subject]" />
<input type="hidden" name="message[content]" value="Existing body" />
<select name="entry[addressed][directly][]" hidden multiple>
  <option value="alice@example.com" selected>Alice</option>
</select>
<select name="entry[addressed][copied][]" hidden multiple></select>
<select name="entry[addressed][blindcopied][]" hidden multiple></select>`
}
