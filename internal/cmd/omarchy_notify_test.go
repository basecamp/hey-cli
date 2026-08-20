package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// testNotifyEnv records every runOutput invocation and answers with the given
// stdout, the way omarchy-notification-send -p prints the toast id.
func testNotifyEnv(stdout string) (omarchyEnv, *[][]string) {
	var calls [][]string
	env := omarchyEnv{
		runOutput: func(name string, args ...string) (string, error) {
			calls = append(calls, append([]string{name}, args...))
			return stdout, nil
		},
	}
	return env, &calls
}

func unseenPosting(id int64, sender, name string, entries int32) generated.Posting {
	return generated.Posting{Id: id, Name: name, VisibleEntryCount: entries,
		Creator: generated.Contact{Name: sender}}
}

// notifyAll runs a tick with a fixed identity and a complete unseen snapshot —
// the common case the older tests exercise.
func notifyAll(env omarchyEnv, unseen []generated.Posting) {
	notifyNewMail(env, "test", unseen, true)
}

func TestNotifyNewMailSeedsStateWithoutToasting(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	notifyAll(env, []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 3),
	})

	if len(*calls) != 0 {
		t.Errorf("first run must never toast the backlog, ran %v", *calls)
	}
	state, existed := loadOmarchyPollState()
	if !existed || state.Seen["101"] != 1 || state.Seen["102"] != 3 {
		t.Errorf("first run should seed the fingerprints, got %+v (existed=%v)", state, existed)
	}
}

func TestNotifyNewMailToastsOneNewThread(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("42\n")

	known := unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)
	notifyAll(env, []generated.Posting{known})
	fresh := unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1)
	fresh.Summary = "Your August invoice is attached."
	notifyAll(env, []generated.Posting{known, fresh})

	if len(*calls) != 1 {
		t.Fatalf("want exactly one toast, ran %v", *calls)
	}
	argv := (*calls)[0]
	if argv[0] != "omarchy-notification-send" {
		t.Errorf("wrong command: %v", argv)
	}
	for flag, value := range map[string]string{
		"--glyph": omarchyBarGlyph, "--app-name": "HEY", "-u": "low", "--exec": omarchyFocusCommand,
	} {
		i := slices.Index(argv, flag)
		if i < 0 || argv[i+1] != value {
			t.Errorf("%s %q missing from %v", flag, value, argv)
		}
	}
	if !slices.Contains(argv, "Northwind Invoicing — Invoice #4021") ||
		!slices.Contains(argv, "Your August invoice is attached.") {
		t.Errorf("headline/description missing from %v", argv)
	}
	if slices.Contains(argv, "-r") {
		t.Errorf("no cached toast id yet, must not pass -r: %v", argv)
	}
	if argv[len(argv)-1] != "-p" {
		t.Errorf("-p must be passed to learn the toast id: %v", argv)
	}
	if state, _ := loadOmarchyPollState(); state.ToastID != 42 {
		t.Errorf("printed toast id should be cached, got %+v", state)
	}
}

func TestNotifyNewMailReplacesThePreviousToast(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("42\n")

	notifyAll(env, nil)
	notifyAll(env, []generated.Posting{unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)})
	notifyAll(env, []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1),
	})

	if len(*calls) != 2 {
		t.Fatalf("want two toasts, ran %v", *calls)
	}
	argv := (*calls)[1]
	i := slices.Index(argv, "-r")
	if i < 0 || argv[i+1] != "42" {
		t.Errorf("second toast should replace the first via -r 42: %v", argv)
	}
}

func TestNotifyNewMailDoesNotReuseAStaleToastID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	// A toast id from before a reboot may now belong to another application.
	if err := saveOmarchyPollState(omarchyPollState{Identity: "test", Seen: map[string]int32{},
		ToastID: 42, ToastAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	notifyAll(env, []generated.Posting{unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)})

	if len(*calls) != 1 || slices.Contains((*calls)[0], "-r") {
		t.Errorf("a stale toast id must not be passed as -r, ran %v", *calls)
	}
	if state, _ := loadOmarchyPollState(); state.ToastID != 7 || state.ToastAt == 0 {
		t.Errorf("the fresh toast id and time should be cached, got %+v", state)
	}
}

func TestNotifyNewMailKeepsMailTextOutOfOptionParsing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	notifyAll(env, nil)
	fresh := unseenPosting(101, "-r Systems Ltd", "--help with the quarterly numbers", 1)
	fresh.Summary = "-p please see attached"
	notifyAll(env, []generated.Posting{fresh})

	if len(*calls) != 1 {
		t.Fatalf("want one toast, ran %v", *calls)
	}
	argv := (*calls)[0]
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "-") && !slices.Contains([]string{"--glyph", "--app-name", "-u", "--exec", "-r", "-p"}, arg) {
			t.Errorf("mail-derived text must never arrive as an option-looking argument: %q in %v", arg, argv)
		}
	}
	if !slices.Contains(argv, "\u2060-r Systems Ltd — --help with the quarterly numbers") {
		t.Errorf("the text itself must be preserved behind the word joiner: %v", argv)
	}
}

func TestPollIdentityIsKeyedOnTheUser(t *testing.T) {
	alice := pollIdentity("https://app.hey.com/", "all", "1001")
	bob := pollIdentity("https://app.hey.com", "all", "1002")
	if alice == bob {
		t.Error("a different user on the same server and account is a different identity")
	}
	if alice != pollIdentity("https://app.hey.com", "all", "1001") {
		t.Error("the server spelling must be normalized and the identity stable for one user")
	}
	if pollIdentity("https://app.hey.com", "all", "") != "https://app.hey.com all" {
		t.Error("without a user the identity is just server and account")
	}
}

func TestBarStatusNotifySkipsWhenTheIdentityIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/imbox.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": [{"id": 5, "name": "Invoice #4021", "seen": false}]}`))
			return
		}
		w.WriteHeader(500) // the identity endpoint is down
	}))
	defer server.Close()

	out, err := runBarStatus(t, server.URL, true, "--notify")
	if err != nil || !strings.Contains(out, "active") {
		t.Errorf("the bar must still light, got %q, %v", out, err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("without knowing who the poll runs as, the fingerprints must stay untouched")
	}
}

func TestOmarchyRemoveKeepsPollStateWhileBarRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	env, _ := testOmarchyEnv(t)
	setup := omarchySetup{env: env}
	setup.apply()
	if err := saveOmarchyPollState(omarchyPollState{Identity: "test", Seen: map[string]int32{"1": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(env.configDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(env.configDir(), 0o700) })

	steps := statuses(setup.remove())
	if steps["bar indicator"] != "failed" || steps["poll state"] != "kept" {
		t.Errorf("while the bar module cannot be removed the fingerprints must stay, got %v", steps)
	}
	if _, existed := loadOmarchyPollState(); !existed {
		t.Error("poll state was deleted although the poller is still scheduled")
	}
}

func TestNotifyNewMailToastsWhenAThreadGrows(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	thread := unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)
	notifyAll(env, []generated.Posting{thread})
	notifyAll(env, []generated.Posting{thread})
	if len(*calls) != 0 {
		t.Fatalf("an unchanged thread must not toast, ran %v", *calls)
	}

	thread.VisibleEntryCount = 2
	notifyAll(env, []generated.Posting{thread})
	if len(*calls) != 1 || !slices.Contains((*calls)[0], "Maria Delgado — Lunch on Thursday?") {
		t.Errorf("a new reply on a known thread should toast, ran %v", *calls)
	}
}

func TestNotifyNewMailSkipsMutedButRemembersThem(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	notifyAll(env, nil)
	muted := unseenPosting(103, "Weekend Deals", "48 hours only", 1)
	muted.Muted = true
	notifyAll(env, []generated.Posting{muted})

	if len(*calls) != 0 {
		t.Errorf("muted threads must never toast, ran %v", *calls)
	}
	if state, _ := loadOmarchyPollState(); state.Seen["103"] != 1 {
		t.Errorf("muted threads should still be fingerprinted, got %+v", state)
	}
}

func TestNotifyNewMailBatchesIntoOneToast(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	notifyAll(env, nil)
	batch := []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1),
		unseenPosting(103, "Sam Whitfield", "Draft agenda for Monday", 1),
		unseenPosting(104, "Priya Raman", "Photos from the offsite", 1),
	}
	batch[0].AlternativeSenderName = "Maria (personal)"
	notifyAll(env, batch)

	if len(*calls) != 1 {
		t.Fatalf("a batch must collapse to one toast, ran %v", *calls)
	}
	argv := (*calls)[0]
	if !slices.Contains(argv, "4 new in Imbox") {
		t.Errorf("batch headline missing: %v", argv)
	}
	if !slices.Contains(argv, "Maria (personal), Northwind Invoicing, Sam Whitfield, …") {
		t.Errorf("batch description should list the first senders: %v", argv)
	}
}

func TestNotifyNewMailPrunesDepartedThreads(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, _ := testNotifyEnv("7\n")

	notifyAll(env, []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1),
	})
	notifyAll(env, []generated.Posting{unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1)})

	state, _ := loadOmarchyPollState()
	if _, kept := state.Seen["101"]; kept || state.Seen["102"] != 1 {
		t.Errorf("fingerprints should prune to the postings still unseen, got %+v", state)
	}
}

func TestNotifyNewMailDoesNotPersistATruncatedSeed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	// The first tick could not read every unseen page: no seed is written.
	notifyNewMail(env, "test", []generated.Posting{unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)}, false)
	if _, existed := loadOmarchyPollState(); existed {
		t.Fatal("an incomplete seed must not be persisted")
	}

	// The next complete tick seeds silently, including the thread the failed
	// page had hidden — which therefore never reads as new.
	notifyAll(env, []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1),
	})
	if len(*calls) != 0 {
		t.Errorf("the retried seed must still be silent, ran %v", *calls)
	}
	if state, existed := loadOmarchyPollState(); !existed || state.Seen["102"] != 1 {
		t.Errorf("the retried seed should fingerprint everything, got %+v", state)
	}
}

func TestNotifyNewMailKeepsFingerprintsOffATruncatedPage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	notifyAll(env, []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1),
	})

	// An all-unseen page may be cut off; 101 falling off it must not be pruned…
	notifyNewMail(env, "test", []generated.Posting{unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1)}, false)
	state, _ := loadOmarchyPollState()
	if state.Seen["101"] != 1 {
		t.Errorf("a thread off a truncated page must keep its fingerprint, got %+v", state)
	}

	// …so its return to the page is not mistaken for new mail.
	notifyAll(env, []generated.Posting{
		unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1),
		unseenPosting(102, "Northwind Invoicing", "Invoice #4021", 1),
	})
	if len(*calls) != 0 {
		t.Errorf("a re-appearing known thread must not toast, ran %v", *calls)
	}
}

func TestNotifyNewMailRetriesAfterAFailedSend(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var calls [][]string
	failing := omarchyEnv{runOutput: func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		return "", errors.New("no notification daemon")
	}}

	notifyAll(failing, nil)
	fresh := unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)
	notifyAll(failing, []generated.Posting{fresh})
	if len(calls) != 1 {
		t.Fatalf("the failed send should have been attempted once, ran %v", calls)
	}
	if state, _ := loadOmarchyPollState(); state.Seen["101"] != 0 {
		t.Errorf("an undelivered posting must not be fingerprinted, got %+v", state)
	}

	working, sent := testNotifyEnv("42\n")
	notifyAll(working, []generated.Posting{fresh})
	if len(*sent) != 1 {
		t.Errorf("the toast must retry on the next tick, ran %v", *sent)
	}
}

func TestNotifyNewMailReseedsWhenIdentityChanges(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	env, calls := testNotifyEnv("7\n")

	notifyNewMail(env, "https://app.hey.com all", nil, true)
	backlog := unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)
	notifyNewMail(env, "https://app.hey.com 12345", []generated.Posting{backlog}, true)

	if len(*calls) != 0 {
		t.Errorf("another account's backlog must reseed silently, ran %v", *calls)
	}
	state, _ := loadOmarchyPollState()
	if state.Identity != "https://app.hey.com 12345" || state.Seen["101"] != 1 {
		t.Errorf("state should now fingerprint the new identity, got %+v", state)
	}
}

func TestBarStatusNotifySeedsEveryUnseenPage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/identity.json":
			_, _ = w.Write([]byte(`{"id": 7, "name": "Maria Delgado"}`))
		case r.URL.Path == "/imbox.json" && r.URL.Query().Get("page") == "":
			// An all-unseen first page: the unseen set may continue.
			fmt.Fprintf(w, `{"id": 1, "name": "Imbox", "kind": "inbox", "next_history_url": %q,
			  "postings": [{"id": 1, "name": "Newest", "seen": false, "visible_entry_count": 1},
			               {"id": 2, "name": "Newer", "seen": false, "visible_entry_count": 1}]}`,
				server.URL+"/imbox.json?page=2")
		case r.URL.Path == "/imbox.json" && r.URL.Query().Get("page") == "2":
			// Older unseen threads, then the first seen one closes the set.
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "next_history_url": "",
			  "postings": [{"id": 3, "name": "Older but unseen", "seen": false, "visible_entry_count": 4},
			               {"id": 4, "name": "Already read", "seen": true, "visible_entry_count": 1}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	out, err := runBarStatus(t, server.URL, true, "--notify")
	if err != nil || !strings.Contains(out, "active") {
		t.Fatalf("bar JSON unchanged by pagination, got %q, %v", out, err)
	}
	state, existed := loadOmarchyPollState()
	if !existed || state.Seen["1"] != 1 || state.Seen["2"] != 1 || state.Seen["3"] != 4 {
		t.Errorf("the first seed must fingerprint every unseen thread across pages, got %+v", state)
	}
	if _, fingerprinted := state.Seen["4"]; fingerprinted {
		t.Errorf("seen threads are not fingerprinted: %+v", state)
	}
}

// unseenPagesServer serves `pages` all-unseen Imbox pages (one posting each,
// ids 1..pages) followed by a page whose first posting is seen, and counts
// the page fetches.
func unseenPagesServer(t *testing.T, pages int) (*httptest.Server, *int) {
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

func TestBarStatusWithoutNotifyReadsOnlyOnePage(t *testing.T) {
	server, fetched := unseenPagesServer(t, 3)

	out, err := runBarStatus(t, server.URL, true)
	if err != nil || !strings.Contains(out, "active") {
		t.Fatalf("bar should light, got %q, %v", out, err)
	}
	if *fetched != 1 {
		t.Errorf("the indicator needs one page, fetched %d", *fetched)
	}
}

func TestBarStatusNotifySeedsExhaustivelyThenCapsSteadyTicks(t *testing.T) {
	server, fetched := unseenPagesServer(t, 3)
	old := unseenPageCap
	unseenPageCap = 2
	t.Cleanup(func() { unseenPageCap = old })

	// First seed: every unseen page is read, cap or no cap.
	stateHome := t.TempDir()
	if _, err := runBarStatusWithState(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	state, _ := loadOmarchyPollState()
	if len(state.Seen) != 3 {
		t.Errorf("the seed must fingerprint every unseen thread, got %+v", state)
	}
	if *fetched != 4 {
		t.Errorf("the seed should read all three unseen pages and the closing one, fetched %d", *fetched)
	}

	// Steady tick: the cap applies and the snapshot is incomplete, so the
	// thread beyond the cap keeps its fingerprint rather than being pruned.
	*fetched = 0
	if _, err := runBarStatusWithState(t, stateHome, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if *fetched != 2 {
		t.Errorf("a steady tick stops at the cap, fetched %d", *fetched)
	}
	if state, _ = loadOmarchyPollState(); len(state.Seen) != 3 {
		t.Errorf("an incomplete snapshot must keep the fingerprints it could not see, got %+v", state)
	}
}

func TestSeedPageCapCountsTheInitialPage(t *testing.T) {
	// Two all-unseen pages plus the closing seen page: a seed cap of three
	// total pages must reach the closing page and complete.
	server, fetched := unseenPagesServer(t, 2)
	old := unseenSeedPageCap
	unseenSeedPageCap = 3
	t.Cleanup(func() { unseenSeedPageCap = old })

	if _, err := runBarStatus(t, server.URL, true, "--notify"); err != nil {
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
	if _, err := runBarStatus(t, server.URL, true, "--notify"); err != nil {
		t.Fatal(err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("an over-cap seed is incomplete and must not be persisted")
	}
}

func TestOmarchySetupNotifyFailsWhenStaleStateCannotBeDropped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove anything")
	}
	env, _ := testOmarchyEnv(t)
	on := true
	omarchySetup{env: env}.apply()
	if err := saveOmarchyPollState(omarchyPollState{Identity: "test", Seen: map[string]int32{"1": 1}}); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Dir(omarchyPollStatePath())
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })

	steps := statuses(omarchySetup{env: env, notify: &on}.apply())
	if steps["bar indicator"] != "failed" {
		t.Errorf("enabling toasts over undroppable stale fingerprints must fail, got %q", steps["bar indicator"])
	}
	if module := readText(t, env.shellPath()); strings.Contains(module, "--notify") {
		t.Error("the module must not be switched to --notify when the reseed could not be prepared")
	}
}

func TestBarStatusNotifySeedsStateAndPrintsBarJSON(t *testing.T) {
	server := imboxServer(t, `[{"id": 5, "name": "Invoice #4021", "seen": false, "visible_entry_count": 2}]`)
	defer server.Close()

	out, err := runBarStatus(t, server.URL, true, "--notify")
	if err != nil || !strings.Contains(out, "active") {
		t.Errorf("bar JSON must be unchanged by --notify, got %q, %v", out, err)
	}
	state, existed := loadOmarchyPollState()
	if !existed || state.Seen["5"] != 2 {
		t.Errorf("--notify should seed state on first run, got %+v (existed=%v)", state, existed)
	}
}

func TestBarStatusNotifySilentWhenUnauthenticatedOrOffline(t *testing.T) {
	server := imboxServer(t, `[]`)

	out, err := runBarStatus(t, server.URL, false, "--notify")
	if err != nil || out != "" {
		t.Errorf("logged out should stay silent, got %q, %v", out, err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("logged out must not touch state")
	}

	server.Close()
	out, err = runBarStatus(t, server.URL, true, "--notify")
	if err != nil || out != "" {
		t.Errorf("offline should stay silent, got %q, %v", out, err)
	}
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("offline must not touch state")
	}
}

func TestOmarchySetupNotifyTogglesBarExec(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	on, off := true, false

	barExec := func() string {
		var shell map[string]any
		if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
			t.Fatal(err)
		}
		layout := shell["bar"].(map[string]any)["layout"].(map[string]any)
		return barLayoutModule(layout, omarchyBarModuleID)["exec"].(string)
	}

	omarchySetup{env: env}.apply()
	if barExec() != "hey omarchy bar-status" {
		t.Fatalf("default install must not notify, exec = %q", barExec())
	}

	steps := statuses(omarchySetup{env: env, notify: &on}.apply())
	if steps["bar indicator"] != "installed" || barExec() != "hey omarchy bar-status --notify" {
		t.Errorf("--notify should rewrite the exec, got %q / %q", steps["bar indicator"], barExec())
	}

	steps = statuses(omarchySetup{env: env, notify: &on}.apply())
	if steps["bar indicator"] != "unchanged" {
		t.Errorf("--notify twice should be idempotent, got %q", steps["bar indicator"])
	}

	steps = statuses(omarchySetup{env: env}.apply())
	if steps["bar indicator"] != "unchanged" || barExec() != "hey omarchy bar-status --notify" {
		t.Errorf("a plain re-run must leave notifications as they are, got %q / %q", steps["bar indicator"], barExec())
	}

	steps = statuses(omarchySetup{env: env, notify: &off}.apply())
	if steps["bar indicator"] != "installed" || barExec() != "hey omarchy bar-status" {
		t.Errorf("--no-notify should revert the exec, got %q / %q", steps["bar indicator"], barExec())
	}
}

func TestNotifyNewMailSkipsTheToastWhenStateCannotBeSaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	env, calls := testNotifyEnv("7\n")

	notifyAll(env, nil)
	stateDir := filepath.Dir(omarchyPollStatePath())
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })

	notifyAll(env, []generated.Posting{unseenPosting(101, "Maria Delgado", "Lunch on Thursday?", 1)})
	if len(*calls) != 0 {
		t.Errorf("a toast whose fingerprints cannot be saved would repeat every tick; it must be skipped, ran %v", *calls)
	}
}

func TestOmarchySetupNotifyReenableReseedsState(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	on, off := true, false

	omarchySetup{env: env, notify: &on}.apply()
	if err := saveOmarchyPollState(omarchyPollState{Identity: "test", Seen: map[string]int32{"101": 1}}); err != nil {
		t.Fatal(err)
	}

	omarchySetup{env: env, notify: &off}.apply()
	if _, existed := loadOmarchyPollState(); !existed {
		t.Fatal("turning toasts off should not touch the fingerprints")
	}

	omarchySetup{env: env, notify: &on}.apply()
	if _, existed := loadOmarchyPollState(); existed {
		t.Error("turning toasts back on must drop stale fingerprints so the first tick reseeds")
	}
}

func TestOmarchySetupNotifyOnFreshInstall(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	on := true

	omarchySetup{env: env, notify: &on}.apply()

	shell := readText(t, env.shellPath())
	if !strings.Contains(shell, "hey omarchy bar-status --notify") {
		t.Errorf("fresh install with --notify should enable toasts:\n%s", shell)
	}
}

func TestOmarchySetupReconcileKeepsNotifyChoice(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"version":1,"bar":{"layout":{"left":[],"center":[],"right":[
	  {"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --notify","interval":60,
	   "tooltip":"HEY","onClick":"omarchy-launch-or-focus-tui --app-id=org.omarchy.hey hey"},
	  {"id":"omarchy.tray"}]}}}`
	if err := os.WriteFile(env.shellPath(), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	steps := statuses(omarchySetup{env: env}.apply())
	if steps["bar indicator"] != "installed" {
		t.Errorf("a stale module should be rewritten on a plain re-run, got %q", steps["bar indicator"])
	}
	var shell map[string]any
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	layout := shell["bar"].(map[string]any)["layout"].(map[string]any)
	module := barLayoutModule(layout, omarchyBarModuleID)
	if module["onClick"] != omarchyFocusCommand || module["interval"] != float64(180) {
		t.Errorf("click command and interval should be reconciled: %v", module)
	}
	if module["exec"] != "hey omarchy bar-status --notify" {
		t.Errorf("the notify choice must survive a plain re-run: %v", module)
	}
	if right := layout["right"].([]any); len(right) != 2 || barEntryID(right[1]) != "omarchy.tray" {
		t.Errorf("module position and neighbours must be kept: %v", right)
	}

	if again := statuses(omarchySetup{env: env}.apply()); again["bar indicator"] != "unchanged" {
		t.Errorf("reconciled module must be stable, got %q", again["bar indicator"])
	}
}
