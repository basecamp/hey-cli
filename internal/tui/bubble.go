package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

type bubbleAction int

const (
	bubbleNow bubbleAction = iota
	bubbleSchedule
	bubbleChooseDate
	bubbleCancel
)

type bubbleChoice struct {
	action bubbleAction
	name   string
	slot   hey.BubbleUpSlot
	at     time.Time
}

type bubblePicker struct {
	plainModal
	postingID    int64
	choices      []bubbleChoice
	cursor       int
	date         *dateTimePicker
	busy         bool
	loading      bool
	unknownState bool
	lookupCancel context.CancelFunc
	problem      string
	now          func() time.Time
	openedDay    string
}

type bubbleDoneMsg struct {
	picker  *bubblePicker
	message string
	err     error
}

func newBubblePicker(posting mail.Posting, scheduled bool, now func() time.Time) *bubblePicker {
	at := now().UTC()
	morning := time.Date(at.Year(), at.Month(), at.Day(), 8, 0, 0, 0, time.UTC)
	p := &bubblePicker{postingID: posting.ID, now: now, openedDay: at.Format("2006-01-02")}
	p.choices = append(p.choices, bubbleChoice{name: "Now"})
	if evening := morning.Add(10 * time.Hour); evening.After(at) {
		p.choices = append(p.choices, bubbleChoice{name: "Later today", action: bubbleSchedule, slot: hey.BubbleUpLaterToday, at: evening})
	}
	p.choices = append(p.choices,
		bubbleChoice{name: "Tomorrow", action: bubbleSchedule, slot: hey.BubbleUpTomorrow, at: morning.AddDate(0, 0, 1)},
		bubbleChoice{name: "This weekend", action: bubbleSchedule, slot: hey.BubbleUpThisWeekend, at: morning.AddDate(0, 0, nextBubbleDay(at.Weekday(), time.Saturday))},
		bubbleChoice{name: "Next week", action: bubbleSchedule, slot: hey.BubbleUpNextWeek, at: morning.AddDate(0, 0, nextBubbleDay(at.Weekday(), time.Monday))},
		bubbleChoice{name: "Choose a date…", action: bubbleChooseDate},
	)
	if scheduled || posting.BubbledUp {
		p.choices = append(p.choices, bubbleChoice{name: "Cancel bubble-up", action: bubbleCancel})
	}
	return p
}

func nextBubbleDay(from, to time.Weekday) int { return (int(to)-int(from)+6)%7 + 1 }

func (v *mailView) startBubblePicker() tea.Cmd {
	if !v.inThread && !v.searchActive && !v.bundleActive && v.refuseForSelection() {
		return nil
	}
	if v.requests.loading {
		return nil
	}
	posting := v.actionPosting()
	if !v.inThread && v.searchActive {
		posting = v.searchList.selectedPosting()
	}
	if !v.inThread && v.bundleActive {
		posting = v.bundleList.selectedPosting()
	}
	if posting == nil || posting.ID == 0 || posting.IsBundle {
		v.notice = "Open an individual email thread to bubble it up"
		return nil
	}
	scheduled := posting.BubbleScheduled
	for _, box := range v.boxes {
		if box.ID == posting.BoxID && box.Kind == mail.KindBox && box.BoxKind == hey.BoxKindBubbleUp {
			scheduled = true
		}
	}
	picker := newBubblePicker(*posting, scheduled, time.Now)
	v.openModal(picker)
	if v.searchActive && posting.BoxID == 0 && !posting.BubbledUp && !posting.BubbleScheduled {
		return v.loadBubbleState(picker)
	}
	return nil
}

func (p *bubblePicker) handleKey(v *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if p.unknownState {
		if msg.Key().Code == tea.KeyEscape {
			if p.lookupCancel != nil {
				p.lookupCancel()
			}
			return nil, false
		}
		if !p.loading && msg.String() == "ctrl+r" {
			return v.loadBubbleState(p), true
		}
		return nil, true
	}
	if p.busy {
		return nil, true
	}
	if p.date != nil {
		switch msg.Key().Code {
		case tea.KeyEscape:
			p.date = nil
			p.problem = ""
			return nil, true
		case tea.KeyEnter:
			return p.submit(v, bubbleChoice{name: "Choose a date…", action: bubbleChooseDate}), true
		}
		return p.date.handleKey(msg), true
	}
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false
	case tea.KeyEnter:
		choice := p.choices[p.cursor]
		if choice.action == bubbleChooseDate {
			p.date = newDateTimePicker(p.now().UTC().AddDate(0, 0, 1), true)
			return p.date.focusFirst(), true
		}
		return p.submit(v, choice), true
	}
	p.cursor = stepListCursor(p.cursor, len(p.choices), msg)
	p.problem = ""
	return nil, true
}

func (p *bubblePicker) submit(v *mailView, choice bubbleChoice) tea.Cmd {
	sdk, ctx, id := v.vc.sdk, v.vc.ctx, p.postingID
	now := p.now().UTC()
	date := ""
	if p.date != nil {
		at, err := time.Parse("2006-01-02", p.date.date())
		if err != nil || at.Format("2006-01-02") != p.date.date() {
			p.problem = "Enter a date as YYYY-MM-DD"
			return nil
		}
		choice.at = at.Add(8 * time.Hour)
		if at.Format("2006-01-02") == now.Format("2006-01-02") {
			choice.slot = hey.BubbleUpLaterToday
			choice.at = at.Add(18 * time.Hour)
		} else {
			date = at.Format("2006-01-02")
		}
	} else if choice.slot != "" && now.Format("2006-01-02") != p.openedDay {
		p.problem = "The date changed. Close and reopen Bubble up."
		return nil
	}
	if !choice.at.IsZero() && !choice.at.After(now) {
		p.problem = "Choose a future date; today's slot is 18:00 UTC"
		return nil
	}
	p.busy = true
	p.problem = ""
	v.pendingMutations++
	return func() tea.Msg {
		var err error
		message := "Thread will bubble up"
		switch {
		case choice.action == bubbleCancel:
			err = sdk.Postings().CancelBubbleUp(ctx, id)
			message = "Bubble-up canceled; thread returned to the Imbox, marked seen"
		case choice.action == bubbleNow:
			err = sdk.Postings().BubbleUpNow(ctx, id)
			message = "Thread bubbled up"
		case date != "":
			err = sdk.Postings().ScheduleBubbleUp(ctx, date, id)
		default:
			err = sdk.Postings().ScheduleBubbleUpFor(ctx, choice.slot, id)
		}
		return bubbleDoneMsg{picker: p, message: message, err: err}
	}
}

func (v *mailView) bubbleDone(msg bubbleDoneMsg) tea.Cmd {
	v.finishMutation()
	p := msg.picker
	p.busy = false
	if msg.err != nil {
		p.problem = errorNotice("Could not change bubble-up", msg.err)
		return nil
	}
	if v.modal == p {
		v.modal = nil
	}
	if v.inThread && v.threadPosting.ID == p.postingID {
		v.ExitThread()
	}
	v.notice = msg.message
	// Re-read the source instead of guessing its membership and ordering after HEY
	// schedules, pops, or immediately resurfaces a thread.
	if v.seenActive {
		requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestSeen)
		v.seenNextPage = ""
		v.seenLoadingMore = false
		v.seenMoreID++
		return v.fetchSeenPostings(ctx, requestID)
	}
	if v.searchActive {
		return v.requestSearch(v.searchQuery)
	}
	if v.bundleActive {
		if v.bundleContactID != 0 {
			return v.requestContactThreads(v.bundleContactID)
		}
		return v.requestBundle(v.bundlePostingID)
	}
	return v.reloadPostings()
}

func (p *bubblePicker) handleMsg(msg tea.Msg) (tea.Cmd, bool) {
	if p.date == nil || p.busy {
		return nil, false
	}
	var cmd tea.Cmd
	p.date.dateInput, cmd = p.date.dateInput.Update(msg)
	return cmd, true
}

func (p *bubblePicker) draw(v *mailView) string {
	var body string
	if p.loading {
		body = "Loading Bubble Up…"
	} else if p.unknownState {
		body = "Press Ctrl+R to retry, or Esc to cancel."
	} else if p.date != nil {
		body = "Date (YYYY-MM-DD, on HEY's UTC calendar)\n" + p.date.view() + "\n\n08:00 UTC; today's date uses 18:00 UTC."
	} else {
		names := make([]string, len(p.choices))
		for i, choice := range p.choices {
			names[i] = choice.name
			if !choice.at.IsZero() {
				names[i] += "  · " + choice.at.Local().Format("Mon Jan 2, 15:04 MST")
			}
		}
		body = strings.Join(modalListRows(names, p.cursor, modalContentWidth(v.vc.width), max(modalContentRows(v.vc.height)-3, 1)), "\n")
	}
	if p.busy {
		body += "\n\nSaving…"
	}
	if p.problem != "" {
		body += "\n\n" + p.problem
	}
	base := v.listView()
	if v.inThread {
		base = v.topicViewport.View()
	}
	return overlayModal(base, modalFrame("Bubble up", body, v.vc.width), v.vc.width, v.vc.height)
}

func (p *bubblePicker) helpBindings() []helpBinding {
	if p.loading {
		return []helpBinding{{"esc", "cancel"}}
	}
	if p.unknownState {
		return []helpBinding{{"ctrl+r", "retry"}, {"esc", "cancel"}}
	}
	if p.busy {
		return []helpBinding{{"", "saving bubble-up…"}}
	}
	if p.date != nil {
		return []helpBinding{{"↑/↓", "change day"}, {"enter", "schedule"}, {"esc", "back"}}
	}
	return []helpBinding{{"↑/↓", "choose"}, {"enter", "apply"}, {"esc", "cancel"}}
}
