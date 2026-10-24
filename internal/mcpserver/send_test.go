package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- fixtures ---

var grantedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const testOrigin = "https://app.hey.example"

func contact(name, address string) generated.Contact {
	return generated.Contact{Id: int64(len(address)), Name: name, EmailAddress: address}
}

// fakeDrafts is HEY as send_draft sees it: one draft, an identity, and the mail this
// identity sent before the sign-in (what search finds).
type fakeDrafts struct {
	mu       sync.Mutex
	edit     generated.MessageEditState
	editErr  error
	sends    []hey.DraftContent
	sendErr  error
	sentAs   *generated.SentMessage
	sentTo   map[string]time.Time
	searches int
}

func newFakeDrafts() *fakeDrafts {
	return &fakeDrafts{
		edit: generated.MessageEditState{
			Id:        5,
			Subject:   "Re: Q3 plan",
			Content:   "<div>Hi Ann, the numbers on page 2 moved since Tuesday.</div>",
			Sender:    generated.Contact{Id: 77, Name: "Jeremy Daer", EmailAddress: "jeremy@hey.example"},
			Addressed: generated.Addressed{Directly: []generated.Contact{contact("Ann Example", "ann@example.com")}},
		},
		sentTo: map[string]time.Time{"ann@example.com": grantedAt.Add(-48 * time.Hour)},
	}
}

func (f *fakeDrafts) update(fn func(*generated.MessageEditState)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.edit)
}

func (f *fakeDrafts) sent() []hey.DraftContent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sends)
}

func (f *fakeDrafts) GetEdit(_ context.Context, draftID int64) (*generated.MessageEditState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editErr != nil {
		return nil, f.editErr
	}
	if draftID != f.edit.Id {
		return nil, &hey.Error{HTTPStatus: http.StatusNotFound, Message: "not found"}
	}
	edit := f.edit
	edit.Addressed = generated.Addressed{
		Directly:    slices.Clone(f.edit.Addressed.Directly),
		Copied:      slices.Clone(f.edit.Addressed.Copied),
		Blindcopied: slices.Clone(f.edit.Addressed.Blindcopied),
	}
	return &edit, nil
}

func (f *fakeDrafts) SendDraft(_ context.Context, _ int64, draft hey.DraftContent) (*generated.SentMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, draft)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	if f.sentAs != nil {
		return f.sentAs, nil
	}
	return &generated.SentMessage{Id: 5, TopicId: 900}, nil
}

func (f *fakeDrafts) Identity(context.Context) (*generated.Identity, error) {
	return &generated.Identity{
		PrimaryContact: generated.Contact{EmailAddress: "jeremy@hey.example"},
		Senders:        []generated.Sender{{Id: 77, EmailAddress: "jeremy@hey.example"}},
	}, nil
}

func (f *fakeDrafts) Search(_ context.Context, params hey.SearchParams) (*generated.AdvancedSearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches++
	at, ok := f.sentTo[params.To]
	if !ok {
		return &generated.AdvancedSearchResult{}, nil
	}
	return &generated.AdvancedSearchResult{Matches: []generated.SearchMatch{{Entries: []generated.Entry{{
		CreatedAt: at.Add(-2 * time.Hour),
		ActiveAt:  at,
		Creator:   generated.Contact{EmailAddress: "jeremy@hey.example"},
		Addressed: generated.Addressed{Directly: []generated.Contact{{EmailAddress: params.To}}},
	}}}}}, nil
}

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type sendRig struct {
	drafts *fakeDrafts
	state  *SendState
	clock  *clock
	api    *fakeAPI
	cfg    SendConfig
	server *Server
}

func newSendRig(t *testing.T) *sendRig {
	t.Helper()
	state, err := OpenSendState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rig := &sendRig{drafts: newFakeDrafts(), state: state, clock: &clock{now: grantedAt.Add(24 * time.Hour)}, api: &fakeAPI{}}
	rig.cfg = SendConfig{Drafts: rig.drafts, State: state, Grant: Grant{Key: "grant-1", GrantedAt: grantedAt}, Origin: testOrigin, Now: rig.clock.Now}
	rig.server, err = New(rig.api, Config{Send: &rig.cfg})
	if err != nil {
		t.Fatal(err)
	}
	return rig
}

// answer is how the person answers the confirmation; nil means the client declares no
// elicitation at all.
type answer func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)

func approve(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
}

// connect opens a client session on the rig's server. version "" is the SDK's latest
// (2026-07-28, where the client carries the rounds itself); an older one has the
// server elicit within the call. manual turns the client's own round handling off.
func (r *sendRig) connect(t *testing.T, elicit answer, version string, manual bool) *mcp.ClientSession {
	t.Helper()
	opts := &mcp.ClientOptions{}
	if elicit != nil {
		opts.ElicitationHandler = elicit
	}
	if manual {
		opts.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := r.server.BuildMCPServer(slog.New(slog.DiscardHandler)).Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, opts)
	session, err := client.Connect(context.Background(), clientTransport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// sendDraft calls send_draft on the rig's draft, 5.
func sendDraft(t *testing.T, session *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      sendToolName,
		Arguments: map[string]any{"action": sendDraftName, "params": map[string]any{"draft_id": 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	return textContent(t, result)
}

func mustContain(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

var eras = []struct{ name, version string }{
	{"2026-07-28 client rounds", ""},
	{"2025-06-18 server elicitation", "2025-06-18"},
}

// --- the tool ---

func TestHeySendIsServedWithHonestHints(t *testing.T) {
	rig := newSendRig(t)
	session := rig.connect(t, approve, "", false)

	var send *mcp.Tool
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name == sendToolName {
			send = tool
		}
	}
	if send == nil {
		t.Fatal("hey_send isn't served")
	}
	a := send.Annotations
	if a == nil || a.ReadOnlyHint || a.IdempotentHint || a.DestructiveHint == nil || !*a.DestructiveHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
		t.Errorf("annotations = %+v, want destructive, open world, not read-only, not idempotent", a)
	}
}

func TestHeySendIsNotServedReadOnlyOrWithoutSend(t *testing.T) {
	rig := newSendRig(t)
	for name, cfg := range map[string]Config{
		"no send config": {},
		"read-only":      {ReadOnly: true, Send: &rig.cfg},
		"no threads":     {Domains: []string{"boxes"}, Send: &rig.cfg},
	} {
		t.Run(name, func(t *testing.T) {
			srv, err := New(&fakeAPI{}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if srv.ServesSend() {
				t.Error("hey_send is served")
			}
		})
	}
}

// --- confirmation ---

func TestSendDraftSendsOnlyWhatThePersonConfirmed(t *testing.T) {
	for _, era := range eras {
		t.Run(era.name, func(t *testing.T) {
			rig := newSendRig(t)
			var prompt string
			session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				prompt = req.Params.Message
				if len(rig.drafts.sent()) != 0 {
					t.Error("sent before the person answered")
				}
				return approve(ctx, req)
			}, era.version, false)

			result := sendDraft(t, session)
			if result.IsError {
				t.Fatalf("send failed: %s", resultText(t, result))
			}
			mustContain(t, resultText(t, result), `"sent": true`, `"message_id": 5`)
			mustContain(t, prompt, `Send this email from "Jeremy Daer" <jeremy@hey.example>?`,
				`To:       "Ann Example" <ann@example.com>`, "Cc:       —", "Bcc:      —",
				"Subject:  Re: Q3 plan", "Attached: —", "When:     Now (undo window applies)",
				"    Hi Ann, the numbers on page 2 moved since Tuesday.")

			sent := rig.drafts.sent()
			if len(sent) != 1 {
				t.Fatalf("sent %d times, want once", len(sent))
			}
			want := hey.DraftContent{Subject: "Re: Q3 plan", Content: rig.drafts.edit.Content, To: []string{"ann@example.com"}, CC: []string{}, BCC: []string{}, ActingSenderID: 77}
			got := sent[0]
			if got.Subject != want.Subject || got.Content != want.Content || !slices.Equal(got.To, want.To) || len(got.CC)+len(got.BCC) != 0 || got.ActingSenderID != want.ActingSenderID || got.Schedule != nil {
				t.Errorf("sent %+v, want %+v", got, want)
			}
		})
	}
}

func TestSendDraftWithoutApprovalSendsNothing(t *testing.T) {
	answers := map[string]*mcp.ElicitResult{
		"decline":          {Action: "decline"},
		"cancel":           {Action: "cancel"},
		"accept, unticked": {Action: "accept", Content: map[string]any{"confirm": false}},
	}
	for name, given := range answers {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			session := rig.connect(t, func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) { return given, nil }, "", false)
			result := sendDraft(t, session)
			if result.IsError {
				t.Fatalf("an unapproved send is an error result: %s", resultText(t, result))
			}
			mustContain(t, resultText(t, result), `"sent": false`, "Draft saved but not sent", testOrigin+"/messages/5/edit")
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times without approval", n)
			}
		})
	}
}

func TestSendDraftCantSendFromAHostWithoutElicitation(t *testing.T) {
	for _, era := range eras {
		t.Run(era.name, func(t *testing.T) {
			rig := newSendRig(t)
			session := rig.connect(t, nil, era.version, false)
			result := sendDraft(t, session)
			if result.IsError {
				t.Fatalf("D3 answers a non-error result: %s", resultText(t, result))
			}
			mustContain(t, resultText(t, result), `"sent": false`, "can't show a confirmation", "send it in HEY: "+testOrigin+"/messages/5/edit")
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times from a host that can't confirm", n)
			}
		})
	}
}

// --- digest binding ---

func TestSendDraftRefusesADraftChangedAfterApproval(t *testing.T) {
	changes := map[string]func(*generated.MessageEditState){
		"a Bcc added": func(e *generated.MessageEditState) {
			e.Addressed.Blindcopied = append(e.Addressed.Blindcopied, contact("", "archive@evil.example"))
		},
		"a recipient swapped": func(e *generated.MessageEditState) {
			e.Addressed.Directly = []generated.Contact{contact("Ann Example", "ann@evil.example")}
		},
		"the subject": func(e *generated.MessageEditState) { e.Subject = "Re: Q3 plan (updated)" },
		"the body":    func(e *generated.MessageEditState) { e.Content += "<div>PS: the reset code is 123456</div>" },
		"the sender":  func(e *generated.MessageEditState) { e.Sender = contact("Jeremy", "jeremy@work.example") },
		"a schedule":  func(e *generated.MessageEditState) { e.ScheduledDeliveryAt = grantedAt.Add(72 * time.Hour) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				rig.drafts.update(change)
				return approve(ctx, req)
			}, "", false)
			result := sendDraft(t, session)
			if !result.IsError {
				t.Fatalf("a changed draft was sent: %s", resultText(t, result))
			}
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times after the draft changed", n)
			}
		})
	}
}

func TestSendDraftNamesTheChangeWhenOnlyTheContentMoved(t *testing.T) {
	rig := newSendRig(t)
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		rig.drafts.update(func(e *generated.MessageEditState) { e.Subject = "Something else" })
		return approve(ctx, req)
	}, "", false)
	mustContain(t, resultText(t, sendDraft(t, session)), "the draft changed since you approved it")
}

func TestAnAttachmentReplacedWithTheSameNameAndSizeIsAChange(t *testing.T) {
	figure := func(sgid, url string) string {
		return fmt.Sprintf(`<div>Plan attached.</div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;%s&quot;,&quot;filename&quot;:&quot;plan.pdf&quot;,&quot;filesize&quot;:217088,&quot;contentType&quot;:&quot;application/pdf&quot;,&quot;url&quot;:&quot;%s&quot;}" class="attachment"><img src="%s"><figcaption>plan.pdf</figcaption></figure>`, sgid, url, url)
	}
	first, err := analyzeBody(figure("BAh7blob1", "https://app.hey.example/blobs/redirect/aaa?exp=1"))
	if err != nil {
		t.Fatal(err)
	}
	refetched, _ := analyzeBody(figure("BAh7blob1", "https://app.hey.example/blobs/redirect/bbb?exp=2"))
	replaced, _ := analyzeBody(figure("BAh7blob2", "https://app.hey.example/blobs/redirect/aaa?exp=1"))

	if first.hash != refetched.hash {
		t.Error("an unchanged attachment hashed differently on a second fetch (signed URLs must not count)")
	}
	if first.hash == replaced.hash {
		t.Error("a same-name, same-size replacement attachment hashed the same")
	}
	if len(first.attachments) != 1 || first.attachments[0].Filename != "plan.pdf" || first.attachments[0].Size != 217088 {
		t.Errorf("attachments = %+v", first.attachments)
	}
	if first.lossless {
		t.Error("a draft with an attachment counts as fully shown")
	}
	if first.remote != "" {
		t.Errorf("the attachment's own preview counted as a remote reference: %s", first.remote)
	}
}

// --- single use ---

func TestSendDraftSendsADraftOnce(t *testing.T) {
	rig := newSendRig(t)
	session := rig.connect(t, approve, "", false)
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("first send failed: %s", resultText(t, result))
	}
	again := sendDraft(t, session)
	if !again.IsError {
		t.Fatalf("a second send of the draft went through: %s", resultText(t, again))
	}
	mustContain(t, resultText(t, again), "already sent")
	if n := len(rig.drafts.sent()); n != 1 {
		t.Errorf("sent %d times, want once", n)
	}
}

// roundOne asks for a confirmation without answering it, as a 2026-07-28 client that
// carries the rounds itself would, and returns the signed state.
func roundOne(t *testing.T, session *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	result := sendDraft(t, session)
	if !result.NeedsInput() || result.RequestState == "" || result.InputRequests[confirmInputID] == nil {
		t.Fatalf("first round didn't ask for a confirmation: %+v", result)
	}
	return result
}

func retry(t *testing.T, session *mcp.ClientSession, state string, answer *mcp.ElicitResult) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:           sendToolName,
		Arguments:      map[string]any{"action": sendDraftName, "params": map[string]any{"draft_id": 5}},
		RequestState:   state,
		InputResponses: mcp.InputResponseMap{confirmInputID: answer},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAReplayedConfirmationSendsNothing(t *testing.T) {
	rig := newSendRig(t)
	session := rig.connect(t, approve, "", true)
	accepted := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}

	state := roundOne(t, session).RequestState
	if result := retry(t, session, state, accepted); result.IsError {
		t.Fatalf("confirmed send failed: %s", resultText(t, result))
	}
	replayed := retry(t, session, state, accepted)
	if !replayed.IsError {
		t.Fatalf("a replayed confirmation sent again: %s", resultText(t, replayed))
	}
	if n := len(rig.drafts.sent()); n != 1 {
		t.Errorf("sent %d times, want once", n)
	}
}

func TestTwoConfirmationsOfOneDraftSendItOnce(t *testing.T) {
	rig := newSendRig(t)
	session := rig.connect(t, approve, "", true)
	accepted := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}

	first := roundOne(t, session).RequestState
	rig.clock.advance(time.Second)
	second := roundOne(t, session).RequestState
	if first == second {
		t.Fatal("the two rounds minted the same state, so this wouldn't test two confirmations")
	}
	if result := retry(t, session, first, accepted); result.IsError {
		t.Fatalf("first confirmation failed: %s", resultText(t, result))
	}
	if result := retry(t, session, second, accepted); !result.IsError {
		t.Fatalf("a second confirmation of the same draft sent it again: %s", resultText(t, result))
	}
	if n := len(rig.drafts.sent()); n != 1 {
		t.Errorf("sent %d times, want once", n)
	}
}

func TestAForgedOrExpiredConfirmationSendsNothing(t *testing.T) {
	accepted := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}
	cases := map[string]func(rig *sendRig, state string) string{
		"tampered payload": func(_ *sendRig, state string) string {
			payload, signature, _ := strings.Cut(state, ".")
			return payload[:len(payload)-2] + "AA." + signature
		},
		"unsigned": func(_ *sendRig, state string) string {
			payload, _, _ := strings.Cut(state, ".")
			return payload + "."
		},
		"made up": func(*sendRig, string) string { return "eyJrIjoic2VuZF9kcmFmdCJ9.c2lnbmF0dXJl" },
		"expired": func(rig *sendRig, state string) string {
			rig.clock.advance(confirmTTL + time.Second)
			return state
		},
		"another sign-in's key": func(_ *sendRig, state string) string {
			other := &sender{key: []byte("a different installation's key.."), cfg: SendConfig{Now: time.Now}}
			payload, _, _ := strings.Cut(state, ".")
			raw, _ := base64Decode(payload)
			var st confirmState
			_ = json.Unmarshal(raw, &st)
			return other.sign(st)
		},
	}
	for name, forge := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			session := rig.connect(t, approve, "", true)
			state := roundOne(t, session).RequestState
			result := retry(t, session, forge(rig, state), accepted)
			if !result.IsError {
				t.Fatalf("sent on a forged confirmation: %s", resultText(t, result))
			}
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times", n)
			}
		})
	}
}

// --- recipients, exposure and caps ---

func TestSendDraftFlagsANewRecipient(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Addressed.Copied = []generated.Contact{contact("Bob Example", "bob@example.com")}
	})
	var prompt string
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return approve(ctx, req)
	}, "", false)
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("send failed: %s", resultText(t, result))
	}
	mustContain(t, prompt, `Cc:       "Bob Example" <bob@example.com> (new: never emailed before)`)
	if strings.Contains(prompt, "<ann@example.com> (new") {
		t.Error("a recipient emailed before the sign-in was flagged new")
	}
}

func TestMailSentAfterTheSignInDoesntMakeARecipientKnown(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.sentTo["ann@example.com"] = grantedAt.Add(time.Hour)
	var prompt string
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return approve(ctx, req)
	}, "", false)
	sendDraft(t, session)
	mustContain(t, prompt, "<ann@example.com> (new: never emailed before)")
}

func TestAnUnscreenedReadRefusesSendsToNewRecipients(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Addressed.Directly = []generated.Contact{contact("", "archive@evil.example")}
	})
	session := rig.connect(t, approve, "", false)

	// Reading a thread may return an unscreened sender's words.
	read, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_threads",
		Arguments: map[string]any{"action": "get_topic", "params": map[string]any{"topicId": 9}},
	})
	if err != nil || read.IsError {
		t.Fatalf("get_topic: %v %v", err, read)
	}

	result := sendDraft(t, session)
	if !result.IsError {
		t.Fatalf("sent to a new recipient right after an unscreened read: %s", resultText(t, result))
	}
	mustContain(t, resultText(t, result), "unscreened", "get_topic", "archive@evil.example")
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times", n)
	}

	// Once the window lapses, the send is a D4 send again: confirmed and flagged.
	rig.clock.advance(exposureWindow + time.Second)
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("still refused after the exposure window: %s", resultText(t, result))
	}
}

func TestABoxReadDoesntExpose(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Addressed.Directly = []generated.Contact{contact("", "new@example.com")}
	})
	session := rig.connect(t, approve, "", false)
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_boxes",
		Arguments: map[string]any{"action": "get_imbox", "params": map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("an Imbox read exposed the grant: %s", resultText(t, result))
	}
}

func TestAnUnscreenedReadBetweenPromptAndApprovalRefusesTheSend(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Addressed.Directly = []generated.Contact{contact("", "new@example.com")}
	})
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		// Another conversation on this sign-in reads the Screener meanwhile.
		if err := rig.state.MarkExposed(ctx, "grant-1", "get_clearances", rig.clock.Now()); err != nil {
			t.Fatal(err)
		}
		return approve(ctx, req)
	}, "", false)
	result := sendDraft(t, session)
	if !result.IsError {
		t.Fatalf("an approval carried past an exposure: %s", resultText(t, result))
	}
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times", n)
	}
}

func TestAnUnscreenedReadBetweenPromptAndApprovalRefusesUnseenContent(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Content = `<div>Plan attached.</div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;BAh7&quot;,&quot;filename&quot;:&quot;notes.txt&quot;,&quot;filesize&quot;:120}"></figure>`
	})
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		if err := rig.state.MarkExposed(ctx, "grant-1", "get_message", rig.clock.Now()); err != nil {
			t.Fatal(err)
		}
		return approve(ctx, req)
	}, "", false)
	if result := sendDraft(t, session); !result.IsError {
		t.Fatalf("an attachment went out on an approval an exposure overtook: %s", resultText(t, result))
	}
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times", n)
	}
}

func TestAnExposedReadIsRefusedWhenItsMarkerCantBeWritten(t *testing.T) {
	rig := newSendRig(t)
	rig.state.fsync = func(*os.File) error { return errors.New("disk full") }
	session := rig.connect(t, approve, "", false)
	read, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_threads",
		Arguments: map[string]any{"action": "get_topic", "params": map[string]any{"topicId": 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !read.IsError || strings.Contains(textContent(t, read), `"ok":true`) {
		t.Fatalf("content went out without its exposure marker: %v", read.Content)
	}
}

func TestSendDraftCapsRecipientsSendsAndNewAddresses(t *testing.T) {
	t.Run("recipients per message", func(t *testing.T) {
		rig := newSendRig(t)
		rig.drafts.update(func(e *generated.MessageEditState) {
			for i := range maxRecipients {
				e.Addressed.Copied = append(e.Addressed.Copied, contact("", fmt.Sprintf("colleague%d@example.com", i)))
			}
		})
		session := rig.connect(t, approve, "", false)
		mustContain(t, resultText(t, sendDraft(t, session)), "at most 20 recipients")
	})

	t.Run("messages a day", func(t *testing.T) {
		rig := newSendRig(t)
		for i := range maxSendsPerDay {
			if err := rig.state.claim(context.Background(), testOrigin, int64(1000+i), "grant-1", nil, false, rig.clock.Now()); err != nil {
				t.Fatal(err)
			}
		}
		session := rig.connect(t, approve, "", false)
		mustContain(t, resultText(t, sendDraft(t, session)), "at most 50 messages a day")
		rig.clock.advance(capWindow)
		if result := sendDraft(t, session); result.IsError {
			t.Fatalf("the cap outlived its window: %s", resultText(t, result))
		}
	})

	t.Run("new addresses count once each", func(t *testing.T) {
		rig := newSendRig(t)
		for i := range maxSendsPerDay - 1 {
			if err := rig.state.claim(context.Background(), testOrigin, int64(1000+i), "grant-1", []string{"bob@example.com"}, false, rig.clock.Now()); err != nil {
				t.Fatalf("send %d to one new address: %v", i, err)
			}
		}
		_, charged, _ := rig.state.budget(context.Background(), "grant-1", rig.clock.Now())
		if len(charged) != 1 {
			t.Errorf("%d sends to one new address charged %d", maxSendsPerDay-1, len(charged))
		}
	})

	t.Run("an eleventh new address", func(t *testing.T) {
		rig := newSendRig(t)
		for i := range maxNewAddressesPerDay {
			if err := rig.state.claim(context.Background(), testOrigin, int64(1000+i), "grant-1", []string{fmt.Sprintf("new%d@example.com", i)}, false, rig.clock.Now()); err != nil {
				t.Fatal(err)
			}
		}
		rig.drafts.update(func(e *generated.MessageEditState) {
			e.Addressed.Directly = []generated.Contact{contact("", "eleventh@example.com")}
		})
		session := rig.connect(t, approve, "", false)
		mustContain(t, resultText(t, sendDraft(t, session)), "at most 10 new addresses")
		if n := len(rig.drafts.sent()); n != 0 {
			t.Errorf("sent %d times", n)
		}
	})

	t.Run("caps are per sign-in", func(t *testing.T) {
		rig := newSendRig(t)
		for i := range maxSendsPerDay {
			_ = rig.state.claim(context.Background(), testOrigin, int64(1000+i), "another-grant", nil, false, rig.clock.Now())
		}
		session := rig.connect(t, approve, "", false)
		if result := sendDraft(t, session); result.IsError {
			t.Fatalf("another sign-in's sends counted here: %s", resultText(t, result))
		}
	})
}

func TestUnseenContentGoesOnlyToKnownRecipients(t *testing.T) {
	long := "<div>" + strings.Repeat("The numbers moved again. ", 80) + "</div>"
	attached := `<div>Plan attached.</div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;BAh7&quot;,&quot;filename&quot;:&quot;notes.txt&quot;,&quot;filesize&quot;:120}"></figure>`

	for name, body := range map[string]string{"past the cap": long, "an attachment": attached} {
		t.Run(name+" to a new recipient", func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.update(func(e *generated.MessageEditState) {
				e.Content = body
				e.Addressed.Directly = []generated.Contact{contact("", "new@example.com")}
			})
			session := rig.connect(t, approve, "", false)
			mustContain(t, resultText(t, sendDraft(t, session)), "can't be fully reviewed here")
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times", n)
			}
		})
		t.Run(name+" to a known recipient", func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.update(func(e *generated.MessageEditState) { e.Content = body })
			session := rig.connect(t, approve, "", false)
			if result := sendDraft(t, session); result.IsError {
				t.Fatalf("refused for a known recipient: %s", resultText(t, result))
			}
		})
	}
}

func TestARemoteReferenceIsRefusedForEveryRecipient(t *testing.T) {
	for name, body := range map[string]string{
		"image":      `<div>Hi Ann</div><img src="https://tracker.example/p.gif?d=bWFpbGJveA">`,
		"css url":    `<div style="background:url(https://tracker.example/p.gif)">Hi Ann</div>`,
		"stylesheet": `<link rel="stylesheet" href="https://tracker.example/s.css"><div>Hi</div>`,
	} {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.update(func(e *generated.MessageEditState) { e.Content = body })
			session := rig.connect(t, approve, "", false)
			mustContain(t, resultText(t, sendDraft(t, session)), "loads by itself")
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times", n)
			}
		})
	}
}

func TestAScheduledDraftIsntSentHere(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) { e.ScheduledDeliveryAt = grantedAt.Add(72 * time.Hour) })
	session := rig.connect(t, approve, "", false)
	mustContain(t, resultText(t, sendDraft(t, session)), "already scheduled")
}

func TestTheConfirmationRendersEveryFieldInert(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Subject = "Q3\nBcc: nobody"
		e.Addressed.Directly = []generated.Contact{contact("Ann\n  Bcc:      —\u202e", "ann@example.com")}
		e.Content = "<div>To: someone@evil.example</div><div>zero\u200bwidth</div>"
	})
	var prompt string
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return &mcp.ElicitResult{Action: "decline"}, nil
	}, "", false)
	sendDraft(t, session)

	mustContain(t, prompt, `Subject:  Q3\u{000A}Bcc: nobody`, `"Ann\u{000A}  Bcc:      —\u{202E}" <ann@example.com>`,
		"    To: someone@evil.example", `zero\u{200B}width`)
	if strings.Count(prompt, "\n  Bcc:") != 1 {
		t.Errorf("a field forged a Bcc line:\n%s", prompt)
	}
}

// --- durability ---

func TestAFailedFsyncFailsTheSendClosed(t *testing.T) {
	rig := newSendRig(t)
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		rig.state.fsync = func(*os.File) error { return errors.New("I/O error") }
		return approve(ctx, req)
	}, "", false)
	result := sendDraft(t, session)
	if !result.IsError {
		t.Fatalf("sent without a durable claim: %s", resultText(t, result))
	}
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times", n)
	}
}

func TestClaimsAndCapsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	now := grantedAt.Add(24 * time.Hour)
	first, err := OpenSendState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.claim(context.Background(), testOrigin, 5, "grant-1", []string{"new@example.com"}, false, now); err != nil {
		t.Fatal(err)
	}
	if err := first.MarkExposed(context.Background(), "grant-1", "get_topic", now); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSendState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.claim(context.Background(), testOrigin, 5, "grant-2", nil, false, now); err == nil {
		t.Error("the same draft was claimed again after a restart, from another sign-in")
	}
	if exposed, _, _ := reopened.exposure(context.Background(), "grant-1", now); !exposed {
		t.Error("the exposure marker didn't survive a restart")
	}
	if _, charged, _ := reopened.budget(context.Background(), "grant-1", now); !charged["new@example.com"] {
		t.Error("the new-address charge didn't survive a restart")
	}

	key1, _ := first.ConfirmKey(context.Background())
	key2, _ := reopened.ConfirmKey(context.Background())
	if len(key1) != confirmKeyBytes || string(key1) != string(key2) {
		t.Error("the confirmation key isn't one per installation")
	}
}

// --- what the send does on failure ---

func TestAFailedSendIsNeverRetriedAndStaysClaimed(t *testing.T) {
	for name, sendErr := range map[string]error{
		"HEY refused": &hey.Error{HTTPStatus: http.StatusUnprocessableEntity, Message: "sending limit reached"},
		"no answer":   errors.New("connection reset"),
	} {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.sendErr = sendErr
			session := rig.connect(t, approve, "", false)
			result := sendDraft(t, session)
			if !result.IsError {
				t.Fatalf("a failed send reported success: %s", resultText(t, result))
			}
			if n := len(rig.drafts.sent()); n != 1 {
				t.Errorf("send attempted %d times, want once", n)
			}
			rig.drafts.sendErr = nil
			again := sendDraft(t, session)
			mustContain(t, resultText(t, again), "already sent")
			if n := len(rig.drafts.sent()); n != 1 {
				t.Errorf("a claimed draft was sent again after a failure (%d attempts)", n)
			}
		})
	}
}

// --- exposure table ---

// TestEveryServedReadIsClassifiedForExposure holds readExposes total: a read served
// without an entry exposes by default, but the table should say so on purpose.
func TestEveryServedReadIsClassifiedForExposure(t *testing.T) {
	cat := loadForTest(t)
	for _, d := range cat.Domains {
		for _, op := range d.Operations {
			if !op.ReadOnly {
				continue
			}
			if _, ok := readExposes[op.ID]; !ok {
				t.Errorf("%s (%s) is served but not classified in readExposes", op.ID, d.Key)
			}
		}
	}
}

func base64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// The claim re-reads exposure itself, so a marker landing after the second round's
// review and before the claim still refuses a draft to someone new or with unseen
// content.
func TestTheClaimRereadsExposure(t *testing.T) {
	rig := newSendRig(t)
	now := rig.clock.Now()
	if err := rig.state.MarkExposed(context.Background(), "grant-1", "get_topic", now); err != nil {
		t.Fatal(err)
	}
	var refused *refusalError
	if err := rig.state.claim(context.Background(), testOrigin, 5, "grant-1", []string{"new@example.com"}, false, now); !errors.As(err, &refused) {
		t.Errorf("claimed a draft to someone new while exposed: %v", err)
	}
	if err := rig.state.claim(context.Background(), testOrigin, 5, "grant-1", nil, true, now); !errors.As(err, &refused) {
		t.Errorf("claimed unseen content while exposed: %v", err)
	}
	if err := rig.state.claim(context.Background(), testOrigin, 5, "grant-1", nil, false, now); err != nil {
		t.Errorf("refused a fully shown draft to known recipients while exposed: %v", err)
	}
}

// The caps are charged in the claim's own locked update, not only checked by the
// review before it, so two processes can't both spend the last of one.
func TestTheClaimEnforcesTheCaps(t *testing.T) {
	ctx := context.Background()
	rig := newSendRig(t)
	now := rig.clock.Now()
	var refused *refusalError
	for i := range maxNewAddressesPerDay {
		if err := rig.state.claim(ctx, testOrigin, int64(100+i), "grant-1", []string{fmt.Sprintf("new%d@example.com", i)}, false, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := rig.state.claim(ctx, testOrigin, 200, "grant-1", []string{"eleventh@example.com"}, false, now); !errors.As(err, &refused) {
		t.Errorf("claimed an eleventh new address: %v", err)
	}
	for i := maxNewAddressesPerDay; i < maxSendsPerDay; i++ {
		if err := rig.state.claim(ctx, testOrigin, int64(100+i), "grant-1", nil, false, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := rig.state.claim(ctx, testOrigin, 300, "grant-1", nil, false, now); !errors.As(err, &refused) {
		t.Errorf("claimed a fifty-first send: %v", err)
	}
}

// Each fsync is load-bearing: the new state file's before the rename, and the
// directory's after it.
func TestEitherFailedFsyncFailsTheClaim(t *testing.T) {
	for name, failDir := range map[string]bool{"the file": false, "the directory": true} {
		t.Run(name, func(t *testing.T) {
			if failDir && runtime.GOOS == "windows" {
				t.Skip("Windows can't open a directory to fsync it; syncDir skips it there")
			}
			rig := newSendRig(t)
			rig.state.fsync = func(f *os.File) error {
				info, err := f.Stat()
				if err != nil {
					return err
				}
				if info.IsDir() == failDir {
					return errors.New("I/O error")
				}
				return f.Sync()
			}
			if err := rig.state.claim(context.Background(), testOrigin, 5, "grant-1", nil, false, rig.clock.Now()); err == nil {
				t.Errorf("a claim whose fsync of %s failed was acknowledged", name)
			}
		})
	}
}

// --- review round one ---

func TestASignInChangedUnderTheServerRefusesSendsAndExposingReads(t *testing.T) {
	rig := newSendRig(t)
	stored := "grant-1"
	rig.cfg.CurrentGrant = func(context.Context) (string, error) { return stored, nil }
	srv, err := New(rig.api, Config{Send: &rig.cfg})
	if err != nil {
		t.Fatal(err)
	}
	rig.server = srv
	session := rig.connect(t, approve, "", false)

	stored = "grant-2" // hey auth login --mcp ran again while this process served
	mustContain(t, resultText(t, sendDraft(t, session)), "signed in again")
	read, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_threads",
		Arguments: map[string]any{"action": "get_topic", "params": map[string]any{"topicId": 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !read.IsError {
		t.Error("an exposing read went out under a sign-in its marker wouldn't be recorded against")
	}
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times", n)
	}
}

func TestAnAttachmentThatIsntAPlainBlobIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no blob identity":   `<figure data-trix-attachment="{}" style="background:url(https://tracker.example/p.gif)">hidden text</figure>`,
		"embedded html":      `<figure data-trix-attachment="{&quot;contentType&quot;:&quot;text/html&quot;,&quot;content&quot;:&quot;&lt;img src=https://tracker.example/p.gif&gt;&quot;}"></figure>`,
		"unparseable data":   `<figure data-trix-attachment="{not json"></figure>`,
		"action-text, empty": `<action-text-attachment content="&lt;b&gt;x&lt;/b&gt;"></action-text-attachment>`,
	} {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.update(func(e *generated.MessageEditState) { e.Content = "<div>Hi Ann</div>" + body })
			session := rig.connect(t, approve, "", false)
			mustContain(t, resultText(t, sendDraft(t, session)), "can't verify")
			if n := len(rig.drafts.sent()); n != 0 {
				t.Errorf("sent %d times", n)
			}
		})
	}
}

func TestTheConfirmationShowsAWholeAddressHoweverLong(t *testing.T) {
	long := strings.Repeat("a", 240) + "@evil.example"
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Addressed.Directly = []generated.Contact{contact("Ann Example", long)}
	})
	var prompt string
	session := rig.connect(t, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return &mcp.ElicitResult{Action: "decline"}, nil
	}, "", false)
	sendDraft(t, session)
	mustContain(t, prompt, "<"+long+">")
}

func TestAReadOnlyServerStillRecordsExposure(t *testing.T) {
	for name, cfg := range map[string]func(*SendConfig) Config{
		"--read-only": func(send *SendConfig) Config { return Config{ReadOnly: true, Send: send} },
		"--domains search": func(send *SendConfig) Config {
			return Config{Domains: []string{"search", "threads"}, ReadOnly: true, Send: send}
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			srv, err := New(rig.api, cfg(&rig.cfg))
			if err != nil {
				t.Fatal(err)
			}
			if srv.ServesSend() {
				t.Fatal("a read-only server serves hey_send")
			}
			rig.server = srv
			session := rig.connect(t, approve, "", false)
			if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "hey_search",
				Arguments: map[string]any{"action": "advanced_search", "params": map[string]any{"q": "reset"}},
			}); err != nil {
				t.Fatal(err)
			}
			if exposed, _, _ := rig.state.exposure(context.Background(), "grant-1", rig.clock.Now()); !exposed {
				t.Error("a read on a server that can't send didn't expose the sign-in another hey mcp sends on")
			}
		})
	}
}

func TestTheNewAddressWindowRollsWithTheLastSend(t *testing.T) {
	ctx := context.Background()
	rig := newSendRig(t)
	start := rig.clock.Now()
	first := make([]string, maxNewAddressesPerDay)
	for i := range first {
		first[i] = fmt.Sprintf("new%d@example.com", i)
	}
	if err := rig.state.claim(ctx, testOrigin, 100, "grant-1", first, false, start); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.claim(ctx, testOrigin, 101, "grant-1", first, false, start.Add(23*time.Hour)); err != nil {
		t.Fatalf("resending to the same new addresses was charged again: %v", err)
	}
	var refused *refusalError
	if err := rig.state.claim(ctx, testOrigin, 102, "grant-1", []string{"eleventh@example.com"}, false, start.Add(24*time.Hour+time.Minute)); !errors.As(err, &refused) {
		t.Errorf("an eleventh new address fit in a window that still held ten: %v", err)
	}
}

// --- review round two ---

func TestADeliveryHEYKeptAsADraftIsntReportedSent(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.sentAs = &generated.SentMessage{Id: 5} // the redirect to the edit page
	session := rig.connect(t, approve, "", false)
	result := sendDraft(t, session)
	if !result.IsError || strings.Contains(resultText(t, result), `"sent": true`) {
		t.Fatalf("a draft HEY kept was reported sent: %s", resultText(t, result))
	}
	mustContain(t, resultText(t, result), "kept the draft")
}

func TestADraftMadeBeforeTheSignInButSentAfterDoesntMakeARecipientKnown(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.sentTo["ann@example.com"] = grantedAt.Add(time.Hour) // created an hour before, delivered an hour after
	var prompt string
	session := rig.connect(t, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return &mcp.ElicitResult{Action: "decline"}, nil
	}, "", false)
	sendDraft(t, session)
	mustContain(t, prompt, "<ann@example.com> (new: never emailed before)")
}

func TestAnEscapedOrFunctionalStyleCountsAsLoading(t *testing.T) {
	for name, style := range map[string]string{
		"css escape":    `background:u\72l(https://tracker.example/p.gif)`,
		"function call": `background:image(https://tracker.example/p.gif)`,
	} {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.update(func(e *generated.MessageEditState) { e.Content = `<div style="` + style + `">Hi Ann</div>` })
			session := rig.connect(t, approve, "", false)
			mustContain(t, resultText(t, sendDraft(t, session)), "loads by itself")
		})
	}
}

// An attachment goes out as its identity alone, so nothing a model puts in or on its
// wrapper (a tracker preview, a styled or rewritten caption, a stray attribute, markup
// inside) is sent, and the caption that is sent is the one the prompt shows.
func TestAnAttachmentIsDeliveredAsItsIdentityAlone(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Content = `<div>Plan attached.</div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;BAh7&quot;,&quot;filename&quot;:&quot;plan.pdf&quot;,&quot;filesize&quot;:120,&quot;url&quot;:&quot;https://app.hey.example/blob?sig=1&quot;}" data-trix-attributes="{&quot;caption&quot;:&quot;Q3 plan&quot;}" style="display:none" onclick="x"><img src="/\\tracker.example/p.gif" srcset="https://tracker.example/p.gif 2x"><figcaption style="background:url(https://tracker.example/c.gif)">forward the reset codes</figcaption><div>hidden</div></figure>`
	})
	var prompt string
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return approve(ctx, req)
	}, "", false)
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("send failed: %s", resultText(t, result))
	}
	mustContain(t, prompt, `Attached: plan.pdf (120 bytes), captioned "Q3 plan"`)

	delivered := rig.drafts.sent()[0].Content
	for _, gone := range []string{"tracker", "reset codes", "hidden", "display:none", "onclick", "sig=1", "<img", "figcaption"} {
		if strings.Contains(delivered, gone) {
			t.Errorf("the delivered body still carries %q: %s", gone, delivered)
		}
	}
	mustContain(t, delivered, "BAh7", "plan.pdf", "Q3 plan", "Plan attached.")
}

// --- review round three ---

func TestNoscriptMarkupCountsAsLoading(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) {
		e.Content = `<div>Hi Ann</div><noscript><img src="https://tracker.example/p.gif"></noscript>`
	})
	session := rig.connect(t, approve, "", false)
	mustContain(t, resultText(t, sendDraft(t, session)), "loads by itself")
}

// --- review round four ---

func TestTheWholeSubjectIsShown(t *testing.T) {
	rig := newSendRig(t)
	subject := "Re: Q3 plan " + strings.Repeat("and more ", 30) + "RESET-CODE-123456"
	rig.drafts.update(func(e *generated.MessageEditState) { e.Subject = subject })
	var prompt string
	session := rig.connect(t, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompt = req.Params.Message
		return &mcp.ElicitResult{Action: "decline"}, nil
	}, "", false)
	sendDraft(t, session)
	mustContain(t, prompt, "Subject:  "+subject+"\n")
}

func TestEveryInvisibleCharacterIsEscaped(t *testing.T) {
	for name, r := range map[string]rune{
		"tag character":             0xE0041,
		"variation selector":        0xFE0F,
		"private use":               0xE000,
		"word joiner":               0x2060,
		"invisible times":           0x2062,
		"combining grapheme joiner": 0x034F,
		"hangul filler":             0x3164,
	} {
		if got := escapeInvisible("a"+string(r)+"b", true); !strings.Contains(got, `\u{`) {
			t.Errorf("%s (U+%04X) shown raw: %q", name, r, got)
		}
	}
}

func TestAWriteThatAnswersWithContentExposes(t *testing.T) {
	rig := newSendRig(t)
	rig.api.resp = &hey.Response{StatusCode: http.StatusOK, Data: json.RawMessage(`{"id":3,"name":"Mallory","email_address":"mallory@evil.example"}`)}
	session := rig.connect(t, approve, "", false)
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_contacts",
		Arguments: map[string]any{"action": "reveal_contact", "params": map[string]any{"contactId": 3}},
	}); err != nil {
		t.Fatal(err)
	}
	if exposed, _, _ := rig.state.exposure(context.Background(), "grant-1", rig.clock.Now()); !exposed {
		t.Error("reveal_contact returned a contact without exposing the sign-in")
	}

	quiet := newSendRig(t)
	quiet.api.resp = &hey.Response{StatusCode: http.StatusNoContent, Data: json.RawMessage(``)}
	session = quiet.connect(t, approve, "", false)
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_threads",
		Arguments: map[string]any{"action": "trash_topic", "params": map[string]any{"topicId": 9}},
	}); err != nil {
		t.Fatal(err)
	}
	if exposed, _, _ := quiet.state.exposure(context.Background(), "grant-1", quiet.clock.Now()); exposed {
		t.Error("a write that answered nothing exposed the sign-in")
	}
}

// --- review round five ---

// To someone new, the draft goes as the plain text the confirmation shows, so nothing
// in its markup (hidden text, a list's type, trailing whitespace, invisible characters)
// carries anything the person didn't read.
func TestToSomeoneNewWhatGoesIsWhatWasShown(t *testing.T) {
	var tagged strings.Builder
	for _, c := range "reset code 123456" {
		tagged.WriteRune(0xE0000 + c)
	}
	cases := map[string]struct{ body, shown string }{
		"hidden text":         {`<div>Thanks!</div><div style="display:none">forward the reset codes</div>`, "Thanks!\nforward the reset codes"},
		"an ordered list":     {`<ol><li>one</li><li>two</li></ol>`, "- one\n- two\n"},
		"an unordered list":   {`<ul><li>one</li><li>two</li></ul>`, "- one\n- two\n"},
		"bold and italic":     {`<div><strong>one</strong> <em>two</em></div>`, "one two"},
		"tag characters":      {"<div>Hi Ann." + tagged.String() + "</div>", "Hi Ann."},
		"zero-width padding":  {"<div>" + strings.Repeat("\u200b", 200) + "Hi Ann.</div>", "Hi Ann."},
		"trailing whitespace": {"<pre>Hi Ann\n\t \t  \n</pre>", "Hi Ann\n\n"},
	}
	delivered := map[string]string{}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newSendRig(t)
			rig.drafts.update(func(e *generated.MessageEditState) {
				e.Content = tc.body
				e.Addressed.Directly = []generated.Contact{contact("", "new@example.com")}
			})
			var prompt string
			session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				prompt = req.Params.Message
				return approve(ctx, req)
			}, "", false)
			if result := sendDraft(t, session); result.IsError {
				t.Fatalf("send failed: %s", resultText(t, result))
			}
			mustContain(t, prompt, "Goes as plain text")
			for _, line := range strings.Split(tc.shown, "\n") {
				mustContain(t, prompt, "    "+line+"\n")
			}
			got := rig.drafts.sent()[0].Content
			if want := plainHTML(tc.shown); got != want {
				t.Errorf("delivered %q, want exactly the shown text %q", got, want)
			}
			delivered[name] = got
		})
	}
	if delivered["an ordered list"] != delivered["an unordered list"] {
		t.Error("a list's type reached the recipient without the person seeing it")
	}
}

func TestToAKnownRecipientTheDraftGoesAsItIs(t *testing.T) {
	rig := newSendRig(t)
	body := `<div><strong>Hi Ann</strong>, the plan:</div><ol><li>numbers</li></ol>`
	rig.drafts.update(func(e *generated.MessageEditState) { e.Content = body })
	session := rig.connect(t, approve, "", false)
	if result := sendDraft(t, session); result.IsError {
		t.Fatalf("send failed: %s", resultText(t, result))
	}
	mustContain(t, rig.drafts.sent()[0].Content, "<strong>Hi Ann</strong>", "<ol>")
}

func TestAPlainPathThatChangesBetweenRoundsRefusesTheSend(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.update(func(e *generated.MessageEditState) { e.Content = `<div><strong>Hi Ann</strong></div>` })
	session := rig.connect(t, func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		// Approved as it is, to a known recipient; then an unscreened read lands, and
		// the draft would now go as plain text, which isn't what was approved.
		if err := rig.state.MarkExposed(ctx, "grant-1", "get_topic", rig.clock.Now()); err != nil {
			t.Fatal(err)
		}
		return approve(ctx, req)
	}, "", false)
	mustContain(t, resultText(t, sendDraft(t, session)), "changes how the draft would go")
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times", n)
	}
}
