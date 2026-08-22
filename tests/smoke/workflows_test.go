package smoke_test

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"testing"
	"time"
)

type smokeWorkflow struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
}

type smokeWorkflowDetail struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Stages []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"stages"`
}

func TestWorkflowsAndWorkflow(t *testing.T) {
	response := heyJSON(t, "workflows")
	workflows := dataAs[[]smokeWorkflow](t, response)
	if response.Summary == "" {
		t.Error("workflows response omitted summary")
	}
	if len(workflows) == 0 {
		skipf(t, "no workflows available for workflow detail validation")
	}

	workflow := workflows[0]
	detail := dataAs[smokeWorkflowDetail](t, heyJSON(t, "workflow", strconv.FormatInt(workflow.ID, 10)))
	if detail.ID != workflow.ID || detail.Name != workflow.Name || detail.Stages == nil {
		t.Errorf("workflow detail = %+v, want %+v with stages", detail, workflow)
	}
}

func TestWorkflowOutputFormats(t *testing.T) {
	workflows := dataAs[[]smokeWorkflow](t, heyJSON(t, "workflows"))
	for _, args := range [][]string{
		{"workflows", "--quiet"},
		{"workflows", "--ids-only"},
		{"workflows", "--count"},
		{"workflows", "--markdown"},
		{"workflows", "--styled"},
	} {
		_, stderr, code := hey(t, args...)
		if code != 0 {
			t.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
		}
	}
	if len(workflows) == 0 {
		skipf(t, "no workflows available for detail output validation")
	}
	id := strconv.FormatInt(workflows[0].ID, 10)
	for _, args := range [][]string{
		{"workflow", id, "--quiet"},
		{"workflow", id, "--ids-only"},
		{"workflow", id, "--count"},
		{"workflow", id, "--markdown"},
		{"workflow", id, "--styled"},
	} {
		_, stderr, code := hey(t, args...)
		if code != 0 {
			t.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
		}
	}
}

func TestWorkflowMutations(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Disposable workflow test %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", "This disposable thread verifies workflow stages.",
		"--json",
	)
	if code != 0 {
		skipf(t, "could not create a disposable thread (exit %d): %s", code, stderr)
	}
	t.Cleanup(func() { cleanupThreadBySubject(t, subject) })

	_, topicID, accountID, err := waitForPostingAndTopicIDsBySubject(t, subject)
	if err != nil {
		t.Fatalf("could not find disposable thread: %v", err)
	}
	if topicID == 0 || accountID == 0 {
		skipf(t, "disposable thread did not expose topic and account IDs")
	}

	workflowName := "Smoke workflow " + uid
	workflowWriteAvailableJSON(t, "workflow", "create", workflowName, "--account", strconv.FormatInt(accountID, 10))
	workflowID, err := waitForWorkflowIDByName(t, workflowName, accountID, true)
	if err != nil || workflowID == 0 {
		t.Fatalf("created workflow lookup = %d, %v", workflowID, err)
	}
	cleanupWorkflowID := workflowID
	t.Cleanup(func() {
		if cleanupWorkflowID != 0 {
			_, _, _ = hey(t, "workflow", "delete", strconv.FormatInt(cleanupWorkflowID, 10), "--json")
		}
	})

	updatedName := workflowName + " updated"
	workflowWriteJSON(t, "workflow", "update", strconv.FormatInt(workflowID, 10), "--name", updatedName)
	if updatedID, err := waitForWorkflowIDByName(t, updatedName, accountID, true); err != nil || updatedID != workflowID {
		t.Fatalf("updated workflow lookup = %d, %v; want %d", updatedID, err, workflowID)
	}

	detail := workflowDetail(t, workflowID)
	if len(detail.Stages) == 0 {
		t.Fatal("new workflow has no initial stage")
	}
	initialStageID := detail.Stages[0].ID
	knownStages := make(map[int64]bool, len(detail.Stages))
	for _, stage := range detail.Stages {
		knownStages[stage.ID] = true
	}

	workflowWriteJSON(t, "workflow", "stage", "create", strconv.FormatInt(workflowID, 10))
	newStageID, err := waitForNewWorkflowStage(t, workflowID, knownStages)
	if err != nil || newStageID == 0 {
		t.Fatalf("new stage lookup = %d, %v", newStageID, err)
	}
	stageName := "Interviewing " + uid
	workflowWriteJSON(t, "workflow", "stage", "update", strconv.FormatInt(workflowID, 10), strconv.FormatInt(newStageID, 10), "--name", stageName)
	if !workflowHasStage(t, workflowID, newStageID, stageName) {
		t.Fatalf("workflow %d did not retain renamed stage %d", workflowID, newStageID)
	}

	workflowWriteJSON(t, "workflow", "add", strconv.FormatInt(topicID, 10), "--to", strconv.FormatInt(workflowID, 10), "--stage", strconv.FormatInt(newStageID, 10))
	if !workflowStageContainsSubject(t, workflowID, newStageID, subject) {
		t.Errorf("workflow stage %d does not contain %q after add", newStageID, subject)
	}

	workflowWriteJSON(t, "workflow", "move", strconv.FormatInt(topicID, 10), "--workflow", strconv.FormatInt(workflowID, 10), "--to", strconv.FormatInt(initialStageID, 10))
	if !workflowStageContainsSubject(t, workflowID, initialStageID, subject) {
		t.Errorf("workflow stage %d does not contain %q after move", initialStageID, subject)
	}

	workflowWriteJSON(t, "workflow", "remove", strconv.FormatInt(topicID, 10), "--from", strconv.FormatInt(workflowID, 10))
	if strings.Contains(html.UnescapeString(fetchHTML(t, baseURL+"/workflows/"+strconv.FormatInt(workflowID, 10))), subject) {
		t.Errorf("workflow still contains %q after remove", subject)
	}

	workflowWriteJSON(t, "workflow", "stage", "delete", strconv.FormatInt(workflowID, 10), strconv.FormatInt(newStageID, 10))
	if workflowHasStage(t, workflowID, newStageID, stageName) {
		t.Errorf("workflow still contains deleted stage %d", newStageID)
	}

	workflowWriteJSON(t, "workflow", "delete", strconv.FormatInt(workflowID, 10))
	cleanupWorkflowID = 0
	if found, err := waitForWorkflowIDByName(t, updatedName, accountID, false); err != nil || found != 0 {
		t.Fatalf("deleted workflow lookup = %d, %v", found, err)
	}
}

func TestWorkflowRejectsInvalidIDs(t *testing.T) {
	for _, args := range [][]string{
		{"workflow", "0"},
		{"workflow", "stage", "delete", "1", "bad"},
		{"workflow", "add", "1", "--to", "0", "--stage", "2"},
		{"workflow", "move", "1", "--workflow", "2", "--to", "0"},
		{"workflow", "remove", "bad", "--from", "2"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			heyFail(t, args...)
		})
	}
}

func workflowWriteAvailableJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		skipf(t, "workflow writes unavailable (exit %d): %s", code, stderr)
	}
	return decodeWorkflowWrite(t, stdout)
}

func workflowWriteJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		t.Fatalf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
	}
	return decodeWorkflowWrite(t, stdout)
}

func decodeWorkflowWrite(t *testing.T, stdout string) Response {
	t.Helper()
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("invalid workflow response: %v: %s", err, stdout)
	}
	return response
}

func workflowDetail(t *testing.T, workflowID int64) smokeWorkflowDetail {
	t.Helper()
	return dataAs[smokeWorkflowDetail](t, heyJSON(t, "workflow", strconv.FormatInt(workflowID, 10)))
}

func waitForWorkflowIDByName(t *testing.T, name string, accountID int64, wantPresent bool) (int64, error) {
	t.Helper()
	var lastErr error
	for range 10 {
		stdout, stderr, code := hey(t, "workflows", "--all", "--account", strconv.FormatInt(accountID, 10), "--json")
		if code != 0 {
			lastErr = fmt.Errorf("list workflows (exit %d): %s", code, stderr)
		} else {
			var response Response
			if err := json.Unmarshal([]byte(stdout), &response); err != nil {
				lastErr = err
			} else {
				var workflows []smokeWorkflow
				if err := json.Unmarshal(response.Data, &workflows); err != nil {
					lastErr = err
				} else {
					var found int64
					for _, workflow := range workflows {
						if workflow.Name == name {
							found = workflow.ID
							break
						}
					}
					if (wantPresent && found != 0) || (!wantPresent && found == 0) {
						return found, nil
					}
					lastErr = fmt.Errorf("workflow %q presence = %v, want %v", name, found != 0, wantPresent)
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, lastErr
}

func waitForNewWorkflowStage(t *testing.T, workflowID int64, known map[int64]bool) (int64, error) {
	t.Helper()
	for range 10 {
		for _, stage := range workflowDetail(t, workflowID).Stages {
			if !known[stage.ID] {
				return stage.ID, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("workflow %d did not list a new stage", workflowID)
}

func workflowHasStage(t *testing.T, workflowID, stageID int64, name string) bool {
	t.Helper()
	for _, stage := range workflowDetail(t, workflowID).Stages {
		if stage.ID == stageID && stage.Name == name {
			return true
		}
	}
	return false
}

func workflowStageContainsSubject(t *testing.T, workflowID, stageID int64, subject string) bool {
	t.Helper()
	page := html.UnescapeString(fetchHTML(t, baseURL+"/workflows/"+strconv.FormatInt(workflowID, 10)))
	marker := fmt.Sprintf(`id="container_workflow_stage_%d"`, stageID)
	start := strings.Index(page, marker)
	if start < 0 {
		return false
	}
	rest := page[start+len(marker):]
	if end := strings.Index(rest, `<section class="workflow__stage `); end >= 0 {
		rest = rest[:end]
	}
	return strings.Contains(rest, subject)
}
