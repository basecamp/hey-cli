package tui

import (
	"context"
	"errors"
	"testing"
)

type testRequestKind int

const (
	testRequestNone testRequestKind = iota
	testRequestRead
	testRequestWrite
)

func TestRequestLaneSupersedesTheReadItWasWaitingOn(t *testing.T) {
	var lane requestLane[testRequestKind]

	firstID, firstCtx := lane.begin(context.Background(), testRequestRead)
	secondID, secondCtx := lane.begin(context.Background(), testRequestWrite)

	if firstID == secondID {
		t.Fatalf("both reads carry id %d", firstID)
	}
	if firstCtx.Err() == nil {
		t.Error("the superseded read was left running")
	}
	if secondCtx.Err() != nil {
		t.Errorf("the current read was canceled: %v", secondCtx.Err())
	}
	if !lane.loading || lane.kind != testRequestWrite {
		t.Errorf("lane = loading:%v kind:%v", lane.loading, lane.kind)
	}
	if lane.accepts(requestResult{requestID: firstID}) {
		t.Error("the superseded response was accepted")
	}
	if !lane.accepts(requestResult{requestID: secondID}) {
		t.Error("the current response was discarded")
	}
}

func TestRequestLaneSettleDiscardsAndReports(t *testing.T) {
	var lane requestLane[testRequestKind]

	staleID, _ := lane.begin(context.Background(), testRequestRead)
	currentID, _ := lane.begin(context.Background(), testRequestRead)

	if cmd, ok := lane.settle(requestResult{requestID: staleID}); ok || cmd != nil {
		t.Errorf("stale settle = cmd:%v ok:%v", cmd != nil, ok)
	}
	if !lane.loading {
		t.Error("a stale response closed the current read")
	}

	failure := errors.New("contacts are away")
	cmd, ok := lane.settle(newRequestResult(currentID, failure))
	if ok || cmd == nil {
		t.Fatalf("failed settle = cmd:%v ok:%v", cmd != nil, ok)
	}
	if reported, isErr := cmd().(errMsg); !isErr || !errors.Is(reported.err, failure) {
		t.Errorf("failure reported as %v", cmd())
	}
	if lane.loading || lane.kind != testRequestNone {
		t.Errorf("failed read left the lane open: loading:%v kind:%v", lane.loading, lane.kind)
	}
}

func TestRequestLaneFinishOnlyClosesItsOwnRead(t *testing.T) {
	var lane requestLane[testRequestKind]

	staleID, _ := lane.begin(context.Background(), testRequestRead)
	currentID, currentCtx := lane.begin(context.Background(), testRequestWrite)

	lane.finish(staleID)
	if !lane.loading || lane.kind != testRequestWrite || currentCtx.Err() != nil {
		t.Errorf("a stale response closed the current read: loading:%v kind:%v ctx:%v", lane.loading, lane.kind, currentCtx.Err())
	}

	lane.finish(currentID)
	if lane.loading || lane.kind != testRequestNone || currentCtx.Err() == nil {
		t.Errorf("finished lane = loading:%v kind:%v ctx:%v", lane.loading, lane.kind, currentCtx.Err())
	}
}

func TestRequestLaneCancelAbandonsTheResponse(t *testing.T) {
	var lane requestLane[testRequestKind]

	requestID, ctx := lane.begin(context.Background(), testRequestRead)
	lane.cancel()

	if ctx.Err() == nil {
		t.Error("the abandoned read was left running")
	}
	if lane.loading || lane.kind != testRequestNone {
		t.Errorf("canceled lane = loading:%v kind:%v", lane.loading, lane.kind)
	}
	if lane.accepts(requestResult{requestID: requestID}) {
		t.Error("the abandoned response was accepted")
	}
}
