package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/localapp/localapptest"
	"github.com/basecamp/hey-cli/internal/output"
)

// exitPending is the exit a send that waits for the person ends with: neither done (0)
// nor failed, and a number nothing else exits with.
const exitPending = 9

// appWritesDoor is appImboxDoor's door, answering the writes too: a screening decision, a
// new draft, a reply draft and a send that waits for the person.
func appWritesDoor(t *testing.T) *localapptest.Door {
	t.Helper()
	door := appImboxDoor(t)
	door.Answer("screener.decide", map[string]any{"sender": 51, "screened": "in"})
	door.Answer("draft.write", map[string]any{
		"id": 1731280000123, "kind": "new", "account": "jason@example.com", "from": "jason@example.com",
		"to": []string{"marcus@example.com"}, "cc": []string{}, "bcc": []string{},
		"subject": "Stint plan", "summary": "Saturday works", "saved_at": "2026-11-12T09:30:00Z", "sendable": true,
	})
	door.Answer("draft.reply", map[string]any{
		"id": 1731280000456, "kind": "reply", "thread": 801, "account": "jason@example.com", "from": "jason@example.com",
		"to": []string{"marcus@example.com"}, "cc": []string{}, "bcc": []string{},
		"subject": "Re: Stint plan for Saturday", "summary": "Works for me", "saved_at": "2026-11-12T09:31:00Z", "sendable": true,
	})
	door.Answer("message.send", map[string]any{
		"status": "pending", "confirmation": "c-7",
		"snapshot": map[string]any{
			"draft": 9001, "account": "jason@example.com", "from": "jason@example.com",
			"to": []string{"marcus@example.com"}, "cc": []string{}, "bcc": []string{},
			"subject": "Re: Stint plan for Saturday", "body": "Works for me", "rich": false,
			"attachments": []any{}, "thread": 801,
		},
	})
	return door
}

// appCall is one tools/call the door read.
type appCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func appCalls(t *testing.T, door *localapptest.Door) []appCall {
	t.Helper()
	raws := door.Calls()
	calls := make([]appCall, 0, len(raws))
	for _, raw := range raws {
		var call appCall
		if err := json.Unmarshal(raw, &call); err != nil {
			t.Fatalf("call %s: %v", raw, err)
		}
		calls = append(calls, call)
	}
	return calls
}

func appCallNames(calls []appCall) string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.Name)
	}
	return strings.Join(names, ",")
}

// A sender id from the app's screener list is screened in or out by the app, and the
// answer reads as the server's does, a list of the decided, its ids named as senders.
func TestAppScreenerApproveBySenderID(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "screener", "approve", "51", "--json")
	if err != nil {
		t.Fatal(err)
	}
	response := appEnvelope(t, stdout)
	if response.Meta["endpoint"] != "app" || response.Meta["id_kind"] != "sender" || response.Summary != "1 sender approved" {
		t.Errorf("meta = %v, summary %q", response.Meta, response.Summary)
	}
	decided := appData[[]screenedResult](t, response)
	if len(decided) != 1 || decided[0].ID != 51 || decided[0].Status != "approved" {
		t.Errorf("decided = %+v", decided)
	}
	calls := appCalls(t, door)
	if len(calls) != 1 || calls[0].Name != "screener.decide" ||
		fmt.Sprint(calls[0].Arguments["sender"]) != "51" || calls[0].Arguments["decision"] != "in" {
		t.Errorf("calls = %+v", calls)
	}
}

// An address is looked up in the app's own Screener, waiting and decided alike, whatever
// its case, so an agent never meets the app's sender ids.
func TestAppScreenerDenyByAddress(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("screener.decide", map[string]any{"sender": 53, "screened": "out"})
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "screener", "deny", "Dana@Example.com", "--json")
	if err != nil {
		t.Fatal(err)
	}
	decided := appData[[]screenedResult](t, appEnvelope(t, stdout))
	if len(decided) != 1 || decided[0].ID != 53 || decided[0].Status != "denied" ||
		decided[0].Name != "Dana Whitfield" || decided[0].Email != "dana@example.com" {
		t.Errorf("decided = %+v", decided)
	}
	calls := appCalls(t, door)
	if appCallNames(calls) != "screener.list,screener.decide" ||
		fmt.Sprint(calls[1].Arguments["sender"]) != "53" || calls[1].Arguments["decision"] != "out" {
		t.Errorf("calls = %+v", calls)
	}
}

// An address the app's Screener does not hold is not found, and nobody is decided.
func TestAppScreenerAddressNobodySentIsNotFound(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("screener.list", map[string]any{
		"waiting":       []map[string]any{{"id": 51, "name": "Priya Raman", "address": "priya@example.com"}},
		"waiting_count": 1,
		"decided":       []map[string]any{{"id": 53, "name": "Dana Whitfield", "address": "dana@example.com", "screened_in": true}},
	})
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "screener", "approve", "nobody@example.com")
	if output.ExitCodeFor(err) != output.ExitNotFound {
		t.Fatalf("err = %v", err)
	}
	if names := appCallNames(appCalls(t, door)); names != "screener.list" {
		t.Errorf("calls = %s", names)
	}
}

// What the app does not take is refused before the door is asked: several senders at
// once, a delivery box, marking seen, spam.
func TestAppScreenerRefusesWhatTheAppDoesNotTake(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	for _, args := range [][]string{
		{"screener", "approve", "51", "52"},
		{"screener", "approve", "51", "--box", "The Feed"},
		{"screener", "approve", "51", "--seen"},
		{"screener", "deny", "51", "--spam"},
		{"screener", "deny", "abc"},
	} {
		_, _, err := runProfileCLI(t, args...)
		if output.ExitCodeFor(err) != output.ExitUsage {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the door was called: %s", calls)
	}
}

// An app that says it decided something else is never reported as having done what was
// asked.
func TestAppScreenerDecisionOtherwiseIsNotReportedAsDone(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("screener.decide", map[string]any{"sender": 51, "screened": "out"})
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "screener", "approve", "51")
	if output.ExitCodeFor(err) != output.ExitAPI || !strings.Contains(apiErrorHint(err), "Check in HEY") {
		t.Fatalf("err = %v", err)
	}
}

// compose --draft writes a new draft in the app, sends nothing, and answers the draft
// under the server's field names with hey draft send, on this profile, as its next step.
func TestAppComposeDraftWritesInTheApp(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "compose", "--draft", "--to", "Marcus Ortiz <marcus@example.com>",
		"--subject", "Stint plan", "-m", "Saturday works", "--json")
	if err != nil {
		t.Fatal(err)
	}
	response := appEnvelope(t, stdout)
	if response.Meta["endpoint"] != "app" || !strings.Contains(response.Summary, "nothing has been sent") {
		t.Errorf("meta = %v, summary %q", response.Meta, response.Summary)
	}
	draft := appData[appDraft](t, response)
	if draft.ID != 1731280000123 || draft.Kind != "new" || !draft.Sendable || draft.Subject != "Stint plan" {
		t.Errorf("draft = %+v", draft)
	}
	if !appBreadcrumb(response, "hey -P this-computer draft send 1731280000123") {
		t.Errorf("breadcrumbs = %+v", response.Breadcrumbs)
	}
	calls := appCalls(t, door)
	if len(calls) != 1 || calls[0].Name != "draft.write" ||
		calls[0].Arguments["to"] != "Marcus Ortiz <marcus@example.com>" ||
		calls[0].Arguments["subject"] != "Stint plan" || calls[0].Arguments["body"] != "Saturday works" {
		t.Errorf("calls = %+v", calls)
	}

	ids, _, err := runProfileCLI(t, "compose", "--draft", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works", "--ids-only")
	if err != nil || ids != "1731280000123\n" {
		t.Errorf("--ids-only = %q, %v", ids, err)
	}
	styled, _, err := runProfileCLI(t, "compose", "--draft", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works", "--styled")
	if err != nil || !strings.Contains(styled, "Nothing has been sent") || !strings.Contains(styled, "hey -P this-computer draft send 1731280000123") {
		t.Errorf("styled = %q, %v", styled, err)
	}
}

func appBreadcrumb(response output.Response, command string) bool {
	for _, crumb := range response.Breadcrumbs {
		if crumb.Command == command {
			return true
		}
	}
	return false
}

// A draft HEY would not send from here, one to nobody, is saved and says so, with no send
// to suggest.
func TestAppComposeDraftToNobodySaysHEYWouldNotSendIt(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("draft.write", map[string]any{
		"id": 1731280000124, "kind": "new", "account": "jason@example.com", "from": "jason@example.com",
		"to": []string{}, "cc": []string{}, "bcc": []string{},
		"subject": "Board update", "summary": "Numbers to follow", "saved_at": "2026-11-12T09:30:00Z", "sendable": false,
	})
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "compose", "--draft", "--subject", "Board update", "-m", "Numbers to follow", "--json")
	if err != nil {
		t.Fatal(err)
	}
	response := appEnvelope(t, stdout)
	if !strings.Contains(response.Summary, "would not send") || len(response.Breadcrumbs) != 0 {
		t.Errorf("summary %q, breadcrumbs %+v", response.Summary, response.Breadcrumbs)
	}
	if calls := appCalls(t, door); len(calls) != 1 || calls[0].Arguments["to"] != "" {
		t.Errorf("calls = %+v", calls)
	}
}

// What the app's draft.write does not take is refused before the door is asked.
func TestAppComposeDraftRefusesWhatTheAppDoesNotTake(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	base := []string{"compose", "--draft", "--to", "marcus@example.com", "--subject", "Stint plan"}
	for _, extra := range [][]string{
		{"-m", "Saturday works", "--cc", "dana@example.com"},
		{"-m", "Saturday works", "--bcc", "dana@example.com"},
		{"-m", "Saturday works", "--attach", "plan.pdf"},
		{"--message-html", "<p>Saturday works</p>"},
		{"-m", "Saturday works", "--from", "jason@example.com"},
		{"-m", "Saturday works", "--thread-id", "801"},
		{"-m", "Saturday works", "--no-name-tag"},
	} {
		_, _, err := runProfileCLI(t, append(append([]string{}, base...), extra...)...)
		if output.ExitCodeFor(err) != output.ExitUsage {
			t.Errorf("%v: err = %v", extra, err)
		}
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the door was called: %s", calls)
	}
}

// reply --draft writes a reply draft on the thread in the app, and sends nothing.
func TestAppReplyDraftWritesInTheApp(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "reply", "801", "--draft", "-m", "Works for me", "--json")
	if err != nil {
		t.Fatal(err)
	}
	response := appEnvelope(t, stdout)
	draft := appData[appDraft](t, response)
	if draft.ID != 1731280000456 || draft.Kind != "reply" || draft.Thread == nil || *draft.Thread != 801 {
		t.Errorf("draft = %+v", draft)
	}
	if !strings.Contains(response.Summary, "nothing has been sent") ||
		!appBreadcrumb(response, "hey -P this-computer draft send 1731280000456") {
		t.Errorf("summary %q, breadcrumbs %+v", response.Summary, response.Breadcrumbs)
	}
	calls := appCalls(t, door)
	if len(calls) != 1 || calls[0].Name != "draft.reply" ||
		fmt.Sprint(calls[0].Arguments["thread"]) != "801" || calls[0].Arguments["body"] != "Works for me" {
		t.Errorf("calls = %+v", calls)
	}
}

// A reply draft the app saved on another thread, or as something else, is not reported
// as the reply asked for.
func TestAppReplyDraftOnAnotherThreadIsNotReportedAsDone(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("draft.reply", map[string]any{
		"id": 1731280000456, "kind": "reply", "thread": 802, "account": "jason@example.com", "from": "jason@example.com",
		"to": []string{}, "cc": []string{}, "bcc": []string{}, "subject": "Re: Invoice", "summary": "Works for me",
		"saved_at": "2026-11-12T09:31:00Z", "sendable": true,
	})
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "reply", "801", "--draft", "-m", "Works for me")
	if output.ExitCodeFor(err) != output.ExitAPI || !strings.Contains(apiErrorHint(err), "Check in HEY") {
		t.Fatalf("err = %v", err)
	}
}

// What the app's draft.reply does not take is refused before the door is asked: the app
// addresses a reply itself, as HEY's Reply does.
func TestAppReplyDraftRefusesWhatTheAppDoesNotTake(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	for _, extra := range [][]string{
		{"--draft", "-m", "Works for me", "--to", "dana@example.com"},
		{"--draft", "-m", "Works for me", "--cc", "dana@example.com"},
		{"--draft", "-m", "Works for me", "--bcc", "dana@example.com"},
		{"--draft", "-m", "Works for me", "--to", "dana@example.com", "--replace-recipients"},
		{"--draft", "-m", "Works for me", "--from", "jason@example.com"},
		{"--draft", "-m", "Works for me", "--attach", "plan.pdf"},
		{"--draft", "--message-html", "<p>Works for me</p>"},
		{"--dry-run"},
		{"--draft", "-m", "Works for me", "abc"},
	} {
		args := append([]string{"reply", "801"}, extra...)
		if extra[len(extra)-1] == "abc" {
			args = append([]string{"reply", "abc"}, extra[:len(extra)-1]...)
		}
		_, _, err := runProfileCLI(t, args...)
		if output.ExitCodeFor(err) != output.ExitUsage {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the door was called: %s", calls)
	}
}

// A verb that would send now is refused on the app before anything happens, pointing at
// --draft and then hey draft send, which waits for the person.
func TestAppRefusesWhatWouldSendNow(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	for _, args := range [][]string{
		{"compose", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works"},
		{"compose", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works", "--draft=false"},
		{"reply", "801", "-m", "Works for me"},
		{"bulk-reply", "send", "301", "-m", "Thanks, got it"},
		{"forward", "801", "--to", "dana@example.com"},
	} {
		_, _, err := runProfileCLI(t, append(args, "--json")...)
		if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(err.Error(), "--draft") ||
			!strings.Contains(apiErrorHint(err), "hey -P this-computer draft send") {
			t.Errorf("%v: err = %v (hint %q)", args, err, apiErrorHint(err))
		}
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the door was called: %s", calls)
	}
}

// hey draft send on the app asks HEY to send, and HEY waits for the person: exit 9,
// meta.status pending, and words that never say it was sent.
func TestAppDraftSendWaitsInHEY(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "draft", "send", "9001", "--json")
	if output.ExitCodeFor(err) != exitPending {
		t.Fatalf("err = %v", err)
	}
	response := appEnvelope(t, stdout)
	if response.Meta["status"] != "pending" || response.Meta["endpoint"] != "app" ||
		!strings.Contains(response.Summary, "Waiting in HEY") || !strings.Contains(response.Summary, "nothing has been sent") {
		t.Errorf("meta = %v, summary %q", response.Meta, response.Summary)
	}
	data := appData[map[string]any](t, response)
	if data["status"] != "pending" || data["confirmation"] != "c-7" || data["snapshot"] == nil {
		t.Errorf("data = %v", data)
	}
	calls := appCalls(t, door)
	if len(calls) != 1 || calls[0].Name != "message.send" || fmt.Sprint(calls[0].Arguments["draft"]) != "9001" {
		t.Errorf("calls = %+v", calls)
	}

	styled, _, err := runProfileCLI(t, "draft", "send", "9001", "--styled")
	if output.ExitCodeFor(err) != exitPending || !strings.Contains(styled, "Waiting in HEY") ||
		!strings.Contains(styled, "Nothing has been sent") || strings.Contains(styled, "Draft 9001 sent") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
	ids, stderr, err := runProfileCLI(t, "draft", "send", "9001", "--ids-only")
	if output.ExitCodeFor(err) != exitPending || ids != "c-7\n" || !strings.Contains(stderr, "Waiting in HEY") {
		t.Errorf("--ids-only = %q, stderr %q, err = %v", ids, stderr, err)
	}
}

// An answer to message.send that is not pending is never read as a send.
func TestAppDraftSendAnswerNotPendingIsNotASend(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("message.send", map[string]any{"sent": true})
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "draft", "send", "9001")
	if output.ExitCodeFor(err) != output.ExitAPI || !strings.Contains(err.Error(), "may have done something") ||
		!strings.Contains(apiErrorHint(err), "before running it again") {
		t.Fatalf("err = %v", err)
	}
}

// The app's refusals of a write say what to do in the CLI's terms.
func TestAppWriteRefusalsSayWhatToDo(t *testing.T) {
	for _, test := range []struct {
		name    string
		tool    string
		refusal map[string]any
		args    []string
		code    int
		says    string
	}{
		{"scheduled", "message.send", map[string]any{"error": "disabled", "arg": "draft", "of": "message.send", "given": 9001, "available": []any{9002}},
			[]string{"draft", "send", "9001"}, output.ExitUsage, "9002"},
		{"no sender", "message.send", map[string]any{"error": "not_a_sender", "draft": 9001, "from": "old@example.com"},
			[]string{"draft", "send", "9001"}, output.ExitAPI, "old@example.com"},
		{"no such draft", "message.send", map[string]any{"error": "unavailable", "arg": "draft", "of": "message.send", "given": 77, "available": []any{9001}},
			[]string{"draft", "send", "77"}, output.ExitNotFound, "9001"},
		{"no such sender", "screener.decide", map[string]any{"error": "unavailable", "arg": "sender", "of": "screener.decide", "given": 999, "available": []any{51}},
			[]string{"screener", "approve", "999"}, output.ExitNotFound, "51"},
		{"scheduled, no arg", "message.send", map[string]any{"error": "disabled", "available": []any{9002}},
			[]string{"draft", "send", "9001"}, output.ExitUsage, "draft takes one of: 9002"},
		{"older app", "message.send", map[string]any{"error": "not_offered"},
			[]string{"draft", "send", "9001"}, output.ExitUsage, "does not offer it now"},
		{"drafts unloaded", "message.send", map[string]any{"error": "unavailable"},
			[]string{"draft", "send", "9001"}, output.ExitAPI, "once the app has synced"},
		{"send cut off", "message.send", map[string]any{"error": "session_ended"},
			[]string{"draft", "send", "9001"}, output.ExitAPI, "nothing has been sent without you"},
		{"signed out", "message.send", map[string]any{"error": "signed_out"},
			[]string{"draft", "send", "9001"}, output.ExitAuth, "Sign in"},
		{"not an address", "draft.write", map[string]any{"error": "not_an_address", "arg": "to", "given": []any{"marcus"}},
			[]string{"compose", "--draft", "--subject", "Stint plan", "-m", "Saturday works"}, output.ExitUsage, "marcus"},
		{"too large", "draft.write", map[string]any{"error": "not_saved", "refusal": "too_large"},
			[]string{"compose", "--draft", "--subject", "Stint plan", "-m", "Saturday works"}, output.ExitUsage, "larger"},
		{"saved, not listed", "draft.reply", map[string]any{"error": "saved_not_listed", "draft": 1731280000456, "detail": "gone"},
			[]string{"reply", "801", "--draft", "-m", "Works for me"}, output.ExitAPI, "hey -P this-computer draft list"},
		{"session ended", "draft.write", map[string]any{"error": "session_ended", "detail": "signed out"},
			[]string{"compose", "--draft", "--subject", "Stint plan", "-m", "Saturday works"}, output.ExitAPI, "may have been done"},
	} {
		t.Run(test.name, func(t *testing.T) {
			door := appWritesDoor(t)
			door.Refuse(test.tool, test.refusal)
			appTestHome(t, door.Path, true)

			_, _, err := runProfileCLI(t, test.args...)
			if output.ExitCodeFor(err) != test.code ||
				!strings.Contains(err.Error()+" "+apiErrorHint(err), test.says) {
				t.Errorf("err = %v (hint %q), exit %d", err, apiErrorHint(err), output.ExitCodeFor(err))
			}
		})
	}
}

// Exit 9 is listed only where something can answer it: under the gate.
func TestExitCodesHelpNamesPendingOnlyUnderTheGate(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)
	stdout, _, err := runProfileCLI(t, "help", "exit-codes")
	if err != nil || !strings.Contains(stdout, "  9  ") || !strings.Contains(stdout, "nothing has been sent") {
		t.Errorf("under the gate: %q, %v", stdout, err)
	}

	appTestHome(t, door.Path, false)
	stdout, _, err = runProfileCLI(t, "-P", "hey.com", "help", "exit-codes")
	if err != nil || strings.Contains(stdout, "  9  ") {
		t.Errorf("without the gate: %q, %v", stdout, err)
	}
}

// No write on an app profile reaches HEY's server, even with a token at hand that would
// let it: every verb B routes or refuses, done or refused by the app, asks the door alone.
func TestAppWritesNeverReachHEYsServer(t *testing.T) {
	var reached atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		t.Errorf("reached HEY's server: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	runs := [][]string{
		{"screener", "approve", "51"},
		{"screener", "deny", "dana@example.com"},
		{"screener", "approve", "nobody@example.com"},
		{"compose", "--draft", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works"},
		{"reply", "801", "--draft", "-m", "Works for me"},
		{"draft", "send", "9001"},
		{"compose", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works"},
		{"reply", "801", "-m", "Works for me"},
		{"bulk-reply", "send", "301", "-m", "Thanks, got it"},
		{"forward", "801", "--to", "dana@example.com"},
	}
	for _, refused := range []bool{false, true} {
		door := appWritesDoor(t)
		if refused {
			for _, tool := range []string{"screener.list", "screener.decide", "draft.write", "draft.reply", "message.send"} {
				door.Refuse(tool, map[string]any{"error": "signed_out"})
			}
		}
		appTestHome(t, door.Path, true)
		writeProfileTestConfig(t, homeOf(t), fmt.Sprintf(`{
  "profiles": {"this-computer": {"base_url": %q, "endpoint": "app"}},
  "default_profile": "this-computer"
}`, server.URL))
		t.Setenv("HEY_TOKEN", "a-token-the-server-would-take")
		t.Setenv("HEY_BASE_URL", server.URL)
		for _, args := range runs {
			_, _, _ = runProfileCLI(t, append(args, "--json")...)
		}
		if len(door.Calls()) == 0 {
			t.Errorf("refused=%v: the door was never asked", refused)
		}
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("HEY's server was reached %d times", n)
	}
}

func homeOf(t *testing.T) string {
	t.Helper()
	return os.Getenv("XDG_CONFIG_HOME")
}

// An address missing from a Screener the app lists only part of was not searched for:
// that is no answer that nobody sends from it.
func TestAppScreenerAddressInAPartialListIsNotNotFound(t *testing.T) {
	door := appWritesDoor(t)
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "screener", "approve", "someone-unlisted@example.com")
	if output.ExitCodeFor(err) != output.ExitAPI || !strings.Contains(apiErrorHint(err), "hey -P this-computer screener list") {
		t.Fatalf("err = %v (hint %q)", err, apiErrorHint(err))
	}
	if names := appCallNames(appCalls(t, door)); names != "screener.list" {
		t.Errorf("calls = %s", names)
	}
}

// Every hint on an app profile that names a command names it on this profile: run on a
// server default profile, a listing shows the server's ids, and the next command acts
// there.
func TestAppHintsNameTheProfile(t *testing.T) {
	door := appWritesDoor(t)
	door.Answer("screener.list", map[string]any{"waiting": []any{}, "waiting_count": 0, "decided": []any{}})
	appTestHome(t, door.Path, true)

	for _, args := range [][]string{
		{"screener", "approve", "nobody@example.com"},
		{"screener", "approve", "51", "52"},
		{"screener", "deny", "abc"},
		{"compose", "--draft", "--to", "marcus@example.com", "-m", "Saturday works"},
		{"compose", "--draft", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works", "--thread-id", "801"},
		{"reply", "abc", "--draft", "-m", "Works for me"},
		{"forward", "801", "--to", "dana@example.com"},
	} {
		_, _, err := runProfileCLI(t, args...)
		if err == nil || !strings.Contains(err.Error()+" "+apiErrorHint(err), "hey -P this-computer ") {
			t.Errorf("%v: err = %v (hint %q)", args, err, apiErrorHint(err))
		}
	}
}

// However the app profile is chosen, the Imbox comes from the door and the envelope says
// so; without the gate the same run is refused. Neither reaches HEY's server.
func TestAppImboxRoutesByEveryWayOfChoosingTheProfile(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		env     string
		def     string
		gate    bool
		refused bool
	}{
		{"default profile", nil, "", "this-computer", true, false},
		{"-P over a server default", []string{"-P", "this-computer"}, "", "hey.com", true, false},
		{"HEY_PROFILE over a server default", nil, "this-computer", "hey.com", true, false},
		{"default profile, no gate", nil, "", "this-computer", false, true},
		{"-P, no gate", []string{"-P", "this-computer"}, "", "hey.com", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			door := appImboxDoor(t)
			appTestHome(t, door.Path, test.gate)
			server := noRequestServer(t)
			writeProfileTestConfig(t, os.Getenv("XDG_CONFIG_HOME"), fmt.Sprintf(`{
  "profiles": {
    "this-computer": {"base_url": %q, "endpoint": "app"},
    "hey.com": {"base_url": %q}
  },
  "default_profile": %q
}`, server.URL, server.URL, test.def))
			t.Setenv("HEY_PROFILE", test.env)
			t.Setenv("HEY_TOKEN", "a-token-the-server-would-take")

			stdout, _, err := runProfileCLI(t, append(test.args, "box", "view", "imbox", "--json")...)
			if test.refused {
				if output.ExitCodeFor(err) != output.ExitUsage || len(door.Calls()) != 0 {
					t.Errorf("err = %v, calls %s", err, door.Calls())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if response := appEnvelope(t, stdout); response.Meta["endpoint"] != "app" || response.Meta["profile"] != "this-computer" {
				t.Errorf("meta = %v", response.Meta)
			}
			if len(door.Calls()) != 1 {
				t.Errorf("calls = %s", door.Calls())
			}
		})
	}
}
