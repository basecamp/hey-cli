package smoke_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func parseResponse(t *testing.T, stdout string) Response {
	t.Helper()
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw: %s", err, stdout)
	}
	if !resp.OK {
		t.Fatalf("response ok=false: %s", stdout)
	}
	return resp
}

type draftData struct {
	ID int64 `json:"id"`
}

type draftState struct {
	ID      int64    `json:"id"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	To      []string `json:"to"`
	CC      []string `json:"cc"`
	IsReply bool     `json:"is_reply"`
}

// composeDraft saves a draft and answers its id, skipping (or failing under
// HEY_SMOKE_STRICT) when the server refuses the write.
func composeDraft(t *testing.T, subject string, extra ...string) int64 {
	t.Helper()
	args := append([]string{"compose", "--subject", subject, "-m", "Smoke test draft body.", "--draft", "--json"}, extra...)
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		skipf(t, "compose --draft not accepted by server (exit %d): %s", code, stderr)
	}
	resp := parseResponse(t, stdout)
	draft := dataAs[draftData](t, resp)
	if draft.ID <= 0 {
		t.Fatalf("draft id = %d, want positive", draft.ID)
	}
	t.Cleanup(func() {
		_, _, _ = hey(t, "draft", "delete", fmt.Sprintf("%d", draft.ID), "--json")
	})
	return draft.ID
}

func draftListedIDs(t *testing.T) map[int64]bool {
	t.Helper()
	resp := heyJSON(t, "draft", "list", "--all")
	ids := map[int64]bool{}
	if resp.Data == nil {
		return ids
	}
	for _, d := range dataAs[[]draftData](t, resp) {
		ids[d.ID] = true
	}
	return ids
}

func TestDraftLifecycle(t *testing.T) {
	subject := "Draft lifecycle smoke " + uniqueID()
	id := composeDraft(t, subject)

	if !draftListedIDs(t)[id] {
		t.Errorf("draft %d missing from hey draft list --all", id)
	}

	// Read it back.
	resp := heyJSON(t, "draft", "show", fmt.Sprintf("%d", id))
	state := dataAs[draftState](t, resp)
	if state.Subject != subject || !strings.Contains(state.Body, "Smoke test draft body") {
		t.Errorf("draft show = %+v", state)
	}
	if len(state.To) != 0 {
		t.Errorf("a fresh draft should have no recipients, got %v", state.To)
	}

	// Edit adds a recipient and rewrites the subject; unflagged fields survive.
	newSubject := subject + " (v2)"
	stdout, stderr, code := hey(t, "draft", "edit", fmt.Sprintf("%d", id),
		"--subject", newSubject, "--to", smokeEmail, "--json")
	if code != 0 {
		skipf(t, "draft edit not accepted by server (exit %d): %s", code, stderr)
	}
	_ = stdout
	resp = heyJSON(t, "draft", "show", fmt.Sprintf("%d", id))
	state = dataAs[draftState](t, resp)
	if state.Subject != newSubject {
		t.Errorf("subject = %q, want %q", state.Subject, newSubject)
	}
	if len(state.To) != 1 || state.To[0] != smokeEmail {
		t.Errorf("to = %v, want [%s]", state.To, smokeEmail)
	}
	if !strings.Contains(state.Body, "Smoke test draft body") {
		t.Errorf("body was not preserved through the edit: %q", state.Body)
	}

	// Send it (to self); it leaves the drafts index.
	stdout, stderr, code = hey(t, "draft", "send", fmt.Sprintf("%d", id), "--json")
	if code != 0 {
		skipf(t, "draft send not accepted by server (exit %d): %s", code, stderr)
	}
	_ = stdout
	if draftListedIDs(t)[id] {
		t.Errorf("draft %d still listed after sending", id)
	}
}

func TestDraftDelete(t *testing.T) {
	id := composeDraft(t, "Draft delete smoke "+uniqueID())

	stdout, stderr, code := hey(t, "draft", "delete", fmt.Sprintf("%d", id), "--json")
	if code != 0 {
		skipf(t, "draft delete not accepted by server (exit %d): %s", code, stderr)
	}
	_ = stdout
	if draftListedIDs(t)[id] {
		t.Errorf("draft %d still listed after delete", id)
	}
}

func TestDraftSendRefusesWithoutRecipients(t *testing.T) {
	id := composeDraft(t, "Draft no-recipients smoke "+uniqueID())

	_, stderr := heyFail(t, "draft", "send", fmt.Sprintf("%d", id))
	if !strings.Contains(stderr, "no recipients") {
		t.Errorf("stderr = %q, want a no-recipients refusal", stderr)
	}
}
