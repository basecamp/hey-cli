package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/localapp/localapptest"
	"github.com/basecamp/hey-cli/internal/output"
)

func localDraftAnswer(id int64, kind string, sendable bool) map[string]any {
	draft := map[string]any{
		"id": id, "kind": kind, "account": "ada@hey.example", "from": "ada@hey.example",
		"to": []string{"marcus@example.com"}, "cc": []string{}, "bcc": []string{},
		"subject": "Stint plan", "summary": "Saturday works", "saved_at": "2026-11-11T21:00:00Z",
		"sendable": sendable,
	}
	if kind == "reply" {
		draft["thread"] = 801
		draft["subject"] = "Re: Stint plan"
	}
	return draft
}

func TestLocalDraftsListsEveryDraftAndWhetherHEYWouldAskToSendIt(t *testing.T) {
	door := localapptest.Start(t)
	scheduled := localDraftAnswer(1731280000999, "new", false)
	scheduled["sends_at"] = "2026-11-12T08:00:00Z"
	door.Answer("drafts.list", map[string]any{"drafts": []any{
		localDraftAnswer(1731280000123, "reply", true), scheduled,
	}})

	stdout, _, err := runLocal(t, door.Path, "drafts", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data []localDraft `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || len(envelope.Data) != 2 ||
		envelope.Data[0].ID != 1731280000123 || !*envelope.Data[0].Sendable || *envelope.Data[0].Thread != 801 ||
		envelope.Data[1].SendsAt == nil || *envelope.Data[1].Sendable {
		t.Errorf("drafts = %s (%v)", stdout, err)
	}

	styled, _, err := runLocal(t, door.Path, "drafts", "--styled")
	if err != nil || !strings.Contains(styled, "1731280000123") || !strings.Contains(styled, "marcus@example.com") ||
		!strings.Contains(styled, "scheduled") {
		t.Errorf("styled = %q (%v)", styled, err)
	}
	ids, _, err := runLocal(t, door.Path, "drafts", "--ids-only")
	if err != nil || ids != "1731280000123\n1731280000999\n" {
		t.Errorf("--ids-only = %q (%v)", ids, err)
	}
}

func TestLocalWriteSavesADraftAndSaysHowToSendIt(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("draft.write", localDraftAnswer(1731280000123, "new", true))

	stdout, _, err := runLocal(t, door.Path, "write", "--to", "Marcus <marcus@example.com>", "--subject", "Stint plan", "-m", "Saturday works", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var call struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if calls := door.Calls(); len(calls) != 1 || json.Unmarshal(calls[0], &call) != nil || call.Name != "draft.write" ||
		len(call.Arguments) != 3 || call.Arguments["to"] != "Marcus <marcus@example.com>" ||
		call.Arguments["subject"] != "Stint plan" || call.Arguments["body"] != "Saturday works" {
		t.Errorf("calls = %s", calls)
	}
	if !strings.Contains(stdout, `"id": 1731280000123`) || !strings.Contains(stdout, "hey local send 1731280000123") {
		t.Errorf("stdout = %s", stdout)
	}
	styled, _, err := runLocal(t, door.Path, "write", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works", "--styled")
	if err != nil || !strings.Contains(styled, "Draft 1731280000123 saved") || !strings.Contains(styled, "hey local send 1731280000123") ||
		!strings.Contains(styled, "Nothing has been sent") {
		t.Errorf("styled = %q (%v)", styled, err)
	}
}

func TestLocalWriteWithNoMessageAsksTheAppNothing(t *testing.T) {
	door := localapptest.Start(t)
	previous := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = previous })

	_, _, err := runLocal(t, door.Path, "write", "--to", "marcus@example.com", "--subject", "Stint plan")
	if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(apierr.AsError(err).Message, "no message") {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("calls = %s", calls)
	}
}

// A draft HEY could not send from here now says so, so nobody asks for a send that waits on nothing.
func TestLocalWriteOfADraftHEYWouldNotSendSaysSo(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("draft.write", localDraftAnswer(1731280000123, "new", false))

	styled, _, err := runLocal(t, door.Path, "write", "--to", "", "--subject", "Notes", "-m", "Later", "--styled")
	if err != nil || strings.Contains(styled, "hey local send") || !strings.Contains(styled, "can't send it from here") {
		t.Errorf("styled = %q (%v)", styled, err)
	}
}

func TestLocalReplySavesADraftOnTheThread(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("draft.reply", localDraftAnswer(1731280000123, "reply", true))

	styled, _, err := runLocal(t, door.Path, "reply", "801", "-m", "Saturday works", "--styled")
	if err != nil || !strings.Contains(styled, "Draft 1731280000123 saved") || !strings.Contains(styled, "hey local send 1731280000123") {
		t.Errorf("styled = %q (%v)", styled, err)
	}
	if calls := door.Calls(); len(calls) != 1 ||
		string(calls[0]) != `{"arguments":{"body":"Saturday works","thread":801},"name":"draft.reply"}` {
		t.Errorf("calls = %s", calls)
	}
}

func TestLocalAReplyAnsweredOnAnotherThreadIsTheAppsToExplain(t *testing.T) {
	door := localapptest.Start(t)
	elsewhere := localDraftAnswer(1731280000123, "reply", true)
	elsewhere["thread"] = 802
	door.Answer("draft.reply", elsewhere)

	_, _, err := runLocal(t, door.Path, "reply", "801", "-m", "Saturday works")
	if output.ExitCodeFor(err) != output.ExitAPI || apierr.AsError(err).Hint != "Check in the HEY app what it did" {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
}

// The whole of it: an agent writes a draft, asks to send it by the id it was given, and it
// waits for the person in HEY.
func TestLocalAnAgentWritesADraftThenItsSendWaitsInHEY(t *testing.T) {
	door := pendingDoor(t)
	door.Answer("draft.write", localDraftAnswer(1731280000123, "new", true))

	stdout, _, err := runLocal(t, door.Path, "write", "--to", "marcus@example.com", "--subject", "Stint plan", "-m", "Saturday works", "--json")
	var written struct {
		Data localDraft `json:"data"`
	}
	if err != nil || json.Unmarshal([]byte(stdout), &written) != nil {
		t.Fatalf("write = %s (%v)", stdout, err)
	}
	_, _, err = runLocal(t, door.Path, "send", fmt.Sprint(written.Data.ID))
	if output.ExitCodeFor(err) != output.ExitPending {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if calls := door.Calls(); len(calls) != 2 || string(calls[1]) != `{"arguments":{"draft":1731280000123},"name":"message.send"}` {
		t.Errorf("calls = %s", calls)
	}
}

// Each refusal of the draft commands says plainly what happened, with its exit code;
// nothing was saved or waits.
func TestLocalDraftRefusalsSayWhatHappened(t *testing.T) {
	cases := []struct {
		name, tool string
		refusal    map[string]any
		args       []string
		exit       int
		message    string
		hint       string
	}{
		{"not an address", "draft.write", map[string]any{"error": "not_an_address", "arg": "to", "given": []string{"marcus", "@@"}},
			[]string{"write", "--to", "marcus, @@", "--subject", "s", "-m", "b"}, output.ExitUsage,
			"HEY can't read these as addresses: marcus, @@", "Give the To line as addresses, or Name <address>, separated by commas"},
		{"too many recipients", "draft.write", map[string]any{"error": "not_saved", "refusal": "too_many_recipients"},
			[]string{"write", "--to", "a@example.com", "--subject", "s", "-m", "b"}, output.ExitUsage,
			"HEY didn't save the draft: it goes to more people than HEY sends one message to", "Write it to fewer people"},
		{"too large", "draft.reply", map[string]any{"error": "not_saved", "refusal": "too_large"},
			[]string{"reply", "801", "-m", "b"}, output.ExitAPI,
			"HEY didn't save the draft: it is larger than HEY sends in one message", "Shorten the message, or reply in HEY: a reply's quote counts too"},
		{"write too large", "draft.write", map[string]any{"error": "not_saved", "refusal": "too_large"},
			[]string{"write", "--to", "a@example.com", "--subject", "s", "-m", "b"}, output.ExitUsage,
			"HEY didn't save the draft: it is larger than HEY sends in one message", "Shorten the message"},
		{"reply to too many", "draft.reply", map[string]any{"error": "not_saved", "refusal": "too_many_recipients"},
			[]string{"reply", "801", "-m", "b"}, output.ExitAPI,
			"HEY didn't save the draft: it goes to more people than HEY sends one message to", "Reply to this thread in HEY"},
		{"too many files", "draft.reply", map[string]any{"error": "not_saved", "refusal": "too_many_files"},
			[]string{"reply", "801", "-m", "b"}, output.ExitAPI,
			"HEY didn't save the draft: it holds more files than HEY sends in one message", "Reply to this thread in HEY"},
		{"not a sender", "message.send", map[string]any{"error": "not_a_sender", "draft": 1731280000123, "from": "old@hey.example"},
			[]string{"send", "1731280000123"}, output.ExitAPI,
			"HEY won't send draft 1731280000123: it goes from old@hey.example, which this account no longer sends from", "Change From in HEY, or write a new draft with hey local write"},
		{"thread unreadable", "draft.reply", map[string]any{"error": "unavailable"},
			[]string{"reply", "801", "-m", "b"}, output.ExitAPI,
			"HEY couldn't read thread 801, or the draft it saved went before HEY listed it", "Check hey local drafts before writing it again; hey local imbox lists the threads a reply takes"},
		{"written draft gone", "draft.write", map[string]any{"error": "unavailable"},
			[]string{"write", "--to", "a@example.com", "--subject", "s", "-m", "b"}, output.ExitAPI,
			"HEY couldn't show the draft it saved: it may have gone before HEY listed it", "Check hey local drafts before writing it again"},
		{"no draft sendable now", "message.send", map[string]any{"error": "disabled", "arg": "draft", "of": "draft", "available": []int64{}},
			[]string{"send", "1731280000999"}, output.ExitAPI,
			"HEY won't send draft 1731280000999 now: it is scheduled, goes to nobody, has a file still uploading, or is a forward", "No draft can be sent from here now: hey local drafts shows which are sendable"},
		{"draft not sendable now", "message.send", map[string]any{"error": "disabled", "arg": "draft", "of": "draft", "available": []int64{1731280000123}},
			[]string{"send", "1731280000999"}, output.ExitUsage,
			"HEY won't send draft 1731280000999 now: it is scheduled, goes to nobody, has a file still uploading, or is a forward", "draft takes one of: 1731280000123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			door := localapptest.Start(t)
			door.Refuse(tc.tool, tc.refusal)

			_, _, err := runLocal(t, door.Path, tc.args...)
			failure := apierr.AsError(err)
			if output.ExitCodeFor(err) != tc.exit || failure.Message != tc.message || failure.Hint != tc.hint {
				t.Errorf("exit = %d, message = %q, hint = %q", output.ExitCodeFor(err), failure.Message, failure.Hint)
			}
			if refusal, _ := failure.Meta["refusal"].(map[string]any); refusal["error"] != tc.refusal["error"] {
				t.Errorf("meta.refusal = %v", failure.Meta["refusal"])
			}
		})
	}
}

// A draft saved is never followed by a failure: --ids-only prints its id and --count 1, so
// nobody retries a run that wrote one, and writes another.
func TestLocalADraftSavedGivesItsIDToIDsOnly(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("draft.write", localDraftAnswer(1731280000123, "new", true))
	door.Answer("draft.reply", localDraftAnswer(1731280000123, "reply", true))

	for _, args := range [][]string{
		{"write", "--to", "a@example.com", "--subject", "s", "-m", "b", "--ids-only"},
		{"reply", "801", "-m", "b", "--ids-only"},
		{"call", "draft.write", `{"to":"a@example.com","subject":"s","body":"b"}`, "--ids-only"},
	} {
		stdout, _, err := runLocal(t, door.Path, args...)
		if err != nil || stdout != "1731280000123\n" {
			t.Errorf("%v: stdout = %q (%v)", args, stdout, err)
		}
	}
	count, _, err := runLocal(t, door.Path, "write", "--to", "a@example.com", "--subject", "s", "-m", "b", "--count")
	if err != nil || strings.TrimSpace(count) != "1" {
		t.Errorf("--count = %q (%v)", count, err)
	}
}

// Leaving out --to would write a draft to nobody that HEY won't send; --to "" still can.
func TestLocalWriteAsksForTheToLine(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("draft.write", localDraftAnswer(1731280000123, "new", false))

	_, _, err := runLocal(t, door.Path, "write", "--subject", "s", "-m", "b")
	if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(apierr.AsError(err).Message, "--to") {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("calls = %s", calls)
	}
	if _, _, err := runLocal(t, door.Path, "write", "--to", "", "--subject", "s", "-m", "b"); err != nil {
		t.Errorf("--to \"\": %v", err)
	}
}

// --ids-only keeps the id alone on stdout, but a draft HEY won't send says so on stderr,
// so nobody asks to send it and meets a refusal.
func TestLocalADraftHEYWontSendSaysSoUnderIDsOnly(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("draft.write", localDraftAnswer(1731280000123, "new", false))

	stdout, stderr, err := runLocal(t, door.Path, "write", "--to", "", "--subject", "s", "-m", "b", "--ids-only")
	if err != nil || stdout != "1731280000123\n" || !strings.Contains(stderr, "HEY would not send it from here") {
		t.Errorf("stdout = %q, stderr = %q (%v)", stdout, stderr, err)
	}
}

// A draft saved as some other kind than asked is the app's to explain, as one on another thread.
func TestLocalADraftOfAnotherKindIsTheAppsToExplain(t *testing.T) {
	door := localapptest.Start(t)
	onTheThread := localDraftAnswer(1731280000123, "new", true)
	onTheThread["thread"] = 801
	door.Answer("draft.reply", onTheThread)
	asReply := localDraftAnswer(1731280000123, "reply", true)
	door.Answer("draft.write", asReply)

	for _, args := range [][]string{{"reply", "801", "-m", "b"}, {"write", "--to", "a@example.com", "--subject", "s", "-m", "b"}} {
		_, _, err := runLocal(t, door.Path, args...)
		if output.ExitCodeFor(err) != output.ExitAPI || apierr.AsError(err).Hint != "Check in the HEY app what it did" {
			t.Errorf("%v: exit = %d (%v)", args, output.ExitCodeFor(err), err)
		}
	}
}

// The app's own words on a refusal stay in the message, whichever plain words lead it.
func TestLocalARefusalKeepsTheAppsDetail(t *testing.T) {
	door := localapptest.Start(t)
	door.Refuse("message.send", map[string]any{"error": "not_a_sender", "draft": 1731280000123, "from": "old@hey.example", "detail": "Address removed"})

	_, _, err := runLocal(t, door.Path, "send", "1731280000123")
	if message := apierr.AsError(err).Message; !strings.HasPrefix(message, "HEY won't send draft 1731280000123") || !strings.HasSuffix(message, "(Address removed)") {
		t.Errorf("message = %q", message)
	}
}
