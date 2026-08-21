package cmd

import (
	"errors"
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

func TestOmarchyRemoveKeepsPollStateWhileBarRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	env, _ := testOmarchyEnv(t)
	writeShell(t, env, pluginShellJSON)
	on := true
	omarchySetup{env: env, notify: &on}.apply()
	if err := saveOmarchyPollState(omarchyPollState{Identity: "test", Seen: map[string]int32{"1": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(env.configDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(env.configDir(), 0o700) })

	steps := statuses(omarchySetup{env: env}.remove())
	if steps["bar plugin"] != "failed" || steps["poll state"] != "kept" {
		t.Errorf("while the notify setting cannot be cleared the fingerprints must stay, got %v", steps)
	}
	if _, existed := loadOmarchyPollState(); !existed {
		t.Error("poll state was deleted although the plugin still polls with --notify")
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

// unseenPagesServer serves `pages` all-unseen Imbox pages (one posting each,

func TestOmarchySetupNotifyFailsWhenStaleStateCannotBeDropped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove anything")
	}
	env, _ := testOmarchyEnv(t)
	writeShell(t, env, pluginShellJSON)
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
	if steps["bar plugin"] != "failed" {
		t.Errorf("enabling toasts over undroppable stale fingerprints must fail, got %q", steps["bar plugin"])
	}
	if _, has := pluginEntry(t, env)["notify"]; has {
		t.Error("notify must not be switched on when the reseed could not be prepared")
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
	writeShell(t, env, pluginShellJSON)
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
		t.Error("turning toasts back on must drop stale fingerprints so the first poll reseeds")
	}
}
