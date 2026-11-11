package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/localapp/localapptest"
	"github.com/basecamp/hey-cli/internal/output"
)

// runLocal runs hey local against socket, signed out of HEY and with HEY's server one
// that fails the test if anything reaches it: hey local talks to the app alone.
func runLocal(t *testing.T, socket string, args ...string) (string, string, error) {
	t.Helper()
	heyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("hey local reached HEY's server: %s %s", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(heyServer.Close)

	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", heyServer.URL)
	t.Setenv("HEY_LOCAL_SOCKET", socket)
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"local"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func localEnvelope(t *testing.T, stdout string) output.Response {
	t.Helper()
	var resp output.Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, stdout)
	}
	return resp
}

func imboxDoor(t *testing.T) *localapptest.Door {
	t.Helper()
	door := localapptest.Start(t)
	door.Answer("imbox.list", map[string]any{
		"threads": []map[string]any{
			{"id": 301, "topic": 801, "subject": "Stint plan for Saturday", "summary": "Here's the order",
				"sender": "Marcus Ortiz", "sender_id": 41, "seen": false, "bubbled_up": false},
			{"id": 302, "topic": 802, "subject": "Invoice 2026-114", "summary": "Your invoice",
				"sender": "Hollis Heimboch", "sender_id": 42, "seen": true, "bubbled_up": false},
		},
		"has_more": false,
	})
	return door
}

func TestLocalToolsListsWhatTheAppOffersAndWhatEachDoes(t *testing.T) {
	door := localapptest.Start(t)
	door.Offer("imbox.list", "Lists the threads in the Imbox", "read")
	door.Offer("screener.decide", "Screens a sender in or out", "reversible", "sender", "decision")
	door.Offer("message.send", "Sends a saved draft once you confirm it in HEY", "reaches_people")

	stdout, _, err := runLocal(t, door.Path, "tools", "--json")
	if err != nil {
		t.Fatal(err)
	}
	tools := decodeScreenerData[[]localTool](t, localEnvelope(t, stdout).Data)
	if len(tools) != 3 || tools[0].Name != "imbox.list" || tools[2].Effect != "reaches_people" {
		t.Fatalf("tools = %+v", tools)
	}

	styled, _, err := runLocal(t, door.Path, "tools", "--styled")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled, "message.send") || !strings.Contains(styled, "waits for you in HEY") || !strings.Contains(styled, "decision, sender") {
		t.Errorf("styled = %s", styled)
	}
}

func TestLocalCallTypesEachArgumentAsJSONWouldRead(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "out"})

	stdout, _, err := runLocal(t, door.Path, "call", "screener.decide", "--arg", "sender=42", "--arg", "decision=out", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{"decision":"out","sender":42},"name":"screener.decide"}` {
		t.Errorf("calls = %s", calls)
	}
	answer := decodeScreenerData[map[string]any](t, localEnvelope(t, stdout).Data)
	if answer["screened"] != "out" {
		t.Errorf("answer = %+v", answer)
	}
}

func TestLocalCallTakesAJSONObjectAndArgsOverIt(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "in"})

	_, _, err := runLocal(t, door.Path, "call", "screener.decide", `{"sender":42,"decision":"out"}`, "--arg", "decision=in", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{"decision":"in","sender":42},"name":"screener.decide"}` {
		t.Errorf("calls = %s", calls)
	}
}

func TestLocalCallRefusesArgumentsThatAreNotAnObject(t *testing.T) {
	door := localapptest.Start(t)

	for _, args := range [][]string{
		{"call", "imbox.list", `[1,2]`},
		{"call", "imbox.list", "--arg", "sender"},
	} {
		_, _, err := runLocal(t, door.Path, args...)
		if output.ExitCodeFor(err) != output.ExitUsage {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("calls = %s", calls)
	}
}

func TestLocalImboxListsWhatTheAppHolds(t *testing.T) {
	door := imboxDoor(t)

	stdout, _, err := runLocal(t, door.Path, "imbox", "--json")
	if err != nil {
		t.Fatal(err)
	}
	resp := localEnvelope(t, stdout)
	threads := decodeScreenerData[[]localThread](t, resp.Data)
	if len(threads) != 2 || threads[0].Subject != "Stint plan for Saturday" || threads[0].Topic != 801 {
		t.Fatalf("threads = %+v", threads)
	}
	if resp.Summary != "2 threads in the Imbox" {
		t.Errorf("summary = %q", resp.Summary)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{},"name":"imbox.list"}` {
		t.Errorf("calls = %s", calls)
	}

	styled, _, err := runLocal(t, door.Path, "imbox", "--styled")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled, "Stint plan for Saturday") || !strings.Contains(styled, "Marcus Ortiz") || !strings.Contains(styled, "301") || !strings.Contains(styled, "801") {
		t.Errorf("styled = %s", styled)
	}
}

func TestLocalScreenerListNamesTheSendersADecisionTakes(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.list", map[string]any{
		"waiting":       []map[string]any{{"id": 51, "name": "Priya Natarajan", "address": "priya@example.com"}},
		"waiting_count": 1,
		"decided":       []map[string]any{{"id": 52, "name": "Glenn Hoffman", "address": "glenn@example.org", "screened_in": true}},
	})

	styled, _, err := runLocal(t, door.Path, "screener", "list", "--styled")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"51", "Priya Natarajan", "waiting", "52", "screened in"} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled lacks %q:\n%s", want, styled)
		}
	}
}

func TestLocalScreenerDenyScreensTheSenderOutAndSaysHowToUndoIt(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "out"})

	stdout, _, err := runLocal(t, door.Path, "screener", "deny", "42", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{"decision":"out","sender":42},"name":"screener.decide"}` {
		t.Errorf("calls = %s", calls)
	}
	resp := localEnvelope(t, stdout)
	if len(resp.Breadcrumbs) != 1 || resp.Breadcrumbs[0].Command != "hey local screener approve 42" {
		t.Errorf("breadcrumbs = %+v", resp.Breadcrumbs)
	}

	styled, _, err := runLocal(t, door.Path, "screener", "deny", "42", "--styled")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled, "hey local screener approve 42") {
		t.Errorf("styled = %s", styled)
	}
}

func TestLocalScreenerApproveScreensTheSenderIn(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "in"})

	if _, _, err := runLocal(t, door.Path, "screener", "approve", "42", "--json"); err != nil {
		t.Fatal(err)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{"decision":"in","sender":42},"name":"screener.decide"}` {
		t.Errorf("calls = %s", calls)
	}
}

func TestLocalScreenerRefusesASenderThatIsNotAnID(t *testing.T) {
	door := localapptest.Start(t)

	_, _, err := runLocal(t, door.Path, "screener", "deny", "glenn@example.org")
	if output.ExitCodeFor(err) != output.ExitUsage {
		t.Errorf("err = %v", err)
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("calls = %s", calls)
	}
}

func pendingDoor(t *testing.T) *localapptest.Door {
	t.Helper()
	door := localapptest.Start(t)
	door.Answer("message.send", map[string]any{
		"status":       "pending",
		"confirmation": 7,
		"snapshot": map[string]any{
			"draft": 1731280000123, "account": "jason@example.com", "from": "jason@example.com",
			"to": []string{"marcus@example.com"}, "cc": []string{}, "bcc": []string{},
			"subject": "Re: Stint plan for Saturday", "body": "Works for me.", "rich": false,
			"attachments": []any{}, "thread": 801,
		},
	})
	return door
}

// A send waits in HEY's window for the person, so it is neither done nor failed: it says
// so and exits with its own code.
func TestLocalSendWaitingInHEYIsPendingNotSentNorFailed(t *testing.T) {
	door := pendingDoor(t)

	stdout, stderr, err := runLocal(t, door.Path, "send", "1731280000123", "--json")
	if !errors.Is(err, output.ErrPending) || output.ExitCodeFor(err) != output.ExitPending {
		t.Fatalf("err = %v (exit %d)", err, output.ExitCodeFor(err))
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{"draft":1731280000123},"name":"message.send"}` {
		t.Errorf("calls = %s", calls)
	}
	resp := localEnvelope(t, stdout)
	if !strings.Contains(resp.Summary, "Waiting in HEY") || !strings.Contains(resp.Summary, "nothing is sent") {
		t.Errorf("summary = %q", resp.Summary)
	}
	if resp.Meta["status"] != "pending" {
		t.Errorf("meta = %+v", resp.Meta)
	}
	pending := decodeScreenerData[map[string]any](t, resp.Data)
	if pending["confirmation"] != float64(7) {
		t.Errorf("data = %+v", pending)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}

	styled, _, err := runLocal(t, door.Path, "send", "1731280000123", "--styled")
	if !errors.Is(err, output.ErrPending) {
		t.Fatalf("styled err = %v", err)
	}
	for _, want := range []string{"Waiting in HEY", "Nothing has been sent", "marcus@example.com", "Re: Stint plan for Saturday"} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled lacks %q:\n%s", want, styled)
		}
	}
}

func TestLocalCallOfASendIsPendingToo(t *testing.T) {
	door := pendingDoor(t)

	_, _, err := runLocal(t, door.Path, "call", "message.send", "--arg", "draft=1731280000123", "--json")
	if output.ExitCodeFor(err) != output.ExitPending {
		t.Fatalf("err = %v (exit %d)", err, output.ExitCodeFor(err))
	}
}

func TestLocalWithNoSocketSaysToTurnTheDoorOn(t *testing.T) {
	_, _, err := runLocal(t, filepath.Join(t.TempDir(), "agent.sock"), "imbox")

	failure := apierr.AsError(err)
	if output.ExitCodeFor(err) != output.ExitNetwork {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if !strings.Contains(failure.Hint, `Turn on "Let agents use HEY"`) {
		t.Errorf("hint = %q", failure.Hint)
	}
}

func TestLocalWithNoRuntimeFolderSaysWhereToLook(t *testing.T) {
	for _, runtime := range []string{"", "run/user/1000"} {
		t.Setenv("XDG_RUNTIME_DIR", runtime)

		_, _, err := runLocal(t, "", "imbox")

		failure := apierr.AsError(err)
		if !strings.Contains(failure.Message, "XDG_RUNTIME_DIR is not set to an absolute path") || !strings.Contains(failure.Hint, "--socket") {
			t.Errorf("XDG_RUNTIME_DIR=%q: err = %q, hint = %q", runtime, failure.Message, failure.Hint)
		}
	}
}

func TestLocalASocketFlagOutranksTheEnvironment(t *testing.T) {
	door := imboxDoor(t)

	_, _, err := runLocal(t, filepath.Join(t.TempDir(), "elsewhere.sock"), "imbox", "--socket", door.Path, "--json")
	if err != nil {
		t.Fatal(err)
	}
}

// The app answers a tool it has not got with -32602, which is the caller's mistake.
func TestLocalAnUnknownToolIsAUsageErrorThatPointsAtTheList(t *testing.T) {
	door := localapptest.Start(t)

	_, _, err := runLocal(t, door.Path, "call", "drafts.empty", "--json")
	if output.ExitCodeFor(err) != output.ExitUsage {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if !strings.Contains(err.Error(), "drafts.empty") || !strings.Contains(apierr.AsError(err).Hint, "hey local tools") {
		t.Errorf("err = %v, hint = %q", err, apierr.AsError(err).Hint)
	}
}

func TestLocalAServerErrorIsAnAPIFailure(t *testing.T) {
	door := localapptest.Start(t)
	door.Fail("tools/list", -32603, "internal error")

	_, _, err := runLocal(t, door.Path, "tools", "--json")
	if output.ExitCodeFor(err) != output.ExitAPI {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
}

func TestLocalAMalformedReplyIsAnAPIFailure(t *testing.T) {
	door := localapptest.Start(t)
	door.Reply("tools/call", `{"jsonrpc":"2.0","id":2,"result":`)

	_, _, err := runLocal(t, door.Path, "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitAPI {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("err = %v", err)
	}
}

// An answer the app gives in a shape the command does not know is not shown as an empty
// Imbox.
func TestLocalAnImboxInAnotherShapeIsAnAPIFailure(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("imbox.list", map[string]any{"threads": "none"})

	_, _, err := runLocal(t, door.Path, "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitAPI {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
}

// A sender the app does not hold is refused as the app's Arg.Ref refuses it: unavailable,
// naming the argument and the ids it would take, or disabled where the sender is there but
// the decision is off for them. Either is the caller's to fix.
func TestLocalARefusedArgumentIsAUsageErrorNamingWhatItTakes(t *testing.T) {
	for _, why := range []string{"unavailable", "disabled"} {
		t.Run(why, func(t *testing.T) {
			door := localapptest.Start(t)
			door.Refuse("screener.decide", map[string]any{
				"error": why, "arg": "sender", "of": "sender", "given": 99, "available": []int64{51, 1731280000123},
			})

			_, _, err := runLocal(t, door.Path, "screener", "deny", "99", "--json")
			if output.ExitCodeFor(err) != output.ExitUsage {
				t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
			}
			failure := apierr.AsError(err)
			if !strings.Contains(failure.Message, why) || failure.Hint != "sender takes one of: 51, 1731280000123" {
				t.Errorf("message = %q, hint = %q", failure.Message, failure.Hint)
			}
			if strings.Contains(failure.Hint, "Let agents use HEY") {
				t.Errorf("hint = %q: the door is open, it answered", failure.Hint)
			}
			refusal, _ := failure.Meta["refusal"].(map[string]any)
			if refusal["arg"] != "sender" {
				t.Errorf("meta = %+v", failure.Meta)
			}
		})
	}
}

// A list the app could not load is nothing the caller sent wrong: a read's refusal names
// no argument, and a decision's names one with nothing available.
func TestLocalAListTheAppCouldNotLoadIsAnAPIFailure(t *testing.T) {
	door := localapptest.Start(t)
	door.Refuse("imbox.list", map[string]any{"error": "unavailable"})
	door.Refuse("screener.decide", map[string]any{"error": "unavailable", "arg": "sender", "of": "sender", "given": 42, "available": []int{}})
	door.Refuse("screener.list", map[string]any{"error": "no_such_case", "arg": "box", "cases": []string{}})

	for _, args := range [][]string{{"imbox"}, {"screener", "deny", "42"}, {"screener", "list"}} {
		_, _, err := runLocal(t, door.Path, append(args, "--json")...)
		if output.ExitCodeFor(err) != output.ExitAPI {
			t.Errorf("%v: exit = %d (%v)", args, output.ExitCodeFor(err), err)
		}
	}
}

// Pending reaches every format as pending: a format with no list to show writes what it
// can and still exits 9.
func TestLocalAPendingSendIsPendingInEveryFormat(t *testing.T) {
	door := pendingDoor(t)

	for _, format := range [][]string{{"--ids-only"}, {"--count"}, {"--quiet"}, {"--markdown"}, {"--jq", ".data.confirmation"}, {"--jq", ".nope | error"}} {
		stdout, stderr, err := runLocal(t, door.Path, append([]string{"send", "1731280000123"}, format...)...)
		if output.ExitCodeFor(err) != output.ExitPending {
			t.Errorf("%v: exit = %d (%v)", format, output.ExitCodeFor(err), err)
		}
		if format[0] == "--ids-only" && strings.TrimSpace(stdout) != "7" {
			t.Errorf("--ids-only: stdout = %q", stdout)
		}
		if format[0] == "--markdown" && (!strings.Contains(stdout, "**confirmation:** 7") || strings.Contains(stdout, "[55]")) {
			t.Errorf("--markdown: stdout = %q", stdout)
		}
		if (format[0] == "--ids-only" || format[0] == "--count") && !strings.Contains(stderr, "Waiting in HEY") {
			t.Errorf("%v: stderr = %q", format, stderr)
		}
	}
}

// Whatever the command, an answer that waits for the person is not read on as done.
func TestLocalSugarAnsweredPendingIsPendingNotDone(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"status": "pending", "confirmation": 8})
	door.Answer("imbox.list", map[string]any{"status": "pending", "confirmation": 9})

	for _, args := range [][]string{{"screener", "approve", "42"}, {"imbox"}} {
		stdout, _, err := runLocal(t, door.Path, append(args, "--styled")...)
		if output.ExitCodeFor(err) != output.ExitPending {
			t.Errorf("%v: exit = %d (%v)", args, output.ExitCodeFor(err), err)
		}
		if strings.Contains(stdout, "screened") || strings.Contains(stdout, "Nothing in the Imbox") {
			t.Errorf("%v: stdout = %q", args, stdout)
		}
	}
}

// A decision the app says it took some other way is not reported as the one asked for.
func TestLocalADecisionAnsweredOtherwiseIsAnAPIFailure(t *testing.T) {
	for name, answer := range map[string]map[string]any{
		"empty":          {},
		"the other way":  {"sender": 42, "screened": "out"},
		"another sender": {"sender": 43, "screened": "in"},
	} {
		t.Run(name, func(t *testing.T) {
			door := localapptest.Start(t)
			door.Answer("screener.decide", answer)

			stdout, _, err := runLocal(t, door.Path, "screener", "approve", "42", "--styled")
			if output.ExitCodeFor(err) != output.ExitAPI {
				t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
			}
			if strings.Contains(stdout, "screened") {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
}

func TestLocalAnArgumentWithTrailingJunkIsAWord(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "out"})

	if _, _, err := runLocal(t, door.Path, "call", "screener.decide", "--arg", "sender=42}", "--json"); err != nil {
		t.Fatal(err)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{"sender":"42}"},"name":"screener.decide"}` {
		t.Errorf("calls = %s", calls)
	}

	_, _, err := runLocal(t, door.Path, "call", "screener.decide", `{"sender":42}]`)
	if output.ExitCodeFor(err) != output.ExitUsage {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
}

// The app closes a connection from another user account without a word.
func TestLocalAConnectionClosedUnansweredSaysWhoTheAppAnswers(t *testing.T) {
	path, listener := localapptest.Listen(t)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	_, _, err := runLocal(t, path, "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitNetwork {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if !strings.Contains(apierr.AsError(err).Hint, "user account") {
		t.Errorf("hint = %q", apierr.AsError(err).Hint)
	}
}

func TestLocalSignedOutOfTheAppIsAnAuthFailureThatNamesTheApp(t *testing.T) {
	door := localapptest.Start(t)
	door.Refuse("imbox.list", map[string]any{"error": "signed_out"})

	_, _, err := runLocal(t, door.Path, "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitAuth {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if hint := apierr.AsError(err).Hint; hint != "Sign in to HEY in the app" {
		t.Errorf("hint = %q: the app's sign-in is not the CLI's", hint)
	}
}

// An undo reaches the app the decision did: a --socket given goes into it, quoted.
func TestLocalAnUndoCarriesTheSocketItWasGiven(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "out"})

	stdout, _, err := runLocal(t, filepath.Join(t.TempDir(), "elsewhere.sock"), "--socket", door.Path, "screener", "deny", "42", "--json")
	if err != nil {
		t.Fatal(err)
	}
	resp := localEnvelope(t, stdout)
	if want := "hey local --socket '" + door.Path + "' screener approve 42"; len(resp.Breadcrumbs) != 1 || resp.Breadcrumbs[0].Command != want {
		t.Errorf("breadcrumbs = %+v, want %q", resp.Breadcrumbs, want)
	}
}

func screenerListDoor(t *testing.T, waitingCount int) *localapptest.Door {
	t.Helper()
	door := localapptest.Start(t)
	door.Answer("screener.list", map[string]any{
		"waiting":       []map[string]any{{"id": 51, "name": "Priya Natarajan", "address": "priya@example.com"}},
		"waiting_count": waitingCount,
		"decided":       []map[string]any{{"id": 52, "name": "Glenn Hoffman", "address": "glenn@example.org", "screened_in": false}},
	})
	return door
}

func TestLocalScreenerListGivesIDsAndACountOfEveryoneItHolds(t *testing.T) {
	door := screenerListDoor(t, 1)

	ids, _, err := runLocal(t, door.Path, "screener", "list", "--ids-only")
	if err != nil || ids != "51\n52\n" {
		t.Errorf("--ids-only = %q (%v)", ids, err)
	}
	count, _, err := runLocal(t, door.Path, "screener", "list", "--count")
	if err != nil || strings.TrimSpace(count) != "2" {
		t.Errorf("--count = %q (%v)", count, err)
	}
}

// waiting_count counts everyone waiting, which may be more than the app lists.
func TestLocalScreenerListSaysWhenMoreWaitThanItLists(t *testing.T) {
	door := screenerListDoor(t, 6)

	styled, _, err := runLocal(t, door.Path, "screener", "list", "--styled")
	if err != nil || !strings.Contains(styled, "5 more senders waiting than the app lists.") {
		t.Errorf("styled = %q (%v)", styled, err)
	}
	_, stderr, err := runLocal(t, door.Path, "screener", "list", "--ids-only")
	if err != nil || !strings.Contains(stderr, "5 more senders waiting") {
		t.Errorf("--ids-only stderr = %q (%v)", stderr, err)
	}

	empty := localapptest.Start(t)
	empty.Answer("screener.list", map[string]any{"waiting": []any{}, "waiting_count": 5, "decided": []any{}})
	styled, _, _ = runLocal(t, empty.Path, "screener", "list", "--styled")
	if strings.Contains(styled, "Nobody waits") || !strings.Contains(styled, "5 more senders waiting") {
		t.Errorf("empty styled = %q", styled)
	}
}

// The app may hold more of the Imbox than it has loaded, and a list that stops short says
// so in every format, never only in the envelope.
func TestLocalImboxSaysWhenTheAppHoldsMoreInEveryFormat(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("imbox.list", map[string]any{"threads": []any{}, "has_more": true})

	styled, _, _ := runLocal(t, door.Path, "imbox", "--styled")
	if strings.Contains(styled, "Nothing in the Imbox") || !strings.Contains(styled, "more threads than the app has loaded") {
		t.Errorf("styled = %q", styled)
	}
	for _, format := range []string{"--ids-only", "--count", "--quiet"} {
		_, stderr, err := runLocal(t, door.Path, "imbox", format)
		if err != nil || !strings.Contains(stderr, "more threads than the app has loaded") {
			t.Errorf("%s stderr = %q (%v)", format, stderr, err)
		}
	}
}

// A send the app took is pending; any other answer to it is not a send this knows.
func TestLocalASendNotAnsweredPendingIsAnAPIFailure(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("message.send", map[string]any{})

	stdout, _, err := runLocal(t, door.Path, "send", "1731280000123", "--json")
	if output.ExitCodeFor(err) != output.ExitAPI {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if strings.Contains(stdout, "HEY ran") {
		t.Errorf("stdout = %q", stdout)
	}
}

// A send answered otherwise may have done something in the app: the hint points there,
// and a plain-text answer still reaches the person.
func TestLocalASendAnsweredOtherwisePointsAtTheApp(t *testing.T) {
	door := localapptest.Start(t)
	door.AnswerRaw("message.send", map[string]any{
		"content": []map[string]any{{"type": "text", "text": "Sent to Ada"}},
	})

	_, _, err := runLocal(t, door.Path, "send", "1731280000123")
	failure := apierr.AsError(err)
	if output.ExitCodeFor(err) != output.ExitAPI || failure.Hint != "Check in the HEY app what it did" {
		t.Errorf("exit = %d, hint = %q (%v)", output.ExitCodeFor(err), failure.Hint, err)
	}
	if !strings.Contains(failure.Message, "Sent to Ada") {
		t.Errorf("message = %q", failure.Message)
	}
}

func TestLocalCallRefusesAnArgumentGivenTwice(t *testing.T) {
	door := localapptest.Start(t)

	_, _, err := runLocal(t, door.Path, "call", "screener.decide", "--arg", "sender=42", "--arg", "sender=43")
	if output.ExitCodeFor(err) != output.ExitUsage {
		t.Errorf("exit = %d (%v)", output.ExitCodeFor(err), err)
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("calls = %s", calls)
	}
}

// A decision the app says it took otherwise is the app's to explain, not an old hey.
func TestLocalADecisionAnsweredOtherwiseDoesNotBlameTheCLIsVersion(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "out"})

	_, _, err := runLocal(t, door.Path, "screener", "approve", "42")
	if hint := apierr.AsError(err).Hint; hint != "Check in the HEY app what it did" {
		t.Errorf("hint = %q", hint)
	}
}
