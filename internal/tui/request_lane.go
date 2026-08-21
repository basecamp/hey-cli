package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// requestResult is what a response carries so the lane that asked for it can tell
// the answer to the current question from the answer to one the reader has since
// changed. Messages embed it.
type requestResult struct {
	requestID uint64
	err       error
}

func newRequestResult(requestID uint64, err error) requestResult {
	return requestResult{requestID: requestID, err: err}
}

// requestLane tracks the one read a section is waiting on: the read the user asked
// for, the one whose spinner is on screen and whose answer replaces what they are
// looking at. Beginning another supersedes it, so a slower answer to an older
// question is discarded rather than applied on top of the newer one.
//
// That is the only read this belongs to. A page below the bottom of a list and a
// live re-read of its top page are different lanes and keep their own counters:
// they must never show the spinner, never cancel the read the reader is waiting
// on, and never be mistaken for it. Handing one of them a requestLane would do
// all three.
//
// K is the section's request kind. Its zero value means nothing is in flight.
type requestLane[K comparable] struct {
	id            uint64
	kind          K
	requestCancel context.CancelFunc
	loading       bool
}

// begin supersedes whatever the lane was waiting on and returns the id and context
// for the new read.
func (l *requestLane[K]) begin(ctx context.Context, kind K) (uint64, context.Context) {
	if l.requestCancel != nil {
		l.requestCancel()
	}
	l.id++
	requestCtx, cancel := context.WithCancel(ctx)
	l.kind = kind
	l.requestCancel = cancel
	l.loading = true
	return l.id, requestCtx
}

// settle is the whole prologue of a response handler: a superseded response is
// discarded, the read it answers is closed, and an error becomes the command that
// reports it. The handler carries on only when ok is true.
func (l *requestLane[K]) settle(result requestResult) (cmd tea.Cmd, ok bool) {
	if !l.accepts(result) {
		return nil, false
	}
	l.finish(result.requestID)
	if result.err != nil {
		return func() tea.Msg { return errMsg{result.err} }, false
	}
	return nil, true
}

// accepts reports whether a response answers the read the lane is waiting on. A
// handler with its own error branch pairs it with finish instead of using settle.
func (l *requestLane[K]) accepts(result requestResult) bool {
	return result.requestID == l.id
}

func (l *requestLane[K]) finish(requestID uint64) {
	if requestID == l.id {
		if l.requestCancel != nil {
			l.requestCancel()
		}
		var idle K
		l.kind = idle
		l.requestCancel = nil
		l.loading = false
	}
}

// cancel abandons the read in flight. The response tagged with its id is no longer
// the lane's, so it is discarded when it arrives.
func (l *requestLane[K]) cancel() {
	if l.requestCancel != nil {
		l.requestCancel()
	}
	l.id++
	var idle K
	l.kind = idle
	l.requestCancel = nil
	l.loading = false
}
