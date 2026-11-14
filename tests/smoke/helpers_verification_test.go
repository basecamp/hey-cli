package smoke_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var smokeEvidenceMu sync.Mutex

type smokeCommand func(...string) (string, string, int)

func commandFor(t *testing.T) smokeCommand {
	return func(args ...string) (string, string, int) { return hey(t, args...) }
}

// Record only data-command results, never auth/token/browser progress. A leading
// flag or unknown command is intentionally not logged rather than guessed at.
func recordSmokeCommand(test string, args []string, stdout, stderr string, status int) error {
	directory := os.Getenv("HEY_SMOKE_EVIDENCE_DIR")
	if directory == "" || len(args) == 0 {
		return nil
	}
	allowed := " account attachment box bubble bulk-reply bundle calendar clip collection commands compose config contact doctor draft event forward habit help ignore journal label move reply screener search seen set-aside share snippet spam stop-ignoring thread timetrack todo trash unseen unshare version watch workflow "
	if !strings.Contains(allowed, " "+args[0]+" ") {
		return nil
	}
	for _, arg := range args {
		if arg == "--token" || arg == "--cookie" || strings.HasPrefix(arg, "--token=") || strings.HasPrefix(arg, "--cookie=") {
			return nil
		}
	}
	if !filepath.IsAbs(directory) {
		return fmt.Errorf("HEY_SMOKE_EVIDENCE_DIR must be absolute")
	}
	if configDir != "" {
		relative, err := filepath.Rel(filepath.Dir(configDir), directory)
		if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("command evidence must live outside disposable smoke state")
		}
	}
	smokeEvidenceMu.Lock()
	defer smokeEvidenceMu.Unlock()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("command evidence directory must be private (0700)")
	}
	encoded, err := json.Marshal(map[string]any{"test": test, "argv": args, "stdout": stdout, "stderr": stderr, "exit": status})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, "commands.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err = file.Stat()
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.Join(fmt.Errorf("command evidence file must be private (0600)"), err, file.Close())
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	return errors.Join(writeErr, file.Close())
}

func smokeObservation(run smokeCommand, args ...string) (Response, error) {
	stdout, stderr, code := run(append(args, "--json")...)
	if code != 0 {
		return Response{}, fmt.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return Response{}, fmt.Errorf("hey %s: invalid JSON: %w", strings.Join(args, " "), err)
	}
	if !response.OK {
		return Response{}, fmt.Errorf("hey %s did not confirm a successful read", strings.Join(args, " "))
	}
	return response, nil
}

func smokeJSON(run smokeCommand, args ...string) (json.RawMessage, error) {
	response, err := smokeObservation(run, args...)
	if err != nil {
		return nil, err
	}
	if response.Notice != "" {
		return nil, fmt.Errorf("hey %s: incomplete observation (notice=%q)", strings.Join(args, " "), response.Notice)
	}
	return response.Data, nil
}

func deleteSmokeResource(run smokeCommand, args ...string) error {
	stdout, stderr, code := run(append(args, "--json")...)
	if code != 0 {
		var response ErrorResponse
		if json.Unmarshal([]byte(stderr), &response) == nil && response.Code == "not_found" {
			return nil // Only an independent readback below can establish absence.
		}
		return fmt.Errorf("cleanup hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil || !response.OK {
		return fmt.Errorf("cleanup hey %s did not confirm a mutation", strings.Join(args, " "))
	}
	return nil
}

func cleanupDraftWith(run smokeCommand, id int64) error {
	listed := func() (bool, error) {
		data, err := smokeJSON(run, "draft", "list", "--all")
		if err != nil {
			return false, err
		}
		var drafts []draftData
		if err := json.Unmarshal(data, &drafts); err != nil {
			return false, err
		}
		for _, draft := range drafts {
			if draft.ID == id {
				return true, nil
			}
		}
		return false, nil
	}
	present, err := listed()
	if err != nil {
		return err
	}
	if present {
		if err := deleteSmokeResource(run, "draft", "delete", strconv.FormatInt(id, 10)); err != nil {
			return err
		}
	}
	present, err = listed()
	if err == nil && present {
		err = fmt.Errorf("draft %d remains listed after cleanup", id)
	}
	return err
}

func registerDraftCleanup(t *testing.T, id int64) {
	t.Helper()
	t.Logf("owned draft %d", id)
	t.Cleanup(func() {
		if err := cleanupDraftWith(commandFor(t), id); err != nil {
			t.Errorf("draft %d cleanup: %v", id, err)
		} else {
			t.Logf("draft %d independently confirmed absent", id)
		}
	})
}

type smokeTopicPosting struct {
	ID      int64  `json:"id"`
	TopicID int64  `json:"topic_id"`
	Kind    string `json:"kind"`
	AppURL  string `json:"app_url"`
}

func waitForCreatedTopic(run smokeCommand, topicID int64, attempts int, pause func()) (smokeTopicPosting, error) {
	if topicID <= 0 {
		return smokeTopicPosting{}, fmt.Errorf("created topic must have a positive id")
	}
	var lastErr error
	for range attempts {
		response, err := smokeObservation(run, "box", "view", "imbox", "--all")
		if err != nil {
			lastErr = err
			pause()
			continue
		}
		var box struct {
			Postings []smokeTopicPosting `json:"postings"`
		}
		if err := json.Unmarshal(response.Data, &box); err != nil {
			lastErr = err
			pause()
			continue
		}
		lastErr = nil
		for _, posting := range box.Postings {
			if posting.Kind != "bundle" && (posting.TopicID == topicID || posting.TopicID == 0 && extractTopicID(posting.AppURL) == strconv.FormatInt(topicID, 10)) {
				return posting, nil
			}
		}
		pause()
	}
	if lastErr != nil {
		return smokeTopicPosting{}, fmt.Errorf("created topic %d lookup failed: %w", topicID, lastErr)
	}
	return smokeTopicPosting{}, fmt.Errorf("created topic %d did not arrive; refusing another thread", topicID)
}

func waitForOwnedTopic(t *testing.T, topicID int64, subject string) {
	t.Helper()
	run := commandFor(t)
	pause := func() { time.Sleep(500 * time.Millisecond) }
	cleanupAttempts := 1
	t.Cleanup(func() {
		if err := cleanupOwnedTopicWith(run, topicID, subject, cleanupAttempts, pause); err != nil {
			t.Errorf("owned topic %d cleanup: %v", topicID, err)
		} else {
			t.Logf("topic %d independently confirmed in Trash", topicID)
		}
	})
	if _, err := waitForCreatedTopic(run, topicID, 60, pause); err != nil {
		t.Fatal(err)
	}
	cleanupAttempts = 60
}

func cleanupOwnedTopicWith(run smokeCommand, topicID int64, subject string, attempts int, pause func()) error {
	posting, err := waitForCreatedTopic(run, topicID, attempts, pause)
	if err != nil {
		return err
	}
	if posting.ID <= 0 {
		return fmt.Errorf("owned topic %d has no posting id for cleanup", topicID)
	}
	if _, err := smokeJSON(run, "trash", strconv.FormatInt(posting.ID, 10)); err != nil {
		return err
	}
	var lastErr error
	for range attempts {
		response, err := smokeObservation(run, "search", "--exact", subject, "--in", "trash", "--all")
		if err != nil {
			lastErr = err
			pause()
			continue
		}
		var found []struct {
			TopicID int64 `json:"topic_id"`
		}
		if err := json.Unmarshal(response.Data, &found); err != nil {
			lastErr = err
			pause()
			continue
		}
		lastErr = nil
		for _, match := range found {
			if match.TopicID == topicID {
				return nil
			}
		}
		pause()
	}
	if lastErr != nil {
		return fmt.Errorf("owned topic %d cleanup readback failed: %w", topicID, lastErr)
	}
	return fmt.Errorf("topic %d was not independently found in Trash", topicID)
}

func cleanupEventsWith(run smokeCommand, ids []string, title string, days []string) error {
	if title == "" || len(ids) == 0 || len(days) == 0 {
		return fmt.Errorf("event cleanup needs owned ids, a unique title and touched dates")
	}
	var first, last time.Time
	for _, day := range days {
		date, err := time.Parse("2006-01-02", day)
		if err != nil {
			return fmt.Errorf("invalid owned event date %q: %w", day, err)
		}
		if first.IsZero() || date.Before(first) {
			first = date
		}
		if last.IsZero() || date.After(last) {
			last = date
		}
	}
	read := func() ([]smokeEvent, error) {
		// Period views omit switched-off calendars. List every calendar over
		// the fixture's window, padded for timed events crossing UTC dates.
		data, err := smokeJSON(run, "event", "list", "--starts-on", first.AddDate(0, 0, -1).Format("2006-01-02"),
			"--ends-on", last.AddDate(0, 0, 1).Format("2006-01-02"), "--all")
		if err != nil {
			return nil, err
		}
		var events []smokeEvent
		err = json.Unmarshal(data, &events)
		return events, err
	}
	var problems []error
	// Discover split series before deleting their parents. Only the run's unique
	// title prefix is eligible; never delete somebody else's calendar or event.
	events, err := read()
	if err != nil {
		problems = append(problems, err)
	}
	for _, event := range events {
		if strings.HasPrefix(event.Title, title) {
			id := event.ParentID
			if id == 0 {
				id = event.ID
			}
			if id > 0 {
				ids = append(ids, strconv.FormatInt(id, 10))
			}
		}
	}
	for _, id := range uniqueStrings(ids) {
		if id == "0" {
			problems = append(problems, fmt.Errorf("refusing cleanup of virtual event id 0"))
			continue
		}
		if err := deleteSmokeResource(run, "event", "delete", id); err != nil {
			problems = append(problems, err)
		}
	}
	events, err = read()
	if err != nil {
		problems = append(problems, err)
	}
	for _, event := range events {
		if strings.HasPrefix(event.Title, title) {
			problems = append(problems, fmt.Errorf("owned event %q remains after cleanup", event.Title))
		}
	}
	return errors.Join(problems...)
}

func registerEventCleanup(t *testing.T, id, title string, days ...string) {
	t.Helper()
	t.Logf("owned event/series %s %q on %v", id, title, days)
	t.Cleanup(func() {
		if err := cleanupEventsWith(commandFor(t), []string{id}, title, days); err != nil {
			t.Error(err)
		} else {
			t.Logf("event/series %s independently confirmed absent on %v", id, days)
		}
	})
}

// A denied contact redirects to the Screened Out page even when hidden. Restore
// only this fixture's own clearance before using its hidden-page readback.
func prepareContactCleanupWith(run smokeCommand, id string) error {
	data, err := smokeJSON(run, "contact", "show", id)
	if err != nil {
		return err
	}
	var detail struct {
		Clearance struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"clearance"`
	}
	if err := json.Unmarshal(data, &detail); err != nil {
		return err
	}
	if detail.Clearance.Status == "denied" {
		if detail.Clearance.ID <= 0 {
			return fmt.Errorf("owned contact %s has no clearance ID for cleanup", id)
		}
		_, err = smokeJSON(run, "screener", "approve", strconv.FormatInt(detail.Clearance.ID, 10))
	}
	return err
}

func verifyAvatarRestored(original, current string) error {
	if original == "" {
		return errors.New("missing original avatar; photo cleanup has no baseline")
	}
	if current != original {
		return errors.New("contact photo was not restored to its original avatar")
	}
	return nil
}

func registerContactCleanup(t *testing.T, id, originalAvatar string) {
	t.Helper()
	t.Logf("owned contact %s (retained hidden after cleanup)", id)
	t.Cleanup(func() {
		run := commandFor(t)
		if err := prepareContactCleanupWith(run, id); err != nil {
			t.Error(err)
		}
		// Attempt every cleanup, even when an earlier operation failed.
		for _, action := range [][]string{{"show-again"}, {"unbundle"}, {"note", "delete"}, {"photo", "remove"}} {
			args := append([]string{"contact"}, action...)
			if _, err := smokeJSON(run, append(args, id)...); err != nil {
				t.Error(err)
			}
		}
		data, err := smokeJSON(run, "contact", "show", id)
		var detail struct {
			Note      string         `json:"note"`
			AvatarURL string         `json:"avatar_url"`
			Aliases   []smokeContact `json:"aliases"`
		}
		if err == nil {
			err = json.Unmarshal(data, &detail)
		}
		if err != nil {
			t.Errorf("contact %s cleanup readback: %v", id, err)
		} else {
			if detail.Note != "" {
				t.Errorf("contact %s retained its private note", id)
			}
			if err := verifyAvatarRestored(originalAvatar, detail.AvatarURL); err != nil {
				t.Errorf("contact %s: %v", id, err)
			}
		}
		if _, err := smokeJSON(run, "contact", "hide", id); err != nil {
			t.Error(err)
		}
		for _, contactID := range append([]string{id}, contactAliasIDs(detail.Aliases)...) {
			assertContains(t, fetchHTML(t, baseURL+"/contacts/"+contactID), "This contact is hidden")
		}
		if t.Failed() {
			t.Logf("contact %s cleanup/readback attempted; inspect the failed test and receipts", id)
		} else {
			t.Logf("contact %s note/avatar/hide readbacks passed; fixture retained hidden, not deleted", id)
		}
	})
}

func contactAliasIDs(aliases []smokeContact) []string {
	var ids []string
	for _, alias := range aliases {
		if alias.ID > 0 {
			ids = append(ids, strconv.Itoa(alias.ID))
		}
	}
	return ids
}
