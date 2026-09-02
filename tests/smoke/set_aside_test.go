package smoke_test

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

type smokeSetAsideGroup struct {
	ID          int64 `json:"id"`
	ThreadCount int   `json:"thread_count"`
}

type smokeSetAsideGroupDetail struct {
	ID       int64 `json:"id"`
	BoxID    int64 `json:"box_id"`
	Postings []struct {
		ID         int64 `json:"id"`
		TopicID    int64 `json:"topic_id"`
		BoxGroupID int64 `json:"box_group_id"`
	} `json:"postings"`
	TotalCount int `json:"total_count"`
}

func TestSetAsideView(t *testing.T) {
	response := heyJSON(t, "set-aside", "view", "--limit", "2")
	box := dataAs[struct {
		ID       int64  `json:"id"`
		Kind     string `json:"kind"`
		Postings []any  `json:"postings"`
	}](t, response)
	if box.Kind != "asidebox" || box.Postings == nil {
		t.Errorf("set-aside view = %+v, want the asidebox with postings", box)
	}
	if response.Summary == "" {
		t.Error("set-aside view omitted summary")
	}
}

func TestSetAsideOutputFormats(t *testing.T) {
	for _, args := range [][]string{
		{"set-aside", "view", "--quiet"},
		{"set-aside", "view", "--ids-only"},
		{"set-aside", "view", "--count"},
		{"set-aside", "view", "--markdown"},
		{"set-aside", "view", "--styled"},
		{"set-aside", "group", "list", "--quiet"},
		{"set-aside", "group", "list", "--ids-only"},
		{"set-aside", "group", "list", "--count"},
		{"set-aside", "group", "list", "--markdown"},
		{"set-aside", "group", "list", "--styled"},
	} {
		_, stderr, code := hey(t, args...)
		if code != 0 {
			t.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
		}
	}
}

func TestSetAsideGroupMutations(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Disposable Set Aside group test %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", "This disposable thread verifies Set Aside grouping.",
		"--json",
	)
	if code != 0 {
		skipf(t, "could not create a disposable thread (exit %d): %s", code, stderr)
	}
	t.Cleanup(func() { cleanupThreadBySubject(t, subject) })

	postingID, topicID, _, err := waitForPostingAndTopicIDsBySubject(t, subject)
	if err != nil {
		t.Fatalf("could not find disposable thread: %v", err)
	}
	if postingID == 0 || topicID == 0 {
		skipf(t, "disposable thread did not expose posting and topic IDs")
	}
	posting := strconv.FormatInt(postingID, 10)

	created := setAsideWriteJSON(t, "set-aside", "group", "create", posting)
	groupID := dataAs[struct {
		ID int64 `json:"id"`
	}](t, created).ID
	if groupID == 0 {
		t.Fatalf("group create answered no id: %+v", created)
	}
	group := strconv.FormatInt(groupID, 10)

	assertSetAsideGroupListed(t, groupID, 1, true)

	detail := dataAs[smokeSetAsideGroupDetail](t, heyJSON(t, "set-aside", "group", "view", group, "--all"))
	if detail.ID != groupID || detail.TotalCount != 1 || len(detail.Postings) != 1 {
		t.Fatalf("group view = %+v, want group %d with one posting", detail, groupID)
	}
	if got := detail.Postings[0]; got.ID != postingID || got.TopicID != topicID || got.BoxGroupID != groupID {
		t.Errorf("group posting = %+v, want posting %d/topic %d in group %d", got, postingID, topicID, groupID)
	}

	view := dataAs[struct {
		Postings []struct {
			ID         int64 `json:"id"`
			BoxGroupID int64 `json:"box_group_id"`
		} `json:"postings"`
	}](t, heyJSON(t, "set-aside", "view", "--all"))
	grouped := false
	for _, item := range view.Postings {
		if item.ID == postingID && item.BoxGroupID == groupID {
			grouped = true
		}
	}
	if !grouped {
		t.Errorf("set-aside view does not show posting %d in group %d", postingID, groupID)
	}

	setAsideWriteJSON(t, "set-aside", "group", "add", posting, "--to", group)
	assertSetAsideGroupListed(t, groupID, 1, true)

	setAsideWriteJSON(t, "set-aside", "group", "remove", posting)
	assertSetAsideGroupListed(t, groupID, 0, false)
	if _, stderr := heyFail(t, "set-aside", "group", "view", group); !strings.Contains(stderr, "not_found") && !strings.Contains(stderr, "not found") {
		t.Errorf("viewing the dissolved group should answer not found, got: %s", stderr)
	}

	recreated := setAsideWriteJSON(t, "set-aside", "group", "create", posting)
	groupID = dataAs[struct {
		ID int64 `json:"id"`
	}](t, recreated).ID
	setAsideWriteJSON(t, "set-aside", "group", "delete", strconv.FormatInt(groupID, 10))
	assertSetAsideGroupListed(t, groupID, 0, false)

	imbox := dataAs[struct {
		Postings []struct {
			ID int64 `json:"id"`
		} `json:"postings"`
	}](t, heyJSON(t, "box", "view", "imbox", "--all"))
	backInImbox := false
	for _, item := range imbox.Postings {
		if item.ID == postingID {
			backInImbox = true
		}
	}
	if !backInImbox {
		t.Errorf("deleting the group should move posting %d back to the Imbox", postingID)
	}
}

func TestSetAsideGroupMutationValidation(t *testing.T) {
	if _, stderr := heyFail(t, "set-aside", "group", "add", "1"); !strings.Contains(stderr, "--to") {
		t.Errorf("group add without --to should ask for it, got: %s", stderr)
	}
	if _, stderr := heyFail(t, "set-aside", "group", "create", "not-a-number"); !strings.Contains(stderr, "invalid") {
		t.Errorf("group create with a bad id should refuse it, got: %s", stderr)
	}
	if _, stderr := heyFail(t, "set-aside", "group", "delete", "0"); stderr == "" {
		t.Error("group delete 0 should fail")
	}
}

func setAsideWriteJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		skipf(t, "Set Aside group write unavailable (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("invalid Set Aside group response: %v: %s", err, stdout)
	}
	return response
}

func assertSetAsideGroupListed(t *testing.T, groupID int64, threadCount int, want bool) {
	t.Helper()
	groups := dataAs[[]smokeSetAsideGroup](t, heyJSON(t, "set-aside", "group", "list"))
	found := false
	for _, group := range groups {
		if group.ID == groupID {
			found = true
			if group.ThreadCount != threadCount {
				t.Errorf("group %d thread_count = %d, want %d", groupID, group.ThreadCount, threadCount)
			}
		}
	}
	if found != want {
		t.Errorf("group %d listed = %v, want %v (groups %+v)", groupID, found, want, groups)
	}
}
