package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/mcptest"
)

// attachEdit is draft 9 as HEY serves it for editing: from the second sender, with a
// file already attached.
const attachEdit = `{"id": 9, "subject": "Q3 plan",
	"content": "<div>Here it is.</div><action-text-attachment sgid=\"old-sgid\" content-type=\"application/pdf\" filename=\"plan.pdf\" filesize=\"120\"></action-text-attachment>",
	"sender": {"id": 6, "email_address": "me@acme.example"}, "creator": {"id": 5},
	"addressed": {"directly": [{"name": "Ann", "email_address": "ann@example.com"}], "copied": [{"email_address": "cat@example.com"}], "blindcopied": []}}`

// fakeHEYFiles is HEY as attach_file reaches it through the real SDK: the draft's edit
// state, the direct upload, the storage PUT and the draft's revision.
type fakeHEYFiles struct {
	t          *testing.T
	edit       string
	mu         sync.Mutex
	hits       []string
	stored     []byte
	storedAuth string
	revision   map[string]any
	// revise answers the revision; nil answers 204 and a Location.
	revise func(http.ResponseWriter)
	srv    *httptest.Server
}

func newFakeHEYFiles(t *testing.T, edit string) *fakeHEYFiles {
	f := &fakeHEYFiles{t: t, edit: edit}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHEYFiles) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	switch r.Method + " " + r.URL.Path {
	case "GET /messages/9/edit.json":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.edit)
	case "POST /rails/active_storage/direct_uploads.json":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"signed_id":"signed-1","attachable_sgid":"sgid-new","direct_upload":{"url":"`+f.srv.URL+`/storage/blob?sig=x","headers":{"Content-Type":"text/plain"}}}`)
	case "PUT /storage/blob":
		data, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.stored, f.storedAuth = data, r.Header.Get("Authorization")
		f.mu.Unlock()
	case "PUT /messages/9.json":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("revision body: %v", err)
		}
		f.mu.Lock()
		f.revision = body
		f.mu.Unlock()
		if f.revise != nil {
			f.revise(w)
			return
		}
		w.Header().Set("Location", "/messages/9")
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeHEYFiles) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

// sdkDraftFiles is hey mcp's DraftFiles over one SDK client (internal/cmd's
// mcpDraftFiles splits it by retry).
type sdkDraftFiles struct{ c *hey.Client }

func (d sdkDraftFiles) GetEdit(ctx context.Context, id int64) (*generated.MessageEditState, error) {
	return d.c.Messages().GetEdit(ctx, id)
}

func (d sdkDraftFiles) Put(ctx context.Context, path string, body any) (*hey.Response, error) {
	return d.c.Put(ctx, path, body)
}

func (d sdkDraftFiles) Upload(ctx context.Context, filename, contentType string, content io.ReadSeeker) (*generated.DirectUpload, error) {
	return d.c.Attachments().Upload(ctx, filename, contentType, content)
}

func (f *fakeHEYFiles) session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	c := hey.NewClient(&hey.Config{BaseURL: f.srv.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	_, session := connect(t, &fakeAPI{}, Config{Attach: &AttachConfig{Files: sdkDraftFiles{c}, Origin: testOrigin}})
	return session
}

func attachCall(t *testing.T, session *mcp.ClientSession, params map[string]any) (string, bool) {
	t.Helper()
	all := map[string]any{"draft_id": 9, "filename": "notes.txt", "content_type": "text/plain"}
	for k, v := range params {
		all[k] = v
	}
	return mcptest.CallText(t, session, attachToolName, map[string]any{"action": attachFileName, "params": all})
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// A file in content_base64, checked against its sha256, is uploaded with no HEY
// credential beside it and saved into the draft after its body, with everything else
// the draft had. Nothing is sent.
func TestAttachFileSavesTheFileIntoTheDraft(t *testing.T) {
	f := newFakeHEYFiles(t, attachEdit)
	data := []byte("hello")
	text, isError := attachCall(t, f.session(t), map[string]any{
		"content_type": "text/plain; charset=utf-8", "content_base64": base64.StdEncoding.EncodeToString(data), "sha256": sha256Hex(data),
	})
	if isError {
		t.Fatalf("attach_file failed: %s (HEY saw %v)", text, f.called())
	}
	if string(f.stored) != "hello" || f.storedAuth != "" {
		t.Errorf("storage got %q with Authorization %q; want the bytes and no credential", f.stored, f.storedAuth)
	}
	message, _ := f.revision["message"].(map[string]any)
	content, _ := message["content"].(string)
	want := `<div>Here it is.</div><action-text-attachment sgid="old-sgid" content-type="application/pdf" filename="plan.pdf" filesize="120"></action-text-attachment>` +
		`<br><action-text-attachment sgid="sgid-new" content-type="text/plain" filename="notes.txt" filesize="5"></action-text-attachment>`
	if content != want {
		t.Errorf("content = %s\nwant %s", content, want)
	}
	if message["subject"] != "Q3 plan" {
		t.Errorf("subject = %v", message["subject"])
	}
	if f.revision["acting_sender_id"] != float64(6) {
		t.Errorf("acting_sender_id = %v, want the draft's own sender 6", f.revision["acting_sender_id"])
	}
	entry, _ := f.revision["entry"].(map[string]any)
	if entry["status"] != "drafted" {
		t.Errorf("entry.status = %v, want drafted", entry["status"])
	}
	if got, _ := json.Marshal(entry["addressed"]); string(got) != `{"blindcopied":[],"copied":["cat@example.com"],"directly":["ann@example.com"]}` {
		t.Errorf("entry.addressed = %s, want the draft's recipients", got)
	}
	if _, scheduled := entry["scheduled_delivery"]; scheduled {
		t.Errorf("the revision schedules: %v", entry)
	}
	for _, want := range []string{`"sent": false`, `"filename": "notes.txt"`, `"byte_size": 5`, testOrigin + "/messages/9/edit"} {
		mustContain(t, text, want)
	}
}

// Everything wrong with the file is refused before HEY is asked anything, and so is a
// file object: hey mcp takes none.
func TestAttachFileRefusesABadFileBeforeHEY(t *testing.T) {
	saved := attachmentMaxBytes
	attachmentMaxBytes = 8
	t.Cleanup(func() { attachmentMaxBytes = saved })
	hello := base64.StdEncoding.EncodeToString([]byte("hello"))
	big := base64.StdEncoding.EncodeToString([]byte("nine bytes"))
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"sha256 mismatch":  {map[string]any{"content_base64": hello, "sha256": sha256Hex([]byte("hellO"))}, "doesn't match sha256"},
		"oversize":         {map[string]any{"content_base64": big}, "too large"},
		"not base64":       {map[string]any{"content_base64": "not base64!"}, "base64"},
		"empty":            {map[string]any{"content_base64": ""}, "empty"},
		"no content":       {map[string]any{}, "content_base64"},
		"a file object":    {map[string]any{"file": map[string]any{"download_url": "https://files.oaiusercontent.com/f", "file_id": "f"}}, "unknown argument file"},
		"no filename":      {map[string]any{"filename": " ", "content_base64": hello}, "params.filename"},
		"an unknown param": {map[string]any{"content_base64": hello, "url": "https://example.com/f"}, "doesn't take params.url"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeHEYFiles(t, attachEdit)
			text, isError := attachCall(t, f.session(t), tc.params)
			if !isError {
				t.Fatalf("attach_file took it: %s", text)
			}
			mustContain(t, text, tc.want)
			if hits := f.called(); len(hits) != 0 {
				t.Errorf("HEY was asked %v", hits)
			}
		})
	}
}

// A revision HEY answers with anything but the 204 and Location a saved draft gets is
// reported unconfirmed, as update_message's is, never attached.
func TestAttachFileReportsAnUnacknowledgedSaveUnconfirmed(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter){
		"200":                  func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) },
		"204 with no Location": func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeHEYFiles(t, attachEdit)
			f.revise = answer
			text, isError := attachCall(t, f.session(t), map[string]any{"content_base64": base64.StdEncoding.EncodeToString([]byte("hello"))})
			if !isError || strings.Contains(text, `"attached"`) {
				t.Fatalf("an unacknowledged save was reported attached: %s", text)
			}
			mustContain(t, text, "may not have saved a draft")
		})
	}
}

// A recipient with no address can't be written back, so the draft is left as it is
// rather than saved without them.
func TestAttachFileKeepsARecipientItCantWriteBack(t *testing.T) {
	f := newFakeHEYFiles(t, strings.Replace(attachEdit, `"copied": [{"email_address": "cat@example.com"}]`, `"copied": [{"name": "Cat", "email_address": " "}]`, 1))
	text, isError := attachCall(t, f.session(t), map[string]any{"content_base64": base64.StdEncoding.EncodeToString([]byte("hello"))})
	if !isError {
		t.Fatalf("attached and dropped a recipient: %s", text)
	}
	mustContain(t, text, "no email address")
	if slices.Contains(f.called(), "POST /rails/active_storage/direct_uploads.json") || f.revision != nil {
		t.Errorf("HEY was asked %v", f.called())
	}
}

// A draft scheduled to send isn't changed here, and nothing is uploaded for it.
func TestAttachFileLeavesAScheduledDraftToHEY(t *testing.T) {
	f := newFakeHEYFiles(t, strings.Replace(attachEdit, `"subject": "Q3 plan",`, `"subject": "Q3 plan", "scheduled_delivery_at": "2026-12-01T09:00:00Z",`, 1))
	text, isError := attachCall(t, f.session(t), map[string]any{"content_base64": base64.StdEncoding.EncodeToString([]byte("hello"))})
	if !isError {
		t.Fatalf("attached to a scheduled draft: %s", text)
	}
	mustContain(t, text, "scheduled to send")
	if slices.Contains(f.called(), "POST /rails/active_storage/direct_uploads.json") || f.revision != nil {
		t.Errorf("HEY was asked %v", f.called())
	}
}

// hey_attach is served beside the gateway tools when hey mcp can write, never read-only,
// and lists no file argument.
func TestAttachIsServedWhenHeyMCPCanWrite(t *testing.T) {
	cfg := Config{Attach: &AttachConfig{Files: sdkDraftFiles{}, Origin: testOrigin}}
	_, session := connect(t, &fakeAPI{}, cfg)
	tools := mcptest.ListTools(t, session)
	tool, ok := tools[attachToolName]
	if !ok {
		t.Fatalf("hey_attach isn't listed")
	}
	raw, _ := json.Marshal(tool.InputSchema)
	if strings.Contains(string(raw), "download_url") || len(tool.Meta) != 0 {
		t.Errorf("hey_attach lists a file argument: %s %v", raw, tool.Meta)
	}
	if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || *tool.Annotations.OpenWorldHint {
		t.Errorf("hey_attach hints = %+v, want a closed-world write", tool.Annotations)
	}

	cfg.ReadOnly = true
	_, session = connect(t, &fakeAPI{}, cfg)
	if _, ok := mcptest.ListTools(t, session)[attachToolName]; ok {
		t.Errorf("hey_attach is listed read-only")
	}
	cfg.ReadOnly, cfg.Domains = false, []string{"boxes"}
	_, session = connect(t, &fakeAPI{}, cfg)
	if _, ok := mcptest.ListTools(t, session)[attachToolName]; ok {
		t.Errorf("hey_attach is listed without the threads domain")
	}
}

// The file attach_file writes is named, with its size, in the confirmation that sends
// the draft.
func TestAnAttachedFileIsNamedInTheConfirmation(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Content = withAttachment("<div>Here it is.</div>", attachment{SGID: "sgid-new", Filename: "notes.txt", Size: 2048, ContentType: "text/plain"})
	})
	var prompt string
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return approve(ctx, req)
	}, "", false)
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("send failed: %s", resultText(t, result))
	}
	mustContain(t, prompt, "Attached: notes.txt (2 KB)")
}

// Over newline-delimited JSON, as hey mcp's stdio is, a file at the cap fits in one frame
// at MaxFrameBytes and is attached, and one past the cap is answered "too large" rather
// than ending the session.
func TestTheLargestFileFitsInAFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("moves about 50 MB")
	}
	f := newFakeHEYFiles(t, attachEdit)
	c := hey.NewClient(&hey.Config{BaseURL: f.srv.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	srv, err := New(&fakeAPI{}, Config{Attach: &AttachConfig{Files: sdkDraftFiles{c}, Origin: testOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	ctx := context.Background()
	ss, err := srv.BuildMCPServer(slog.New(slog.DiscardHandler)).Connect(ctx, &mcp.IOTransport{Reader: toServer, Writer: fromServer, MaxLineLength: MaxFrameBytes}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: toClient, Writer: fromClient, MaxLineLength: MaxFrameBytes}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	text, isError := attachCall(t, session, map[string]any{"content_base64": base64.StdEncoding.EncodeToString(make([]byte, attachmentMaxBytes))})
	if isError || int64(len(f.stored)) != attachmentMaxBytes {
		t.Fatalf("a file at the cap wasn't attached (%d bytes stored): %.300s", len(f.stored), text)
	}
	text, isError = attachCall(t, session, map[string]any{"content_base64": base64.StdEncoding.EncodeToString(make([]byte, attachmentMaxBytes+1<<20))})
	if !isError {
		t.Fatalf("a file past the cap was attached: %.300s", text)
	}
	mustContain(t, text, "too large")
}
