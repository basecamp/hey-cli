package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

const (
	bubbleTestPostingID int64 = 1232578819
	bubbleTestTopicID   int64 = 2101829422
)

type bubbleTestState string

const (
	bubbleStateUnbubbled         bubbleTestState = "unbubbled"
	bubbleStateBubbled           bubbleTestState = "bubbled"
	bubbleStateScheduled         bubbleTestState = "scheduled"
	bubbleStateMissing           bubbleTestState = "missing"
	bubbleStateMismatch          bubbleTestState = "mismatch"
	bubbleStateSharedTopic       bubbleTestState = "shared_topic"
	bubbleStateTopicConflict     bubbleTestState = "topic_conflict"
	bubbleStateIncompletePosting bubbleTestState = "incomplete_posting"
	bubbleStatePaginated         bubbleTestState = "paginated"
	bubbleStateIncomplete        bubbleTestState = "incomplete"
)

type bubbleTestService struct {
	t                      *testing.T
	server                 *httptest.Server
	mu                     sync.Mutex
	state                  bubbleTestState
	nowPOST                int
	popDELETE              int
	mutationStatus         int
	skipMutationTransition bool
}

func newBubbleTestService(t *testing.T, initial bubbleTestState) *bubbleTestService {
	t.Helper()
	service := &bubbleTestService{t: t, state: initial}
	service.server = httptest.NewServer(http.HandlerFunc(service.serveHTTP))
	return service
}

func (s *bubbleTestService) close() {
	s.server.Close()
}

func (s *bubbleTestService) counts() (nowPOST, popDELETE int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nowPOST, s.popDELETE
}

func (s *bubbleTestService) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.t.Helper()
	s.t.Logf("%s %s", r.Method, r.URL.Path)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/imbox.json":
		s.writeBox(w, "imbox")
	case r.Method == http.MethodGet && r.URL.Path == "/bubble_up.json":
		s.writeBox(w, "bubble_up")
	case r.Method == http.MethodGet && r.URL.Path == "/imbox/history.json":
		switch s.state {
		case bubbleStatePaginated:
			s.writeBoxPayload(w, "imbox", true, false, bubbleTestTopicID, "")
		case bubbleStateIncomplete:
			s.writeBoxPayload(w, "imbox", false, false, bubbleTestTopicID, s.server.URL+"/imbox/history-2.json")
		default:
			s.t.Errorf("unexpected history request in state %q", s.state)
			w.WriteHeader(http.StatusNotFound)
		}
	case r.Method == http.MethodPost && r.URL.Path == "/postings/bulk_bubble_up_now.json":
		s.nowPOST++
		s.assertBubbleUpNowRequest(r)
		if !s.skipMutationTransition {
			s.state = bubbleStateBubbled
		}
		s.writeMutationStatus(w)
	case r.Method == http.MethodDelete && r.URL.Path == "/postings/bubble_up.json":
		s.popDELETE++
		if got := r.URL.Query().Get("posting_ids"); got != strconvInt64(bubbleTestPostingID) {
			s.t.Errorf("posting_ids = %q, want %d", got, bubbleTestPostingID)
		}
		if !s.skipMutationTransition {
			s.state = bubbleStateUnbubbled
		}
		s.writeMutationStatus(w)
	default:
		s.t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *bubbleTestService) writeMutationStatus(w http.ResponseWriter) {
	status := s.mutationStatus
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (s *bubbleTestService) assertBubbleUpNowRequest(r *http.Request) {
	s.t.Helper()
	var payload struct {
		PostingIDs []int64 `json:"posting_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.t.Errorf("decode Bubble Up Now request: %v", err)
		return
	}
	if len(payload.PostingIDs) != 1 || payload.PostingIDs[0] != bubbleTestPostingID {
		s.t.Errorf("posting_ids = %v, want [%d]", payload.PostingIDs, bubbleTestPostingID)
	}
}

func (s *bubbleTestService) writeBox(w http.ResponseWriter, kind string) {
	s.t.Helper()
	include := false
	bubbled := false
	topicID := bubbleTestTopicID
	nextURL := ""

	switch kind {
	case "imbox":
		include = s.state == bubbleStateUnbubbled || s.state == bubbleStateBubbled || s.state == bubbleStateMismatch || s.state == bubbleStateSharedTopic || s.state == bubbleStateIncompletePosting
		bubbled = s.state == bubbleStateBubbled
		if s.state == bubbleStateMismatch {
			topicID++
		}
		if s.state == bubbleStateIncompletePosting {
			topicID = 0
		}
		if s.state == bubbleStatePaginated || s.state == bubbleStateIncomplete {
			nextURL = s.server.URL + "/imbox/history.json"
		}
	case "bubble_up":
		include = s.state == bubbleStateScheduled
	}
	if s.state == bubbleStateMissing {
		include = false
	}
	s.writeBoxPayload(w, kind, include, bubbled, topicID, nextURL)
}

func (s *bubbleTestService) writeBoxPayload(w http.ResponseWriter, kind string, include, bubbled bool, topicID int64, nextURL string) {
	s.t.Helper()
	postings := []map[string]any{}
	if kind == "imbox" && (s.state == bubbleStateSharedTopic || s.state == bubbleStateTopicConflict) {
		postings = append(postings, map[string]any{
			"id":         bubbleTestPostingID + 1,
			"kind":       "topic",
			"app_url":    fmt.Sprintf("%s/topics/%d", s.server.URL, bubbleTestTopicID),
			"box_id":     1,
			"bubbled_up": false,
		})
	}
	if include {
		appURL := ""
		if topicID > 0 {
			appURL = fmt.Sprintf("%s/topics/%d", s.server.URL, topicID)
		}
		postings = append(postings, map[string]any{
			"id":         bubbleTestPostingID,
			"kind":       "topic",
			"app_url":    appURL,
			"box_id":     1,
			"bubbled_up": bubbled,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"id":       1,
		"kind":     kind,
		"name":     kind,
		"postings": postings,
	}
	if nextURL != "" {
		payload["next_history_url"] = nextURL
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func TestBubbleUpNowMutatesOnceAndVerifiesExactPair(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateUnbubbled)
	defer service.close()

	response, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
	if err != nil {
		t.Fatalf("bubble-up-now: %v", err)
	}
	result := decodeBubbleResult(t, response)
	if !result.Changed || result.NoOp || !result.Verified {
		t.Errorf("result = %+v", result)
	}
	if result.Action != string(bubbleNowAction) || result.PostingID != bubbleTestPostingID || result.TopicID != bubbleTestTopicID {
		t.Errorf("target result = %+v", result)
	}
	if !result.After.InImbox || !result.After.BubbledUp || result.After.Scheduled {
		t.Errorf("after = %+v", result.After)
	}
	nowPOST, popDELETE := service.counts()
	if nowPOST != 1 || popDELETE != 0 {
		t.Errorf("counts now/pop = %d/%d", nowPOST, popDELETE)
	}
}

func TestPopMutatesOnceAndVerifiesExactPair(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateBubbled)
	defer service.close()

	response, err := runBubbleCommand(t, service.server.URL, "pop")
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	result := decodeBubbleResult(t, response)
	if !result.Changed || result.NoOp || !result.Verified {
		t.Errorf("result = %+v", result)
	}
	if result.Action != string(popAction) || result.After.BubbledUp || result.After.Scheduled || !result.After.InImbox {
		t.Errorf("result = %+v", result)
	}
	nowPOST, popDELETE := service.counts()
	if nowPOST != 0 || popDELETE != 1 {
		t.Errorf("counts now/pop = %d/%d", nowPOST, popDELETE)
	}
}

func TestBubbleActionsHandleScheduledPosting(t *testing.T) {
	for _, command := range []string{"bubble-up-now", "pop"} {
		t.Run(command, func(t *testing.T) {
			service := newBubbleTestService(t, bubbleStateScheduled)
			defer service.close()

			response, err := runBubbleCommand(t, service.server.URL, command)
			if err != nil {
				t.Fatalf("%s scheduled posting: %v", command, err)
			}
			result := decodeBubbleResult(t, response)
			if !result.Before.Scheduled || !result.Changed || !result.Verified {
				t.Errorf("result = %+v", result)
			}
		})
	}
}

func TestBubbleActionsReturnVerifiedNoOp(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		state   bubbleTestState
	}{
		{name: "already bubbled", command: "bubble-up-now", state: bubbleStateBubbled},
		{name: "already popped", command: "pop", state: bubbleStateUnbubbled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newBubbleTestService(t, tc.state)
			defer service.close()

			response, err := runBubbleCommand(t, service.server.URL, tc.command)
			if err != nil {
				t.Fatalf("%s: %v", tc.command, err)
			}
			result := decodeBubbleResult(t, response)
			if result.Changed || !result.NoOp || !result.Verified || result.Reason == "" {
				t.Errorf("result = %+v", result)
			}
			nowPOST, popDELETE := service.counts()
			if nowPOST != 0 || popDELETE != 0 {
				t.Errorf("no-op requests now/pop = %d/%d", nowPOST, popDELETE)
			}
		})
	}
}

func TestBubbleUpNowVerifiesAppliedStateAfterAmbiguousResponse(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateUnbubbled)
	service.mutationStatus = http.StatusInternalServerError
	defer service.close()

	response, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
	if err != nil {
		t.Fatalf("bubble-up-now after ambiguous response: %v", err)
	}
	result := decodeBubbleResult(t, response)
	if !result.Changed || !result.Verified || result.Reason == "" || !result.After.BubbledUp {
		t.Errorf("result = %+v", result)
	}
	nowPOST, _ := service.counts()
	if nowPOST != 1 {
		t.Errorf("Bubble Up Now POSTs = %d, want 1", nowPOST)
	}
}

func TestBubbleUpNowDoesNotReplayUnconfirmedMutation(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateUnbubbled)
	service.mutationStatus = http.StatusInternalServerError
	service.skipMutationTransition = true
	defer service.close()

	_, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
	if err == nil {
		t.Fatal("expected unconfirmed mutation to fail")
	}
	if got := output.AsError(err).Code; got != "mutation_unconfirmed" {
		t.Errorf("code = %q, want mutation_unconfirmed (error: %v)", got, err)
	}
	nowPOST, _ := service.counts()
	if nowPOST != 1 {
		t.Errorf("Bubble Up Now POSTs = %d, want 1", nowPOST)
	}
}

func TestBubbleActionsFailClosedOnTargetMismatchOrAbsence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    bubbleTestState
		wantCode string
	}{
		{name: "mismatch", state: bubbleStateMismatch, wantCode: "target_mismatch"},
		{name: "topic conflict", state: bubbleStateTopicConflict, wantCode: "target_mismatch"},
		{name: "incomplete posting", state: bubbleStateIncompletePosting, wantCode: "target_incomplete"},
		{name: "missing", state: bubbleStateMissing, wantCode: "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newBubbleTestService(t, tc.state)
			defer service.close()

			_, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
			if err == nil {
				t.Fatal("expected command to fail")
			}
			if got := output.AsError(err).Code; got != tc.wantCode {
				t.Errorf("code = %q, want %q (error: %v)", got, tc.wantCode, err)
			}
			nowPOST, popDELETE := service.counts()
			if nowPOST != 0 || popDELETE != 0 {
				t.Errorf("failed target requests now/pop = %d/%d", nowPOST, popDELETE)
			}
		})
	}
}

func TestBubbleTargetLookupPrefersExactPairWhenAnotherPostingSharesTopic(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateSharedTopic)
	defer service.close()

	response, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
	if err != nil {
		t.Fatalf("bubble-up-now with shared topic: %v", err)
	}
	result := decodeBubbleResult(t, response)
	if !result.Before.Present || !result.Changed || !result.Verified {
		t.Errorf("result = %+v", result)
	}
	nowPOST, popDELETE := service.counts()
	if nowPOST != 1 || popDELETE != 0 {
		t.Errorf("shared-topic requests now/pop = %d/%d", nowPOST, popDELETE)
	}
}

func TestBubbleTargetLookupFollowsEveryPageBeforeMutating(t *testing.T) {
	service := newBubbleTestService(t, bubbleStatePaginated)
	defer service.close()

	response, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
	if err != nil {
		t.Fatalf("bubble-up-now with paginated target: %v", err)
	}
	result := decodeBubbleResult(t, response)
	if !result.Before.Present || !result.Changed || !result.Verified {
		t.Errorf("result = %+v", result)
	}
	nowPOST, _ := service.counts()
	if nowPOST != 1 {
		t.Errorf("Bubble Up Now POSTs = %d, want 1", nowPOST)
	}
}

func TestBubbleTargetLookupRejectsIncompletePagination(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateIncomplete)
	defer service.close()

	_, err := runBubbleCommand(t, service.server.URL, "bubble-up-now")
	if err == nil {
		t.Fatal("expected incomplete pagination to fail")
	}
	if got := output.AsError(err).Code; got != "target_lookup_incomplete" {
		t.Errorf("code = %q, want target_lookup_incomplete (error: %v)", got, err)
	}
	nowPOST, popDELETE := service.counts()
	if nowPOST != 0 || popDELETE != 0 {
		t.Errorf("incomplete lookup requests now/pop = %d/%d", nowPOST, popDELETE)
	}
}

func TestBubbleActionsRequirePositiveExactIDs(t *testing.T) {
	service := newBubbleTestService(t, bubbleStateUnbubbled)
	defer service.close()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing topic", args: []string{"bubble-up-now", fmt.Sprint(bubbleTestPostingID)}},
		{name: "zero posting", args: []string{"bubble-up-now", "0", "--topic-id", fmt.Sprint(bubbleTestTopicID)}},
		{name: "nonnumeric posting", args: []string{"bubble-up-now", "nope", "--topic-id", fmt.Sprint(bubbleTestTopicID)}},
		{name: "zero topic", args: []string{"pop", fmt.Sprint(bubbleTestPostingID), "--topic-id", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runBubbleArgs(t, service.server.URL, tc.args...)
			if err == nil {
				t.Fatal("expected invalid IDs to fail")
			}
			if got := output.AsError(err).Code; got != "usage" {
				t.Errorf("code = %q, want usage", got)
			}
		})
	}
	nowPOST, popDELETE := service.counts()
	if nowPOST != 0 || popDELETE != 0 {
		t.Errorf("invalid target requests now/pop = %d/%d", nowPOST, popDELETE)
	}
}

func runBubbleCommand(t *testing.T, serverURL, command string) (output.Response, error) {
	t.Helper()
	return runBubbleArgs(t, serverURL, command, strconvInt64(bubbleTestPostingID), "--topic-id", strconvInt64(bubbleTestTopicID))
}

func runBubbleArgs(t *testing.T, serverURL string, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	commandArgs := make([]string, 0, 3+len(args))
	commandArgs = append(commandArgs, "--json", "--base-url", serverURL)
	commandArgs = append(commandArgs, args...)
	root.SetArgs(commandArgs)

	err := root.Execute()
	var response output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &response)
	}
	return response, err
}

func decodeBubbleResult(t *testing.T, response output.Response) bubbleActionResult {
	t.Helper()
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var result bubbleActionResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal response data: %v", err)
	}
	return result
}

func strconvInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}
