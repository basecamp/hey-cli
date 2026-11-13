package smoke_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func harnessJSON(value any) (string, string, int) {
	data, _ := json.Marshal(value)
	response, _ := json.Marshal(Response{OK: true, Data: data})
	return string(response), "", 0
}

func TestHarnessOfflineModeRefusesFeatureSelectors(t *testing.T) {
	process := exec.Command(os.Args[0], "-test.run=^TestDraftLifecycle$")
	process.Env = append(os.Environ(), "HEY_SMOKE_OFFLINE=1", "HEY_SMOKE_BASE_URL=invalid://offline-safety-fixture")
	output, err := process.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "offline mode only permits") {
		t.Fatalf("ordinary feature tests were not explicitly refused by offline mode: %v, %s", err, output)
	}
}

func TestHarnessReplyFixtureUsesCreatedTopicNotFirstRow(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			run := func(args ...string) (string, string, int) {
				if !slices.Equal(args, []string{"box", "view", "imbox", "--all", "--json"}) {
					t.Fatalf("unexpected command: %v", args)
				}
				postings := []smokeTopicPosting{{ID: 77, TopicID: 7701, AppURL: "/topics/7701"}}
				if present {
					postings = append(postings, smokeTopicPosting{ID: 88, TopicID: 8801, AppURL: "/topics/8801"})
				}
				return harnessJSON(map[string]any{"postings": postings})
			}
			posting, err := waitForCreatedTopic(run, 8801, 2, func() {})
			if present && (err != nil || posting.TopicID != 8801) {
				t.Fatalf("wanted the created topic, got %#v, %v", posting, err)
			}
			if !present && (err == nil || posting.ID != 0) {
				t.Fatalf("missing fixture must not fall back: %#v, %v", posting, err)
			}
		})
	}
}

func TestHarnessReplyFixtureWaitsForDelivery(t *testing.T) {
	reads := 0
	run := func(...string) (string, string, int) {
		reads++
		postings := []smokeTopicPosting{}
		if reads == 2 {
			postings = append(postings, smokeTopicPosting{ID: 88, TopicID: 8801})
		}
		return harnessJSON(map[string]any{"postings": postings})
	}
	posting, err := waitForCreatedTopic(run, 8801, 2, func() {})
	if err != nil || posting.ID != 88 || reads != 2 {
		t.Fatalf("asynchronous fixture: %#v, %v, reads=%d", posting, err, reads)
	}
}

func TestHarnessDraftCleanupFailureAndFalseAcknowledgement(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			deleteAttempted := false
			run := func(args ...string) (string, string, int) {
				if args[1] == "list" {
					if code != 0 && deleteAttempted {
						return harnessJSON([]draftData{})
					}
					return harnessJSON([]draftData{{ID: 9901}})
				}
				deleteAttempted = true
				if code != 0 {
					return "", `{"ok":false,"code":"api","error":"cleanup unavailable"}`, code
				}
				return harnessJSON(nil) // Acknowledged, but deliberately not removed.
			}
			if err := cleanupDraftWith(run, 9901); err == nil {
				t.Fatal("cleanup failure/residual draft was accepted")
			}
		})
	}
}

func TestHarnessDraftCleanupDoesNotDeleteASentMessage(t *testing.T) {
	run := func(args ...string) (string, string, int) {
		if args[1] != "list" {
			t.Fatalf("must not delete an ID that left Drafts: %v", args)
		}
		return harnessJSON([]draftData{})
	}
	if err := cleanupDraftWith(run, 9901); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessDraftCleanupReadsBackRemoval(t *testing.T) {
	present := true
	reads := 0
	run := func(args ...string) (string, string, int) {
		if args[1] == "delete" {
			present = false
			return harnessJSON(nil)
		}
		reads++
		drafts := []draftData{}
		if present {
			drafts = append(drafts, draftData{ID: 9901})
		}
		return harnessJSON(drafts)
	}
	if err := cleanupDraftWith(run, 9901); err != nil || reads != 2 {
		t.Fatalf("cleanup must read before and after: %v, reads=%d", err, reads)
	}
}

func TestHarnessEventCleanupRefusesFailuresAndResiduals(t *testing.T) {
	for _, failDelete := range []bool{true, false} {
		t.Run(fmt.Sprint(failDelete), func(t *testing.T) {
			deleteAttempted := false
			run := func(args ...string) (string, string, int) {
				if args[1] == "list" {
					if failDelete && deleteAttempted {
						return harnessJSON([]smokeEvent{})
					}
					return harnessJSON([]smokeEvent{{ID: 42, Title: "Design review unique-42"}, {ID: 99, Title: "Unrelated planning meeting"}})
				}
				deleteAttempted = true
				if args[2] != "42" {
					t.Fatalf("cleanup targeted unrelated event: %v", args)
				}
				if failDelete {
					return "", `{"ok":false,"code":"api"}`, 7
				}
				return harnessJSON(nil)
			}
			if err := cleanupEventsWith(run, []string{"42"}, "Design review unique-42", []string{"2099-04-16"}); err == nil {
				t.Fatal("cleanup failure or residual event accepted")
			}
		})
	}
}

func TestHarnessEventCleanupIncludesSwitchedOffCalendarSplits(t *testing.T) {
	remaining := map[string]bool{"42": true, "73": true}
	run := func(args ...string) (string, string, int) {
		if args[1] == "day" {
			return harnessJSON([]smokeEvent{}) // A switched-off calendar is absent here.
		}
		if args[1] == "delete" {
			if _, owned := remaining[args[2]]; !owned {
				t.Fatalf("unrelated event targeted: %v", args)
			}
			remaining[args[2]] = false
			return harnessJSON(nil)
		}
		want := []string{"event", "list", "--starts-on", "2099-04-15", "--ends-on", "2099-04-24", "--all", "--json"}
		if !slices.Equal(args, want) {
			t.Fatalf("cleanup must read all calendars across the owned window: %v", args)
		}
		var events []smokeEvent
		for _, id := range []string{"42", "73"} {
			if remaining[id] {
				number := int64(42)
				if id == "73" {
					number = 73
				}
				events = append(events, smokeEvent{ID: number, Title: "Design review unique-42"})
			}
		}
		return harnessJSON(events)
	}
	err := cleanupEventsWith(run, []string{"42"}, "Design review unique-42", []string{"2099-04-23", "2099-04-16"})
	if err != nil || remaining["42"] || remaining["73"] {
		t.Fatalf("hidden-calendar split escaped cleanup: %v, remaining=%v", err, remaining)
	}
}

func TestHarnessEventCleanupRequiresOwnedFixtures(t *testing.T) {
	run := func(...string) (string, string, int) {
		t.Fatal("empty title must be refused before any command")
		return "", "", 1
	}
	if err := cleanupEventsWith(run, []string{"42"}, "", []string{"2099-04-16"}); err == nil {
		t.Fatal("empty title accepted")
	}
}

func TestHarnessEventCleanupAlreadyGoneNeedsSuccessfulReadback(t *testing.T) {
	run := func(args ...string) (string, string, int) {
		if args[1] == "delete" {
			return "", `{"ok":false,"code":"not_found"}`, 2
		}
		return harnessJSON([]smokeEvent{{ID: 99, Title: "Unrelated planning meeting"}})
	}
	if err := cleanupEventsWith(run, []string{"42"}, "Design review unique-42", []string{"2099-04-16"}); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessContactPhotoCleanupRequiresAnObservedBaseline(t *testing.T) {
	original := "http://app.hey.localhost:3003/avatars/sam-rivera.png"
	uploaded := "http://app.hey.localhost:3003/avatars/sam-rivera-upload.png"
	for _, pair := range [][2]string{{"", ""}, {"", uploaded}, {original, ""}, {original, uploaded}} {
		if err := verifyAvatarRestored(pair[0], pair[1]); err == nil {
			t.Errorf("unproved photo cleanup passed for %v", pair)
		}
	}
	if err := verifyAvatarRestored(original, original); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessContactCleanupApprovesOnlyItsOwnedDeniedClearance(t *testing.T) {
	for _, id := range []int64{0, 77} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			approvals := 0
			run := func(args ...string) (string, string, int) {
				if args[0] == "contact" {
					return harnessJSON(map[string]any{"clearance": map[string]any{"id": id, "status": "denied"}})
				}
				if !slices.Equal(args, []string{"screener", "approve", "77", "--json"}) {
					t.Fatalf("wrong clearance targeted: %v", args)
				}
				approvals++
				return harnessJSON(nil)
			}
			err := prepareContactCleanupWith(run, "42")
			if id == 0 && (err == nil || approvals != 0) {
				t.Fatalf("missing clearance must fail without mutation: %v, %d", err, approvals)
			}
			if id == 77 && (err != nil || approvals != 1) {
				t.Fatalf("owned clearance not restored: %v, %d", err, approvals)
			}
		})
	}
}

func TestHarnessPartialObservationCannotProveCleanup(t *testing.T) {
	run := func(...string) (string, string, int) {
		return `{"ok":true,"data":[],"notice":"More pages remain"}`, "", 0
	}
	if err := cleanupDraftWith(run, 42); err == nil {
		t.Fatal("truncated listing accepted as absence")
	}
}

func TestHarnessTopicLookupRetriesAndAcceptsPositivePartialReads(t *testing.T) {
	reads := 0
	run := func(...string) (string, string, int) {
		reads++
		if reads == 1 {
			return "", "temporary read failure", 7
		}
		return `{"ok":true,"notice":"More pages remain","data":{"postings":[{"id":88,"topic_id":8801}]}}`, "", 0
	}
	posting, err := waitForCreatedTopic(run, 8801, 2, func() {})
	if err != nil || posting.ID != 88 || reads != 2 {
		t.Fatalf("positive observation was not recovered: %#v, %v, reads=%d", posting, err, reads)
	}
}

func TestHarnessTopicCleanupRequiresExactTrashReadback(t *testing.T) {
	for _, mode := range []string{"failed trash", "missing", "wrong topic", "retry", "partial positive"} {
		t.Run(mode, func(t *testing.T) {
			searches := 0
			run := func(args ...string) (string, string, int) {
				switch args[0] {
				case "box":
					return harnessJSON(map[string]any{"postings": []smokeTopicPosting{{ID: 88, TopicID: 8801}}})
				case "trash":
					if args[1] != "88" {
						t.Fatalf("wrong posting trashed: %v", args)
					}
					if mode == "failed trash" {
						return "", "trash unavailable", 7
					}
					return harnessJSON(nil)
				case "search":
					searches++
					if mode == "retry" && searches == 1 {
						return "", "search unavailable", 7
					}
					if mode == "missing" {
						return harnessJSON([]any{})
					}
					if mode == "wrong topic" {
						return harnessJSON([]map[string]int64{{"topic_id": 7701}})
					}
					return `{"ok":true,"notice":"More pages remain","data":[{"topic_id":8801}]}`, "", 0
				}
				t.Fatalf("unexpected command: %v", args)
				return "", "", 1
			}
			err := cleanupOwnedTopicWith(run, 8801, "Design review reply", 2, func() {})
			wantPass := mode == "retry" || mode == "partial positive"
			if (err == nil) != wantPass {
				t.Fatalf("cleanup result = %v, want pass=%v", err, wantPass)
			}
			if mode == "retry" && searches != 2 {
				t.Fatalf("search was not retried: %d", searches)
			}
		})
	}
}

func TestHarnessFailedDeliveryCleanupUsesOneLookup(t *testing.T) {
	reads := 0
	run := func(args ...string) (string, string, int) {
		if args[0] != "box" {
			t.Fatalf("missing topic must not trash another posting: %v", args)
		}
		reads++
		return harnessJSON(map[string]any{"postings": []smokeTopicPosting{}})
	}
	if err := cleanupOwnedTopicWith(run, 8801, "Design review reply", 1, func() {}); err == nil || reads != 1 {
		t.Fatalf("failed setup cleanup must make one lookup: %v, reads=%d", err, reads)
	}
}

func TestHarnessCLIEnvDropsInheritedCredentialsProfilesAndDebug(t *testing.T) {
	names := []string{"HEY_TOKEN", "HEY_PROFILE", "HEY_DEBUG", "HEY_CABLE_URL"}
	for _, name := range names {
		t.Setenv(name, "synthetic-caller-setting")
	}
	for _, value := range cliEnv() {
		for _, name := range names {
			if strings.HasPrefix(value, name+"=") {
				t.Errorf("inherited %s reached the CLI environment", name)
			}
		}
	}
}

func TestHarnessCLIEnvRequiresExplicitSmokeAccount(t *testing.T) {
	t.Setenv("HEY_ACCOUNT_ID", "operator-default")
	for _, requested := range []string{"", "88690000"} {
		t.Setenv("HEY_SMOKE_ACCOUNT_ID", requested)
		if !slices.Contains(cliEnv(), "HEY_ACCOUNT_ID="+requested) {
			t.Errorf("explicit smoke account not selected: %q", requested)
		}
		if slices.Contains(cliEnv(), "HEY_ACCOUNT_ID=operator-default") {
			t.Fatal("operator's account filter reached the suite")
		}
	}
}

func TestHarnessRelativeBinaryResolvedBeforeChangingDirectory(t *testing.T) {
	previousBinary, previousURL, previousEmail := binaryPath, baseURL, smokeEmail
	t.Cleanup(func() { binaryPath, baseURL, smokeEmail = previousBinary, previousURL, previousEmail })
	t.Setenv("HEY_SMOKE_OFFLINE", "")
	t.Setenv("HEY_BINARY", "../../bin/hey")
	t.Setenv("HEY_SMOKE_BASE_URL", "invalid://binary-resolution-fixture")
	want, err := filepath.Abs("../../bin/hey")
	if err != nil {
		t.Fatal(err)
	}
	runSmoke(func() int { t.Fatal("invalid origin must not run live tests"); return 1 })
	if binaryPath != want {
		t.Fatalf("binary path = %q, want %q before any child changes cwd", binaryPath, want)
	}
}

func TestHarnessEvidenceRefusesExistingPublicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission check")
	}
	for _, looseDirectory := range []bool{true, false} {
		t.Run(fmt.Sprint(looseDirectory), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "evidence")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HEY_SMOKE_EVIDENCE_DIR", directory)
			path := filepath.Join(directory, "commands.jsonl")
			if looseDirectory {
				if err := os.Chmod(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := recordSmokeCommand(t.Name(), []string{"draft", "list"}, "private fixture", "", 0); err == nil {
				t.Fatal("public evidence permissions accepted")
			}
			if info, err := os.Stat(path); err == nil && info.Size() != 0 {
				t.Fatal("private evidence was written before refusing permissions")
			}
		})
	}
}

func TestHarnessStateRemovedAfterSetupOrTestFailure(t *testing.T) {
	for _, result := range []int{0, 1} {
		t.Run(fmt.Sprint(result), func(t *testing.T) {
			var owned string
			code := withSmokeState(func() int {
				owned = filepath.Dir(configDir)
				for _, directory := range []string{configDir, stateDir, homeDir, cacheDir, workDir} {
					if _, err := os.Stat(directory); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(configDir, "synthetic-credential"), []byte("invalid synthetic fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				return result
			})
			if code != result {
				t.Fatalf("lost test/setup status: %d", code)
			}
			if _, err := os.Stat(owned); !os.IsNotExist(err) {
				t.Fatalf("owned state outlived suite: %v", err)
			}
		})
	}
}

func TestHarnessEvidenceExcludesCredentialCommandsAndSurvivesCleanup(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "evidence")
	t.Setenv("HEY_SMOKE_EVIDENCE_DIR", directory)
	code := withSmokeState(func() int {
		for _, args := range [][]string{{"auth", "token"}, {"--json", "auth", "token"}, {"login"}, {"hey"}, {"setup"}, {"config", "show", "--token=synthetic"}} {
			if err := recordSmokeCommand(t.Name(), args, "SYNTHETIC-PRIVATE-MARKER", "", 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := recordSmokeCommand(t.Name(), []string{"draft", "list"}, `{"ok":true,"data":[]}`, "", 0); err != nil {
			t.Fatal(err)
		}
		return 0
	})
	path := filepath.Join(directory, "commands.jsonl")
	data, err := os.ReadFile(path)
	if err != nil || code != 0 {
		t.Fatalf("evidence did not survive: %v, code=%d", err, code)
	}
	if strings.Contains(string(data), "PRIVATE-MARKER") || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("credential command was recorded: %s", data)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence permissions: %v", info.Mode())
	}
}
