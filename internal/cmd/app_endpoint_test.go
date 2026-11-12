package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/localapp/localapptest"
	"github.com/basecamp/hey-cli/internal/output"
)

// appTestHome isolates a run whose default profile, this-computer, is answered by the HEY
// app at door, beside a server profile, hey.com. Both name a server that fails the test if
// anything reaches it: the app endpoint never falls through to HEY's server. gate sets
// HEY_EXPERIMENTAL=app.
func appTestHome(t *testing.T, door string, gate bool) {
	t.Helper()
	home := profileTestHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("reached HEY's server: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	writeProfileTestConfig(t, home, fmt.Sprintf(`{
  "profiles": {
    "this-computer": {"base_url": %q, "endpoint": "app"},
    "hey.com": {"base_url": %q}
  },
  "default_profile": "this-computer"
}`, server.URL, server.URL))
	t.Setenv("HEY_LOCAL_SOCKET", door)
	t.Setenv("HEY_EXPERIMENTAL", "")
	if gate {
		t.Setenv("HEY_EXPERIMENTAL", "app")
	}
}

func appEnvelope(t *testing.T, stdout string) output.Response {
	t.Helper()
	var response output.Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, stdout)
	}
	return response
}

func appData[T any](t *testing.T, response output.Response) T {
	t.Helper()
	encoded, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data T
	if err := json.Unmarshal(encoded, &data); err != nil {
		t.Fatalf("data %s: %v", encoded, err)
	}
	return data
}

func appImboxDoor(t *testing.T) *localapptest.Door {
	t.Helper()
	door := localapptest.Start(t)
	door.Answer("imbox.list", map[string]any{
		"threads": []map[string]any{
			{"id": 301, "topic": 801, "subject": "Stint plan for Saturday", "summary": "Here's the order we drive in",
				"sender": "Marcus Ortiz", "sender_id": 41, "seen": false, "bubbled_up": false},
			{"id": 302, "topic": 802, "subject": "Invoice 2026-114", "summary": "Your invoice for October",
				"sender": "Hollis Heimboch", "sender_id": 42, "seen": true, "bubbled_up": true},
		},
		"has_more": true,
	})
	door.Answer("screener.list", map[string]any{
		"waiting": []map[string]any{
			{"id": 51, "name": "Priya Raman", "address": "priya@example.com"},
		},
		"waiting_count": 3,
		"decided": []map[string]any{
			{"id": 52, "name": "Weekly Roundup", "address": "news@example.org", "screened_in": false},
			{"id": 53, "name": "Dana Whitfield", "address": "dana@example.com", "screened_in": true},
		},
	})
	door.Answer("drafts.list", map[string]any{
		"drafts": []map[string]any{
			{"id": 9001, "made_as": 1731280000123, "kind": "reply", "thread": 801, "account": "jason@example.com",
				"from": "jason@example.com", "to": []string{"marcus@example.com"}, "cc": []string{}, "bcc": []string{},
				"subject": "Re: Stint plan for Saturday", "summary": "Works for me", "saved_at": "2026-11-11T09:30:00Z", "sendable": true},
		},
	})
	return door
}

// Without HEY_EXPERIMENTAL=app a profile the app answers is refused, never answered from
// HEY's server in its place: nothing reaches the server or the door.
func TestAppProfileIsRefusedWithoutTheExperimentalGate(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, false)

	_, _, err := runProfileCLI(t, "box", "view", "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(err.Error(), "not in this build") {
		t.Fatalf("err = %v", err)
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the door was called: %s", calls)
	}
}

// Without the gate nothing about the app shows: profile list names no endpoint, a server
// profile's envelope carries no meta.endpoint, and hey app is no command at all.
func TestWithoutTheGateNothingAboutTheAppShows(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, false)

	response, _, err := runProfileJSON(t, "-P", "hey.com", "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range appData[[]map[string]any](t, response) {
		if _, ok := row["endpoint"]; ok {
			t.Errorf("profile list names an endpoint: %v", row)
		}
	}
	if _, ok := response.Meta["endpoint"]; ok {
		t.Errorf("meta = %v", response.Meta)
	}
	if _, _, err := runProfileCLI(t, "-P", "hey.com", "app", "tools"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("hey app without the gate: %v", err)
	}
	commands, _, err := runProfileCLI(t, "-P", "hey.com", "commands", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(commands, `"path": "app"`) || strings.Contains(commands, `"path": "app `) {
		t.Errorf("commands lists hey app: %s", commands)
	}
}

func TestAppProfileAnswersTheImboxFromTheDoor(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "box", "view", "imbox", "--json")
	if err != nil {
		t.Fatal(err)
	}
	response := appEnvelope(t, stdout)
	if response.Meta["profile"] != "this-computer" || response.Meta["endpoint"] != "app" || response.Meta["has_more"] != true {
		t.Errorf("meta = %v", response.Meta)
	}
	box := appData[struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Postings []struct {
			ID        int64  `json:"id"`
			TopicID   int64  `json:"topic_id"`
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Summary   string `json:"summary"`
			Seen      bool   `json:"seen"`
			BubbledUp bool   `json:"bubbled_up"`
			Creator   struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"creator"`
		} `json:"postings"`
	}](t, response)
	if box.Kind != "imbox" || len(box.Postings) != 2 {
		t.Fatalf("box = %+v", box)
	}
	first, second := box.Postings[0], box.Postings[1]
	if first.ID != 301 || first.TopicID != 801 || first.Kind != "topic" || first.Name != "Stint plan for Saturday" ||
		first.Summary != "Here's the order we drive in" || first.Seen || first.Creator.Name != "Marcus Ortiz" || first.Creator.ID != 41 {
		t.Errorf("first = %+v", first)
	}
	if !second.Seen || !second.BubbledUp {
		t.Errorf("second = %+v", second)
	}
	if !strings.Contains(response.Notice, "more threads than the HEY app has loaded") {
		t.Errorf("notice = %q", response.Notice)
	}
	if calls := door.Calls(); len(calls) != 1 || !strings.Contains(string(calls[0]), `"imbox.list"`) {
		t.Errorf("calls = %s", calls)
	}
}

func TestAppImboxInEveryFormat(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	ids, _, err := runProfileCLI(t, "box", "view", "imbox", "--ids-only")
	if err != nil || ids != "301\n302\n" {
		t.Errorf("--ids-only = %q, %v", ids, err)
	}
	count, _, err := runProfileCLI(t, "box", "view", "imbox", "--count")
	if err != nil || strings.TrimSpace(count) != "2" {
		t.Errorf("--count = %q, %v", count, err)
	}
	styled, _, err := runProfileCLI(t, "box", "view", "imbox", "--styled")
	if err != nil || !strings.Contains(styled, "Box: Imbox") || !strings.Contains(styled, "Marcus Ortiz") || !strings.Contains(styled, "801") {
		t.Errorf("--styled = %q, %v", styled, err)
	}
	markdown, _, err := runProfileCLI(t, "box", "view", "imbox", "--markdown")
	if err != nil || !strings.Contains(markdown, "# Imbox") || !strings.Contains(markdown, "**Total threads:** 2") {
		t.Errorf("--markdown = %q, %v", markdown, err)
	}
	limited, _, err := runProfileCLI(t, "box", "view", "imbox", "--limit", "1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if rows := appData[struct {
		Postings []any `json:"postings"`
	}](t, appEnvelope(t, limited)).Postings; len(rows) != 1 {
		t.Errorf("--limit 1 = %d rows", len(rows))
	}
}

// The app holds the Imbox alone: another box, and a cursor it never gave, are refused
// before the door is asked.
func TestAppRefusesWhatItDoesNotServe(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	for _, args := range [][]string{
		{"box", "view", "feed"},
		{"box", "view", "1234"},
		{"box", "imbox", "--page", "abc"},
		{"screener", "list", "--page", "abc"},
		{"box", "view", "imbox", "--all"},
		{"screener", "list", "--all"},
		{"screener", "history", "--all"},
		{"draft", "list", "--all"},
		{"thread", "read", "801"},
		{"search", "invoice"},
		{"draft", "delete", "9001"},
		{"screener", "clear"},
		{"bulk-reply", "preview", "301"},
		{"tui"},
		{"doctor"},
	} {
		_, _, err := runProfileCLI(t, append(args, "--json")...)
		if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(err.Error(), "not offered by the HEY app") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	for _, test := range []struct {
		args []string
		says string
	}{
		{[]string{"mcp"}, "hey mcp speaks to HEY's server"},
		{[]string{"auth", "login"}, "needs no sign-in"},
		{[]string{"auth", "status"}, "needs no sign-in"},
		{[]string{"login"}, "needs no sign-in"},
		{[]string{"box", "view", "imbox", "--account", "7"}, "takes no --account"},
		{[]string{"app", "tools", "--account", "7"}, "takes no --account"},
	} {
		_, _, err := runProfileCLI(t, test.args...)
		if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(err.Error(), test.says) {
			t.Errorf("%v: err = %v", test.args, err)
		}
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the door was called: %s", calls)
	}
}

func TestAppProfileRefusesAnAccountFromTheEnvironment(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)
	t.Setenv("HEY_ACCOUNT_ID", "7")

	_, _, err := runProfileCLI(t, "box", "view", "imbox")
	if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(err.Error(), "HEY_ACCOUNT_ID") {
		t.Fatalf("err = %v", err)
	}
}

// A door that is shut is exit 6 with the way to open it, never HEY's server instead.
func TestAppDoorShutIsANetworkFailureWithTheWayToOpenIt(t *testing.T) {
	appTestHome(t, filepath.Join(t.TempDir(), "agent.sock"), true)

	stdout, _, err := runProfileCLI(t, "box", "view", "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitNetwork || !strings.Contains(apiErrorHint(err), "Let Agents Use HEY") {
		t.Fatalf("err = %v, stdout %s", err, stdout)
	}
	if meta := withProfileMeta(err); !strings.Contains(fmt.Sprint(apiErrorMeta(meta)), "endpoint:app") {
		t.Errorf("error meta = %v", apiErrorMeta(meta))
	}
}

func TestAppSignedOutIsAnAuthFailure(t *testing.T) {
	door := localapptest.Start(t)
	door.Refuse("imbox.list", map[string]any{"error": "signed_out"})
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "box", "view", "imbox")
	if output.ExitCodeFor(err) != output.ExitAuth || !strings.Contains(apiErrorHint(err), "Sign in to HEY in the app") {
		t.Fatalf("err = %v", err)
	}
}

// An answer missing what the listing must have is refused, not shown as empty or complete.
func TestAppAnswerMissingWhatItMustHaveIsRefused(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("imbox.list", map[string]any{"threads": []any{}})
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "box", "view", "imbox", "--json")
	if output.ExitCodeFor(err) != output.ExitAPI || !strings.Contains(err.Error(), "has_more") && !strings.Contains(err.Error(), "holds more") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppScreenerListsWhoWaitsAsSenders(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "screener", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	response := appEnvelope(t, stdout)
	rows := appData[[]map[string]any](t, response)
	if len(rows) != 1 || rows[0]["id"] != float64(51) || rows[0]["email_address"] != "priya@example.com" || rows[0]["name"] != "Priya Raman" {
		t.Errorf("rows = %v", rows)
	}
	if response.Meta["total_count"] != float64(3) || response.Meta["id_kind"] != "sender" || response.Meta["endpoint"] != "app" {
		t.Errorf("meta = %v", response.Meta)
	}
	if !strings.Contains(response.Notice, "2 more senders waiting") {
		t.Errorf("notice = %q", response.Notice)
	}
	count, _, err := runProfileCLI(t, "screener", "list", "--count")
	if err != nil || strings.TrimSpace(count) != "3" {
		t.Errorf("--count = %q, %v", count, err)
	}
}

func TestAppScreenerHistoryListsWhoWasDecided(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "screener", "history", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := appData[[]map[string]any](t, appEnvelope(t, stdout))
	if len(rows) != 2 || rows[0]["id"] != float64(52) || rows[0]["status"] != "denied" || rows[1]["status"] != "approved" {
		t.Errorf("rows = %v", rows)
	}
}

func TestAppDraftListKeepsTheServersFieldNames(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "draft", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := appData[[]map[string]any](t, appEnvelope(t, stdout))
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	draft := rows[0]
	if draft["id"] != float64(9001) || draft["updated_at"] != "2026-11-11T09:30:00Z" || draft["subject"] != "Re: Stint plan for Saturday" ||
		draft["sendable"] != true || draft["made_as"] != float64(1731280000123) || draft["kind"] != "reply" {
		t.Errorf("draft = %v", draft)
	}
	if _, ok := draft["saved_at"]; ok {
		t.Errorf("the door's saved_at was kept beside updated_at: %v", draft)
	}
}

// A server profile in an experimental build says it is the server.
func TestServerProfileUnderTheGateNamesTheServer(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	response, _, err := runProfileJSON(t, "-P", "hey.com", "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	if response.Meta["endpoint"] != "server" || response.Meta["profile"] != "hey.com" {
		t.Errorf("meta = %v", response.Meta)
	}
	endpoints := map[string]any{}
	for _, row := range appData[[]map[string]any](t, response) {
		endpoints[fmt.Sprint(row["name"])] = row["endpoint"]
	}
	if endpoints["this-computer"] != "app" || endpoints["hey.com"] != "server" {
		t.Errorf("endpoints = %v", endpoints)
	}
}

// hey app runs a read tool, and refuses one that changes something before calling it.
func TestAppCallRunsReadsAlone(t *testing.T) {
	door := appImboxDoor(t)
	door.Offer("imbox.list", "Lists the threads in the Imbox", "read")
	door.Offer("screener.decide", "Screens a sender in or out", "reversible", "sender", "decision")
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "app", "call", "imbox.list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if response := appEnvelope(t, stdout); response.Meta["endpoint"] != "app" {
		t.Errorf("meta = %v", response.Meta)
	}
	_, _, err = runProfileCLI(t, "app", "call", "screener.decide", "--arg", "sender=51", "--arg", "decision=out")
	if output.ExitCodeFor(err) != output.ExitUsage || !strings.Contains(err.Error(), "runs only what reads") {
		t.Errorf("err = %v", err)
	}
	if calls := door.Calls(); len(calls) != 1 {
		t.Errorf("calls = %s", calls)
	}
}

func apiErrorHint(err error) string { return apierr.AsError(err).Hint }

func apiErrorMeta(err error) map[string]any { return apierr.AsError(err).Meta }

// Completion and profile listing on an app profile read no server and no credential.
func TestAppProfileKeepsCompletionAndReadsNoCredential(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	if _, _, err := runProfileCLI(t, "__complete", "box", "view", ""); err != nil {
		t.Errorf("completion: %v", err)
	}
	// A credential file nothing can read: any look at it for the app profile fails the run.
	home := os.Getenv("XDG_CONFIG_HOME")
	if err := os.WriteFile(filepath.Join(home, "hey-cli", "credentials.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	response, _, err := runProfileJSON(t, "profile", "show")
	if err != nil {
		t.Fatal(err)
	}
	if shown := appData[map[string]any](t, response); shown["endpoint"] != "app" || shown["authenticated"] != nil {
		t.Errorf("profile show = %v", shown)
	}
	response, _, err = runProfileJSON(t, "-P", "this-computer", "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range appData[[]map[string]any](t, response) {
		if row["name"] == "this-computer" && row["authenticated"] != nil {
			t.Errorf("profile list = %v", row)
		}
	}
}

// The Imbox rows carry only what the app has, none of the server's empty fields.
func TestAppImboxRowsCarryNoEmptyServerFields(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)

	stdout, _, err := runProfileCLI(t, "box", "view", "imbox", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, `"note"`) || strings.Contains(stdout, `"bubble_up_schedule"`) || strings.Contains(stdout, "thread read") {
		t.Errorf("stdout = %s", stdout)
	}
}

// The app quitting while it answers is a shut door (6), not a bad answer (7).
func TestAppClosingMidAnswerIsANetworkFailure(t *testing.T) {
	path, listener := localapptest.Listen(t)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		lines := bufio.NewScanner(conn)
		for lines.Scan() {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.Unmarshal(lines.Bytes(), &request)
			if request.Method == "tools/call" {
				return
			}
			if request.Method == "initialize" {
				fmt.Fprintf(conn, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"hey"}}}`+"\n", request.ID)
			}
		}
	}()
	appTestHome(t, path, true)

	_, _, err := runProfileCLI(t, "box", "view", "imbox")
	if output.ExitCodeFor(err) != output.ExitNetwork || !strings.Contains(apiErrorHint(err), "Let Agents Use HEY") {
		t.Fatalf("err = %v", err)
	}
}

// An app that offers nothing is signed out: hey app call says so (3).
func TestAppCallOnASignedOutAppIsAnAuthFailure(t *testing.T) {
	door := localapptest.Start(t)
	appTestHome(t, door.Path, true)

	_, _, err := runProfileCLI(t, "app", "call", "imbox.list")
	if output.ExitCodeFor(err) != output.ExitAuth {
		t.Fatalf("err = %v", err)
	}
}

// Answers whose parts contradict each other, or leave a list out, are refused or kept a list.
func TestAppAnswersAreReadWhole(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.list", map[string]any{
		"waiting":       []map[string]any{{"id": 51, "name": "Priya Raman", "address": "priya@example.com"}, {"id": 54, "name": "Omar Haddad", "address": "omar@example.com"}},
		"waiting_count": 1,
		"decided":       []map[string]any{},
	})
	door.Answer("drafts.list", map[string]any{"drafts": []map[string]any{
		{"id": 9002, "kind": "new", "account": "jason@example.com", "from": "jason@example.com", "to": []string{"marcus@example.com"},
			"subject": "Lunch", "summary": "Thursday?", "saved_at": "2026-11-11T10:00:00Z", "sendable": true},
	}})
	door.Answer("imbox.list", map[string]any{"threads": []map[string]any{
		{"id": 303, "topic": 803, "subject": "Notes for Saturday", "summary": "Pack the tire warmers", "sender": "", "sender_id": 0, "seen": true, "bubbled_up": false},
	}, "has_more": false})
	appTestHome(t, door.Path, true)

	if _, _, err := runProfileCLI(t, "screener", "list", "--json"); output.ExitCodeFor(err) != output.ExitAPI {
		t.Errorf("a count below the list: %v", err)
	}
	stdout, _, err := runProfileCLI(t, "draft", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"cc": []`) || !strings.Contains(stdout, `"bcc": []`) {
		t.Errorf("draft list = %s", stdout)
	}
	stdout, _, err = runProfileCLI(t, "box", "view", "imbox", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, `"id": 0`) {
		t.Errorf("a sender with no id reads as sender 0: %s", stdout)
	}
}

// A repository's app-only entry is a profile too, so it holds up a delete that would
// otherwise leave it to bring the profile back.
func TestARepositoryAppOnlyEntryHoldsUpADelete(t *testing.T) {
	home := profileTestHome(t)
	writeProfileTestConfig(t, home, `{"profiles":{"work":{"base_url":"https://app.hey.com"}}}`)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".hey", "config.json"), []byte(`{"profiles":{"work":{"endpoint":"app"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	if _, _, err := runProfileJSON(t, "config", "trust-local"); err != nil {
		t.Fatal(err)
	}
	_, _, err := runProfileJSON(t, "profile", "delete", "work")
	if got := errorMessage(err); !strings.Contains(got, `Profile "work" is also set by `) {
		t.Errorf("delete = %q", got)
	}
}

// A limited app listing says how to see the rest by what the app takes: --all is refused
// there, so the hint names --limit instead. The Imbox keeps the app's own warning that it
// has not loaded everything beside it.
func TestAppLimitedListingsOfferOnlyWhatTheAppTakes(t *testing.T) {
	door := appImboxDoor(t)
	door.Answer("drafts.list", map[string]any{
		"drafts": []map[string]any{
			{"id": 9001, "made_as": 1731280000123, "kind": "new", "account": "jason@example.com", "from": "jason@example.com",
				"to": []string{}, "cc": []string{}, "bcc": []string{}, "subject": "One", "summary": "", "saved_at": "2026-11-11T09:30:00Z", "sendable": false},
			{"id": 9002, "made_as": 1731280000124, "kind": "new", "account": "jason@example.com", "from": "jason@example.com",
				"to": []string{}, "cc": []string{}, "bcc": []string{}, "subject": "Two", "summary": "", "saved_at": "2026-11-11T09:31:00Z", "sendable": false},
		},
	})
	appTestHome(t, door.Path, true)

	for _, args := range [][]string{{"box", "view", "imbox"}, {"draft", "list"}} {
		stdout, _, err := runProfileCLI(t, append(args, "--limit", "1", "--json")...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		notice := appEnvelope(t, stdout).Notice
		if strings.Contains(notice, "--all") || !strings.Contains(notice, "Showing 1 of 2") || !strings.Contains(notice, "without --limit") {
			t.Errorf("%v notice = %q", args, notice)
		}
		if args[0] == "box" && !strings.Contains(notice, "more threads than the HEY app has loaded") {
			t.Errorf("the Imbox's own warning went: %q", notice)
		}
	}
}

// Deleting the app's profile looks for no credential and revokes nothing: it has none of
// its own, as profile list and show already hold.
func TestAppProfileDeleteReadsNoCredential(t *testing.T) {
	door := appImboxDoor(t)
	appTestHome(t, door.Path, true)
	// A credential file nothing can read: any look at it for the app profile fails the delete.
	home := os.Getenv("XDG_CONFIG_HOME")
	if err := os.WriteFile(filepath.Join(home, "hey-cli", "credentials.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	response, _, err := runProfileJSON(t, "profile", "delete", "this-computer")
	if err != nil {
		t.Fatal(err)
	}
	if deleted := appData[map[string]any](t, response); deleted["status"] != "deleted" || deleted["revoked"] != nil {
		t.Errorf("delete = %v", deleted)
	}
	if calls := door.Calls(); len(calls) != 0 {
		t.Errorf("the delete asked the app: %s", calls)
	}
}
