package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

const (
	maxBubbleLookupPages = 64
	bubbleLookupTimeout  = 10 * time.Second
)

type bubbleStateLoadedMsg struct {
	picker    *bubblePicker
	canCancel bool
	err       error
}

// Search answers a posting ID but no box or bubble metadata. Resolve membership
// using the same two feeds as hey bubble list, without scanning ordinary Imbox mail.
func (v *mailView) loadBubbleState(p *bubblePicker) tea.Cmd {
	p.loading = true
	p.unknownState = true
	p.problem = ""
	ctx, cancel := context.WithTimeout(v.vc.ctx, bubbleLookupTimeout)
	p.lookupCancel = cancel
	sdk, id := v.vc.sdk, p.postingID
	return func() tea.Msg {
		defer cancel()
		canCancel, err := readBubbleState(ctx, sdk, id)
		return bubbleStateLoadedMsg{picker: p, canCancel: canCancel, err: err}
	}
}

func readBubbleState(ctx context.Context, sdk *hey.Client, postingID int64) (bool, error) {
	pages := 0
	for _, kind := range []string{hey.BoxKindImbox, hey.BoxKindBubbleUp} {
		source := mail.Source{Kind: mail.KindBox, BoxKind: kind}
		cursor := ""
		visited := make(map[string]bool)
		for {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if pages == maxBubbleLookupPages {
				return false, errors.New("bubble-up list is too large to check right now")
			}
			if visited[cursor] {
				return false, errors.New("bubble-up paging repeated a cursor")
			}
			visited[cursor] = true
			pages++
			page, err := mail.ReadPage(ctx, sdk, source, cursor)
			if err != nil {
				return false, err
			}
			prefixEnded := false
			for _, posting := range page.Postings {
				if kind == hey.BoxKindImbox && !posting.BubbledUp {
					prefixEnded = true
					break
				}
				if posting.Id == postingID {
					return true, nil
				}
			}
			if prefixEnded || len(page.Postings) == 0 || page.Cursor == "" {
				break
			}
			cursor = page.Cursor
		}
	}
	return false, nil
}

func (v *mailView) bubbleStateLoaded(msg bubbleStateLoadedMsg) {
	p := msg.picker
	if v.modal != p {
		return
	}
	if p.lookupCancel != nil {
		p.lookupCancel()
		p.lookupCancel = nil
	}
	p.loading = false
	if msg.err != nil {
		p.problem = errorNotice("Could not load Bubble Up", msg.err)
		return
	}
	current := newBubblePicker(mail.Posting{ID: p.postingID}, msg.canCancel, p.now)
	p.choices = current.choices
	p.openedDay = current.openedDay
	p.cursor = 0
	p.unknownState = false
	p.problem = ""
}
