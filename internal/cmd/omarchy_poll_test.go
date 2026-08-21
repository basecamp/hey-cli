package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

// imboxServer answers the identity endpoint and a one-page Imbox.
func imboxServer(t *testing.T, postings string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/identity.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 7, "name": "Maria Delgado"}`))
		case r.Method == "GET" && r.URL.Path == "/imbox.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": ` + postings + `}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

// unseenPagesServer serves `pages` all-unseen Imbox pages (one posting each,
// ids 1..pages) followed by a page whose first posting is seen, and counts
// the page fetches. A page listed in failing answers 500 instead.
func unseenPagesServer(t *testing.T, pages int, failing ...int) (*httptest.Server, *int) {
	t.Helper()
	fetched := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/identity.json" {
			_, _ = w.Write([]byte(`{"id": 7, "name": "Maria Delgado"}`))
			return
		}
		if r.URL.Path != "/imbox.json" {
			w.WriteHeader(404)
			return
		}
		fetched++
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		if slices.Contains(failing, page) {
			w.WriteHeader(500)
			return
		}
		if page > pages {
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": [{"id": 999, "name": "Already read", "seen": true}]}`))
			return
		}
		fmt.Fprintf(w, `{"id": 1, "name": "Imbox", "kind": "inbox", "next_history_url": %q,
		  "postings": [{"id": %d, "name": "Thread %d", "seen": false, "visible_entry_count": 1}]}`,
			fmt.Sprintf("%s/imbox.json?page=%d", server.URL, page+1), page, page)
	}))
	t.Cleanup(server.Close)
	return server, &fetched
}

// testPollEnv swaps the poll's Omarchy environment for a recorder, so a toast
// is observed rather than sent to a notification daemon.
func testPollEnv(t *testing.T) *[][]string {
	t.Helper()
	env, calls := testNotifyEnv("42\n")
	previous := omarchyPollEnv
	omarchyPollEnv = func() omarchyEnv { return env }
	t.Cleanup(func() { omarchyPollEnv = previous })
	return calls
}

type pollResponse struct {
	OK     bool   `json:"ok"`
	Notice string `json:"notice"`
	Data   struct {
		Name           string `json:"name"`
		NextHistoryURL string `json:"next_history_url"`
		Postings       []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Seen bool   `json:"seen"`
		} `json:"postings"`
	} `json:"data"`
	raw json.RawMessage
}

// runHey runs the root command in a sandboxed config, against the given
// server and state directory, and returns what it printed.
func runHey(t *testing.T, stateHome, serverURL string, authenticated bool, args ...string) (string, error) {
	t.Helper()
	if authenticated {
		t.Setenv("HEY_TOKEN", "test-token")
	} else {
		t.Setenv("HEY_TOKEN", "")
	}
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(args, "--base-url", serverURL, "--json"))
	err := root.Execute()
	return buf.String(), err
}

// runPoll runs hey omarchy poll against a state directory, so a test can poll
// more than once over the same fingerprints.
func runPoll(t *testing.T, stateHome, serverURL string, authenticated bool, extraArgs ...string) (pollResponse, error) {
	t.Helper()
	out, err := runHey(t, stateHome, serverURL, authenticated, append([]string{"omarchy", "poll"}, extraArgs...)...)
	var resp pollResponse
	if err == nil {
		if uerr := json.Unmarshal([]byte(out), &resp); uerr != nil {
			t.Fatalf("poll output is not JSON: %q", out)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal([]byte(out), &envelope)
		resp.raw = envelope.Data
	}
	return resp, err
}

func TestPollAnswersLikeBoxImbox(t *testing.T) {
	server := imboxServer(t, `[{"id": 1, "name": "Lunch on Thursday?", "seen": true, "creator": {"name": "Maria Delgado"}},
	  {"id": 2, "name": "Invoice #4021", "seen": false, "account_id": 5, "summary": "Your August invoice is attached."}]`)
	defer server.Close()

	poll, err := runPoll(t, t.TempDir(), server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	box, err := runHey(t, t.TempDir(), server.URL, true, "box", "imbox")
	if err != nil {
		t.Fatal(err)
	}
	var boxEnvelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(box), &boxEnvelope); err != nil {
		t.Fatal(err)
	}
	// The plugin parses hey box imbox --json; the poll has to be the same shape,
	// byte for byte, so the panel renders either.
	if !bytes.Equal(poll.raw, boxEnvelope.Data) {
		t.Errorf("poll data differs from hey box imbox:\n%s\n%s", poll.raw, boxEnvelope.Data)
	}
	if !poll.OK || len(poll.Data.Postings) != 2 || poll.Data.Postings[1].ID != 2 {
		t.Errorf("unexpected poll response: %+v", poll)
	}
}

func TestPollLimitTruncatesAndClearsTheNextPage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id": 1, "name": "Imbox", "kind": "inbox", "next_history_url": %q,
		  "postings": [{"id": 1, "name": "Newest", "seen": false}, {"id": 2, "name": "Newer", "seen": false}, {"id": 3, "name": "New", "seen": true}]}`,
			server.URL+"/imbox.json?page=2")
	}))
	defer server.Close()

	resp, err := runPoll(t, t.TempDir(), server.URL, true, "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Postings) != 2 || resp.Data.Postings[1].ID != 2 {
		t.Errorf("--limit 2 should keep the first two postings, got %+v", resp.Data.Postings)
	}
	if resp.Data.NextHistoryURL != "" {
		t.Errorf("a client-side cut must clear next_history_url so the cut postings are not skipped, got %q", resp.Data.NextHistoryURL)
	}
	if !strings.Contains(resp.Notice, "raise --limit") {
		t.Errorf("the truncation should be noticed, got %q", resp.Notice)
	}

	if _, err := runPoll(t, t.TempDir(), server.URL, true, "--limit", "0"); err == nil || output.AsError(err).Code != "usage" {
		t.Errorf("--limit 0 is a usage error, got %v", err)
	}
}

func TestPollPagesForTheLimitPastTheFirstSeenPosting(t *testing.T) {
	var server *httptest.Server
	fetched := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/identity.json" {
			_, _ = w.Write([]byte(`{"id": 7, "name": "Maria Delgado"}`))
			return
		}
		fetched++
		switch r.URL.Query().Get("page") {
		case "":
			fmt.Fprintf(w, `{"id": 1, "name": "Imbox", "kind": "inbox", "next_history_url": %q,
			  "postings": [{"id": 1, "name": "Newest", "seen": false, "visible_entry_count": 1}, {"id": 2, "name": "Read already", "seen": true}]}`,
				server.URL+"/imbox.json?page=2")
		default:
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": [{"id": 3, "name": "Older", "seen": true}]}`))
		}
	}))
	defer server.Close()
	testPollEnv(t)

	// The unseen set closes on page 1, but the panel asked for three threads.
	resp, err := runPoll(t, t.TempDir(), server.URL, true, "--limit", "3", "--notify")
	if err != nil {
		t.Fatal(err)
	}
	if fetched != 2 || len(resp.Data.Postings) != 3 {
		t.Errorf("the poll should page on for --limit, fetched %d pages and %d postings", fetched, len(resp.Data.Postings))
	}
	state, existed := loadOmarchyPollState()
	if !existed || len(state.Seen) != 1 || state.Seen["1"] != 1 {
		t.Errorf("only the unseen posting is fingerprinted, got existed=%v %+v", existed, state)
	}
}

func TestPollWithoutNotifyReadsOnlyWhatTheLimitNeeds(t *testing.T) {
	server, fetched := unseenPagesServer(t, 3)

	resp, err := runPoll(t, t.TempDir(), server.URL, true, "--limit", "1")
	if err != nil || !resp.OK {
		t.Fatalf("got %+v, %v", resp, err)
	}
	if *fetched != 1 {
		t.Errorf("one thread needs one page, fetched %d", *fetched)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("without --notify the fingerprints are never touched")
	}
}

func TestPollNotifySeedsSilentlyThenToastsOnce(t *testing.T) {
	postings := `[{"id": 5, "name": "Invoice #4021", "seen": false, "visible_entry_count": 2, "creator": {"name": "Northwind Invoicing"}}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/identity.json" {
			_, _ = w.Write([]byte(`{"id": 7, "name": "Maria Delgado"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": ` + postings + `}`))
	}))
	defer server.Close()
	calls := testPollEnv(t)
	stateHome := t.TempDir()

	resp, err := runPoll(t, stateHome, server.URL, true, "--notify")
	if err != nil || len(resp.Data.Postings) != 1 {
		t.Fatalf("the response is unchanged by --notify, got %+v, %v", resp, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("the first poll seeds and never toasts the backlog, ran %v", *calls)
	}
	state, existed := loadOmarchyPollState()
	if !existed || state.Seen["5"] != 2 || state.Identity != pollIdentity(server.URL, "all", "7") {
		t.Errorf("the first poll should seed the fingerprints under the poll's identity, got %+v (existed=%v)", state, existed)
	}

	postings = `[{"id": 6, "name": "Lunch on Thursday?", "seen": false, "visible_entry_count": 1, "creator": {"name": "Maria Delgado"}},` + postings[1:]
	if _, err := runPoll(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("a new thread toasts exactly once, ran %v", *calls)
	}
	argv := (*calls)[0]
	if argv[0] != "omarchy-notification-send" || !slices.Contains(argv, "Maria Delgado — Lunch on Thursday?") {
		t.Errorf("unexpected toast: %v", argv)
	}
	for flag, value := range map[string]string{"--app-name": "HEY", "--exec": omarchyFocusCommand, "-u": "low"} {
		if i := slices.Index(argv, flag); i < 0 || argv[i+1] != value {
			t.Errorf("%s %q missing from %v", flag, value, argv)
		}
	}

	// Same Imbox again: nothing new, nothing sent, and the toast id is kept so
	// the next toast replaces this one.
	if _, err := runPoll(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Errorf("an unchanged Imbox must not toast again, ran %v", *calls)
	}
	if state, _ := loadOmarchyPollState(); state.ToastID != 42 {
		t.Errorf("the toast id should be cached, got %+v", state)
	}
}

func TestPollNotifyReseedsWhenTheIdentityChanges(t *testing.T) {
	server := imboxServer(t, `[{"id": 5, "name": "Invoice #4021", "seen": false, "visible_entry_count": 1}]`)
	defer server.Close()
	calls := testPollEnv(t)
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	// Fingerprints taken for someone else: the backlog must not toast.
	if err := saveOmarchyPollState(omarchyPollState{Identity: "https://app.hey.com all user:1002", Seen: map[string]int32{}}); err != nil {
		t.Fatal(err)
	}

	if _, err := runPoll(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("another identity's backlog must reseed silently, ran %v", *calls)
	}
	if state, _ := loadOmarchyPollState(); state.Identity != pollIdentity(server.URL, "all", "7") || state.Seen["5"] != 1 {
		t.Errorf("the state should now fingerprint the poll's identity, got %+v", state)
	}
}

func TestPollNotifySkipsWhenTheIdentityIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/imbox.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": [{"id": 5, "name": "Invoice #4021", "seen": false}]}`))
			return
		}
		w.WriteHeader(500) // the identity endpoint is down
	}))
	defer server.Close()
	testPollEnv(t)

	resp, err := runPoll(t, t.TempDir(), server.URL, true, "--notify")
	if err != nil || len(resp.Data.Postings) != 1 {
		t.Errorf("the panel still gets its postings, got %+v, %v", resp, err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("without knowing who the poll runs as, the fingerprints must stay untouched")
	}
}

func TestPollNotifySeedsEveryUnseenPageThenCapsSteadyPolls(t *testing.T) {
	server, fetched := unseenPagesServer(t, 3)
	testPollEnv(t)
	old := unseenPageCap
	unseenPageCap = 2
	t.Cleanup(func() { unseenPageCap = old })

	// First seed: every unseen page is read, cap or no cap, and the closing
	// seen page proves the set complete.
	stateHome := t.TempDir()
	if _, err := runPoll(t, stateHome, server.URL, true, "--limit", "1", "--notify"); err != nil {
		t.Fatal(err)
	}
	state, existed := loadOmarchyPollState()
	if !existed || len(state.Seen) != 3 {
		t.Errorf("the seed must fingerprint every unseen thread, got %+v", state)
	}
	if *fetched != 4 {
		t.Errorf("the seed should read all three unseen pages and the closing one, fetched %d", *fetched)
	}

	// Steady poll: the cap applies and the snapshot is incomplete, so the
	// thread beyond the cap keeps its fingerprint rather than being pruned.
	*fetched = 0
	resp, err := runPoll(t, stateHome, server.URL, true, "--limit", "1", "--notify")
	if err != nil {
		t.Fatal(err)
	}
	if *fetched != 2 {
		t.Errorf("a steady poll stops at the cap, fetched %d", *fetched)
	}
	if state, _ = loadOmarchyPollState(); len(state.Seen) != 3 {
		t.Errorf("an incomplete snapshot must keep the fingerprints it could not see, got %+v", state)
	}
	// The pages read for the toasts are the panel's too, cut to its limit —
	// and a cut clears the next page, as in hey box.
	if len(resp.Data.Postings) != 1 || resp.Data.NextHistoryURL != "" || !strings.Contains(resp.Notice, "raise --limit") {
		t.Errorf("the panel gets its --limit with the cut noticed, got %+v %q", resp.Data, resp.Notice)
	}
}

func TestPollSeedPageCapCountsTheInitialPage(t *testing.T) {
	// Two all-unseen pages plus the closing seen page: a seed cap of three
	// total pages must reach the closing page and complete.
	server, fetched := unseenPagesServer(t, 2)
	testPollEnv(t)
	old := unseenSeedPageCap
	unseenSeedPageCap = 3
	t.Cleanup(func() { unseenSeedPageCap = old })

	if _, err := runPoll(t, t.TempDir(), server.URL, true, "--limit", "1", "--notify"); err != nil {
		t.Fatal(err)
	}
	if state, existed := loadOmarchyPollState(); !existed || len(state.Seen) != 2 {
		t.Errorf("a seed whose unseen set fits the cap must complete, got existed=%v %+v", existed, state)
	}
	if *fetched != 3 {
		t.Errorf("the closing page is within the cap, fetched %d", *fetched)
	}

	// One page tighter and the seed is incomplete: nothing may be persisted.
	unseenSeedPageCap = 2
	if _, err := runPoll(t, t.TempDir(), server.URL, true, "--limit", "1", "--notify"); err != nil {
		t.Fatal(err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("an over-cap seed is incomplete and must not be persisted")
	}
}

func TestPollFailsClosedWhenALaterPageFails(t *testing.T) {
	server, fetched := unseenPagesServer(t, 3, 3)
	calls := testPollEnv(t)
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	// A thread fingerprinted earlier that sits beyond the failed page.
	if err := saveOmarchyPollState(omarchyPollState{Identity: pollIdentity(server.URL, "all", "7"), Seen: map[string]int32{"3": 1}}); err != nil {
		t.Fatal(err)
	}

	// The panel must not swap its last complete list for a short one: the
	// poll reports the failed page. The toasts still get the pages that were
	// read — the two new threads on them toast, and the fingerprint beyond the
	// failed page is kept rather than pruned.
	_, err := runPoll(t, stateHome, server.URL, true, "--limit", "5", "--notify")
	if err == nil {
		t.Fatal("a failed later page must fail the poll")
	}
	if *fetched != 3 {
		t.Errorf("fetched %d pages, want the failing third", *fetched)
	}
	if len(*calls) != 1 || !slices.Contains((*calls)[0], "2 new in Imbox") {
		t.Errorf("the threads on the pages that were read toast once, ran %v", *calls)
	}
	if state, _ := loadOmarchyPollState(); state.Seen["3"] != 1 || state.Seen["1"] != 1 || state.Seen["2"] != 1 {
		t.Errorf("an incomplete snapshot keeps the fingerprints it could not see and adds what it saw, got %+v", state)
	}
}

func TestPollWithoutNotifyForgetsTheFingerprints(t *testing.T) {
	server := imboxServer(t, `[{"id": 5, "name": "Invoice #4021", "seen": false, "visible_entry_count": 1}]`)
	defer server.Close()
	calls := testPollEnv(t)
	stateHome := t.TempDir()

	if _, err := runPoll(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if _, existed := loadOmarchyPollState(); !existed {
		t.Fatal("--notify should have seeded")
	}
	// Toasts turned off by any route — the plugin's toggle, omarchy bar set —
	// mean polls without --notify, which disarm the seed…
	if _, err := runPoll(t, stateHome, server.URL, true); err != nil {
		t.Fatal(err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("a poll without --notify must forget the fingerprints")
	}
	// …so turning them back on starts from a silent seed, never the backlog.
	if _, err := runPoll(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("re-enabling must reseed silently, ran %v", *calls)
	}
}

func TestPollReportsErrorsTheBarOnceSwallowed(t *testing.T) {
	server := imboxServer(t, `[]`)
	calls := testPollEnv(t)

	_, err := runPoll(t, t.TempDir(), server.URL, false, "--notify")
	if err == nil || output.AsError(err).Code != "auth" {
		t.Errorf("logged out should answer with the auth envelope the plugin branches on, got %v", err)
	}
	if _, existed := loadOmarchyPollState(); existed || len(*calls) != 0 {
		t.Error("logged out must neither toast nor touch state")
	}

	server.Close()
	_, err = runPoll(t, t.TempDir(), server.URL, true, "--notify")
	if err == nil || output.AsError(err).Code == "auth" {
		t.Errorf("offline should answer with a non-auth error, got %v", err)
	}
	if _, existed := loadOmarchyPollState(); existed || len(*calls) != 0 {
		t.Error("offline must neither toast nor touch state")
	}
}

func TestPollReportsAMalformedGlobalConfig(t *testing.T) {
	server := imboxServer(t, `[{"id": 1, "name": "Invoice #4021", "seen": false}]`)
	defer server.Close()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(configHome, "hey-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "hey-cli", "config.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"omarchy", "poll", "--base-url", server.URL, "--json"})
	err := root.Execute()
	if err == nil || output.AsError(err).Code != "config_error" {
		t.Fatalf("with no trustworthy configuration the poll must say so rather than answer for a guessed server, got %v", err)
	}

	root = newRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"boxes"})
	if err := root.Execute(); err == nil {
		t.Error("ordinary commands still report a broken global config")
	}
}
