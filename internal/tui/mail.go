package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
	internalfolders "github.com/basecamp/hey-cli/internal/folders"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/threadload"
)

// --- Mail messages ---

const maxConcurrentMessageFetches = 6

type mailRequestKind int

const (
	mailRequestNone mailRequestKind = iota
	mailRequestPostings
	mailRequestTopic
	mailRequestReply
	mailRequestForward
	mailRequestSearch
	mailRequestBulkReply
)

type boxesLoadedMsg []mail.Source

type mailSourcesLoadedMsg struct {
	requestID uint64
	sources   []mail.Source
	// screenerCount is what The Screener holds, and screenerStream is the signed stream
	// name to follow to be told when that changes. HEY serves both from the one read.
	screenerCount  int
	screenerStream string
	folderErr      error
	collectionErr  error
}

type postingsLoadedMsg struct {
	requestID  uint64
	boxID      int64
	sourceKind mail.Kind
	nextPage   string
	postings   []mail.Posting
	err        error
}

// postingsAppendedMsg is the page below the one on screen, read because the reader
// scrolled towards the bottom of the list. It has its own lane so it can never be mistaken
// for the read the user is waiting on, and so it lands under the cursor rather than
// carrying it back to the top.
type postingsAppendedMsg struct {
	requestID  uint64
	boxID      int64
	sourceKind mail.Kind
	nextPage   string
	postings   []mail.Posting
	err        error
}

// postingsRefreshedMsg is a box re-read after it changed underneath the reader. It has
// its own lane so it can never be mistaken for a read the user asked for, and so a list
// that is on screen is updated in place rather than replaced.
type postingsRefreshedMsg struct {
	requestID  uint64
	boxID      int64
	sourceKind mail.Kind
	nextPage   string
	postings   []mail.Posting
	err        error
}

type topicLoadedMsg struct {
	requestID   uint64
	boxID       int64
	topicID     int64
	postingID   int64
	title       string
	entries     []mail.Entry
	attachments []messageAttachment
	images      [][]byte
	// notice says what the read did not get, or is empty; complete is whether it got
	// everything — every entry in the index, every body within the limits.
	notice   string
	complete bool
	err      error
}

type searchResultsLoadedMsg struct {
	requestID uint64
	query     string
	nextPage  int
	postings  []mail.Posting
	err       error
}

// searchResultsAppendedMsg is the page of matches below the ones on screen, read because
// the reader scrolled towards the bottom of the results. Its own lane, like a box's.
type searchResultsAppendedMsg struct {
	requestID uint64
	query     string
	nextPage  int
	postings  []mail.Posting
	err       error
}

type attachmentSavedMsg struct {
	topicID      int64
	attachmentID string
	path         string
	err          error
}

type attachmentOpenedMsg struct {
	topicID      int64
	attachmentID string
	filename     string
	err          error
}

type postingActionEffect int

const (
	postingActionNone postingActionEffect = iota
	postingActionRemove
	postingActionSeen
	postingActionUnseen
	postingActionIgnore
	postingActionStopIgnoring
)

type postingActionDoneMsg struct {
	action     string
	boxID      int64
	sourceKind mail.Kind
	postingID  int64
	effect     postingActionEffect
	err        error
}

// postingSeenMsg reports the mark-seen that opening a thread triggers on its
// own, as the web app does out of band once it has rendered the topic.
type postingSeenMsg struct {
	boxID      int64
	sourceKind mail.Kind
	postingID  int64
	err        error
}

// screenerCountLoadedMsg is the Screener's count read again, and with it the signed
// stream name HEY serves alongside it. Carrying the name is what lets a closed stream be
// opened again: this read is reachable from ctrl+r and from the doorbell, and the read
// that discovers the mail sources is not.
type screenerCountLoadedMsg struct {
	count          int
	screenerStream string
	err            error
}

type folderActionDoneMsg struct {
	action     string
	sourceID   int64
	sourceKind mail.Kind
	created    bool
	err        error
}

type collectionActionDoneMsg struct {
	action     string
	sourceID   int64
	sourceKind mail.Kind
	postingID  int64
	collection mail.Collection
	added      bool
	err        error
}

// --- Mail section view ---

type mailView struct {
	vc *viewContext

	boxes    []mail.Source
	boxIndex int

	postingPaging    listPaging
	postingList      contentList
	topicViewport    viewport.Model
	topicContent     string
	topicID          int64
	topicName        string
	entries          []mail.Entry
	attachments      []messageAttachment
	attachmentCursor int
	imageContent     string
	entryOffsets     []int // line where each message starts in the thread content
	inThread         bool
	threadNotice     string // what the open thread's read did not get; stays until the thread is left
	contentHeight    int    // the rows the section has, which the thread's notices and viewport share

	modal                  modal       // the form or picker over the list, and the only one there can be
	cover                  coverPreset // the session's cover; HEY does not serve one to read
	searchList             contentList
	searchActive           bool
	searchQuery            string
	searchNextPage         int    // the page of matches after the ones on screen, zero at the last
	searchLoadingMore      bool   // a page of matches is already on its way
	screenerCount          int    // senders waiting in The Screener
	lastBulkReplyID        int64  // delayed delivery currently available for undo
	pendingMutations       int    // writes that must finish before changing the account context
	notice                 string // one-shot confirmation shown above the posting list
	requests               requestLane[mailRequestKind]
	sourceRequestID        uint64
	folderDiscoveryErr     string
	collectionDiscoveryErr string

	liveRequestID   uint64 // identifies the only live re-read allowed to update the list
	liveRefreshDue  bool   // a re-read is already on its way
	liveUpdatesOver bool   // the changes stream closed, so the list is a snapshot again
	moreRequestID   uint64 // identifies the only page-below read allowed to grow the list
	searchMoreID    uint64 // the same, for the search results
}

func newMailView(vc *viewContext) *mailView {
	view := &mailView{
		vc:            vc,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		searchList:    contentList{hideSeenState: true},
	}
	if vc.loadCover != nil {
		view.cover = parseCoverPreset(vc.loadCover())
	}
	return view
}

func (v *mailView) Init() tea.Cmd {
	if len(v.boxes) == 0 {
		return v.requestSources()
	}
	if v.boxIndex < len(v.boxes) {
		return v.requestPostings(v.boxes[v.boxIndex])
	}
	return nil
}

func (v *mailView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case mailSourcesLoadedMsg:
		if msg.requestID != v.sourceRequestID {
			return nil, true
		}
		v.screenerCount = msg.screenerCount
		sources := msg.sources
		if msg.folderErr != nil {
			v.folderDiscoveryErr = msg.folderErr.Error()
			for _, source := range v.boxes {
				if source.Kind == mail.KindFolder {
					sources = append(sources, source)
				}
			}
		} else {
			v.folderDiscoveryErr = ""
		}
		if msg.collectionErr != nil {
			v.collectionDiscoveryErr = msg.collectionErr.Error()
			for _, source := range v.boxes {
				if source.Kind == mail.KindCollection {
					sources = append(sources, source)
				}
			}
		} else {
			v.collectionDiscoveryErr = ""
		}
		v.updateSourceDiscoveryNotice()
		return v.applySources(sources), true

	case boxesLoadedMsg:
		return v.applySources([]mail.Source(msg)), true

	case screenerCountLoadedMsg:
		if msg.err == nil {
			v.screenerCount = msg.count
		}
		return nil, true

	case postingsLoadedMsg:
		if !v.acceptsPostingsLoaded(msg) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		// Only the Imbox separates New for You from Previously Seen and marks
		// unread threads with the dot; every other source is one flat list. The
		// Imbox is also the only box HEY lets you cover.
		isImbox := v.showsImbox()
		v.postingList.hideSeenState = !isImbox
		if isImbox {
			v.postingList.setCover(v.cover)
		} else {
			v.postingList.setCover(coverNone)
		}
		v.postingList.setPostings(msg.postings)
		v.postingPaging.read(postingIDs(msg.postings), msg.nextPage)
		return v.loadMorePostings(), true

	case postingsAppendedMsg:
		if msg.requestID != v.moreRequestID || msg.boxID != v.currentBoxID() || msg.sourceKind != v.currentSourceKind() {
			return nil, true
		}
		v.postingPaging.loading = false
		if msg.err != nil {
			v.noteFailure("Could not load more mail", msg.err)
			return nil, true
		}
		v.postingList.growPostings(msg.postings)
		v.postingPaging.grew(len(msg.postings), msg.nextPage)
		return v.loadMorePostings(), true

	case mailRefreshDueMsg:
		return v.refreshBox(msg.boxID), true

	case postingsRefreshedMsg:
		if msg.requestID != v.liveRequestID || msg.boxID != v.currentBoxID() || msg.sourceKind != v.currentSourceKind() {
			return nil, true
		}
		if msg.err != nil {
			v.noteFailure("Could not refresh mail", msg.err)
			return nil, true
		}
		v.postingList.refreshHead(msg.postings, v.postingPaging.headIDs)
		v.postingPaging.refreshed(postingIDs(msg.postings), msg.nextPage)
		return nil, true

	case searchResultsLoadedMsg:
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		v.searchActive = true
		v.searchQuery = msg.query
		v.searchNextPage = msg.nextPage
		v.searchLoadingMore = false
		v.searchList.setPostings(msg.postings)
		return v.loadMoreSearchResults(), true

	case searchResultsAppendedMsg:
		if msg.requestID != v.searchMoreID || !v.searchActive || msg.query != v.searchQuery {
			return nil, true
		}
		v.searchLoadingMore = false
		if msg.err != nil {
			v.noteFailure("Could not load more results", msg.err)
			return nil, true
		}
		v.searchList.growPostings(msg.postings)
		if len(msg.postings) == 0 {
			v.searchNextPage = 0
		} else {
			v.searchNextPage = msg.nextPage
		}
		return v.loadMoreSearchResults(), true

	case topicLoadedMsg:
		if msg.boxID != v.currentBoxID() {
			return nil, true
		}
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		v.inThread = true
		v.topicID = msg.topicID
		v.topicName = msg.title
		v.entries = msg.entries
		v.attachments = msg.attachments
		v.attachmentCursor = 0
		v.threadNotice = msg.notice
		v.fitThreadViewport()
		var imageContent strings.Builder
		var uploadCmds []tea.Cmd
		for _, imgData := range msg.images {
			rendered := v.vc.imageRenderer.render(imgData, nextImageID(), v.vc.width-4)
			if rendered.content != "" {
				imageContent.WriteString("\n\n")
				imageContent.WriteString(rendered.content)
			}
			if rendered.raw != "" {
				uploadCmds = append(uploadCmds, tea.Raw(rendered.raw))
			}
		}
		v.imageContent = imageContent.String()
		v.rebuildTopicContent()
		v.topicViewport.GotoTop()
		// A thread read only in part is not marked seen by being opened: the reader has
		// not had all of it, and seen would slide it under the Imbox's cover. The seen
		// key is there for a thread they are done with anyway.
		if msg.complete {
			uploadCmds = append(uploadCmds, v.markPostingSeen(msg.boxID, msg.postingID))
		}
		return tea.Batch(uploadCmds...), true

	case replyContextLoadedMsg:
		if msg.boxID != v.currentBoxID() {
			return nil, true
		}
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		form := newReplyForm(msg, v.vc.styles)
		v.openModal(form)
		return form.init(), true

	case forwardContextLoadedMsg:
		if msg.boxID != v.currentBoxID() {
			return nil, true
		}
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		form := newForwardForm(msg, v.vc.styles)
		v.openModal(form)
		return form.init(), true

	case bulkReplyDraftLoadedMsg:
		if !v.requests.accepts(newRequestResult(msg.requestID, msg.err)) || msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			v.noteFailure("Could not preview bulk reply", msg.err)
			return nil, true
		}
		if msg.draft == nil || len(msg.draft.Entries) == 0 {
			v.notice = "No replyable threads found; nothing was sent"
			return nil, true
		}
		form := newBulkReplyForm(msg.postingIDs, msg.draft, v.vc.styles)
		v.openModal(form)
		return form.init(), true

	case bulkReplySentMsg:
		form := modalOf[*bulkReplyForm](v)
		if form == nil {
			return nil, true
		}
		if msg.err != nil {
			form.sending = false
			form.status = errorNotice("Send failed", msg.err)
			form.isError = true
			return nil, true
		}
		if msg.delivery == nil {
			form.sending = false
			form.status = "Send failed: HEY returned no delivery"
			form.isError = true
			return nil, true
		}
		v.modal = nil
		v.postingList.clearSelected()
		count := int(msg.delivery.EntriesCount)
		v.notice = fmt.Sprintf("%d bulk %s sent", count, replyNoun(count))
		v.lastBulkReplyID = 0
		if msg.delivery.Delayed {
			v.notice = fmt.Sprintf("%d bulk %s queued with undo available", count, replyNoun(count))
			if msg.delivery.Id > 0 {
				v.lastBulkReplyID = msg.delivery.Id
				v.notice += " — press ctrl+u to undo"
			}
		}
		if msg.skipped > 0 {
			v.notice += fmt.Sprintf("; %d skipped", msg.skipped)
		}
		return nil, true

	case bulkReplyUndoneMsg:
		v.finishMutation()
		if msg.id != v.lastBulkReplyID {
			return nil, true
		}
		if msg.err != nil {
			v.noteFailure("Could not undo bulk reply", msg.err)
			return nil, true
		}
		v.lastBulkReplyID = 0
		v.notice = "Bulk reply recalled"
		return nil, true

	case snippetsLoadedMsg:
		form := modalOf[*composeForm](v)
		if form == nil || form != msg.form || form.snippetRequestID != msg.requestID {
			return nil, true
		}
		if msg.err == nil {
			form.availableSnippets = msg.snippets
			form.snippetsLoaded = true
		}
		if form.snippetPicker != nil {
			form.snippetPicker.loaded(msg.snippets, msg.err)
		}
		return nil, true

	case composeSentMsg:
		form := modalOf[*composeForm](v)
		if form == nil {
			return nil, true
		}
		if msg.err != nil {
			form.sending = false
			form.setStatus(errorNotice("Send failed", msg.err), true)
			return nil, true
		}
		v.modal = nil
		v.notice = msg.label
		return nil, true

	case attachmentSavedMsg:
		if !v.currentAttachmentAction(msg.topicID, msg.attachmentID) {
			return nil, true
		}
		if msg.err != nil {
			saveErr := apierr.AsError(msg.err)
			if saveErr.Code == "usage" && strings.HasPrefix(saveErr.Message, "destination already exists:") {
				v.notice = "Attachment already exists: " + terminal.SanitizeLine(msg.path)
			} else {
				v.noteFailure("Could not save attachment", msg.err)
			}
			return nil, true
		}
		v.notice = "Saved attachment to " + terminal.SanitizeLine(msg.path)
		return nil, true

	case attachmentOpenedMsg:
		if !v.currentAttachmentAction(msg.topicID, msg.attachmentID) {
			return nil, true
		}
		if msg.err != nil {
			v.noteFailure("Could not open attachment", msg.err)
			return nil, true
		}
		v.notice = "Opened attachment " + terminal.SanitizeLine(msg.filename)
		return nil, true

	case postingActionDoneMsg:
		v.finishMutation()
		if msg.boxID != v.currentBoxID() || (msg.sourceKind != "" && msg.sourceKind != v.currentSourceKind()) {
			return nil, true
		}
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.notice = msg.action
		idx := v.postingIndex(msg.postingID)
		if idx >= 0 {
			switch msg.effect {
			case postingActionNone:
			case postingActionRemove:
				v.removePostingAt(idx)
			case postingActionSeen:
				v.postingList.markSeen(idx)
			case postingActionUnseen:
				v.postingList.markUnseen(idx)
			case postingActionIgnore:
				v.postingList.postings[idx].Muted = true
			case postingActionStopIgnoring:
				v.postingList.postings[idx].Muted = false
			}
		}
		if v.requests.kind == mailRequestPostings {
			if source := v.currentSource(); source != nil {
				return v.requestPostings(*source), true
			}
		}
		// A thread leaving the list can uncover the bottom of it, so what is below comes up
		// to fill the gap rather than leaving a short list with more waiting behind it.
		return v.loadMorePostings(), true

	case postingSeenMsg:
		v.finishMutation()
		if msg.err != nil {
			v.noteFailure("Could not mark thread as seen", msg.err)
			return nil, true
		}
		if msg.boxID == v.currentBoxID() && msg.sourceKind == v.currentSourceKind() {
			if idx := v.postingIndex(msg.postingID); idx >= 0 {
				v.postingList.markSeen(idx)
			}
		}
		return nil, true

	case folderActionDoneMsg:
		v.finishMutation()
		if msg.err != nil {
			if msg.sourceID == v.currentBoxID() && msg.sourceKind == v.currentSourceKind() {
				v.noteFailure("Could not update labels", msg.err)
			}
			return nil, true
		}
		if msg.sourceID == v.currentBoxID() && msg.sourceKind == v.currentSourceKind() {
			v.notice = msg.action
		}
		if msg.created {
			return v.requestSources(), true
		}
		if source := v.currentSource(); source != nil && msg.sourceID == source.ID && msg.sourceKind == source.Kind {
			return v.requestPostings(*source), true
		}
		return nil, true

	case collectionActionDoneMsg:
		v.finishMutation()
		if msg.sourceID != v.currentBoxID() || msg.sourceKind != v.currentSourceKind() {
			return nil, true
		}
		if msg.err != nil {
			v.notice = terminal.SanitizeLine(errorNotice("Could not update collections", msg.err))
			return nil, true
		}
		v.notice = msg.action
		if index := v.postingIndex(msg.postingID); index >= 0 {
			v.updatePostingCollection(index, msg.collection, msg.added)
			if !msg.added && msg.sourceKind == mail.KindCollection && msg.collection.ID == msg.sourceID {
				v.removePostingAt(index)
			}
		}
		if msg.sourceKind == mail.KindCollection && msg.collection.ID == msg.sourceID {
			if source := v.currentSource(); source != nil {
				return v.requestPostings(*source), true
			}
		}
		return nil, true
	}

	// Cursor blinks and other component messages go to the open modal. A form owns
	// the message while it is open, whether or not it yields a cmd; a picker has no
	// use for one and leaves it to whatever is on screen behind it.
	if v.modal != nil {
		if cmd, taken := v.modal.handleMsg(msg); taken {
			return cmd, true
		}
	}

	// Pass through to viewport if in thread
	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd, cmd != nil
	}

	return nil, false
}

func (v *mailView) View() string {
	if v.modal != nil {
		return v.modal.draw(v)
	}
	if v.inThread {
		v.fitThreadViewport()
		var lines []string
		for _, notice := range v.threadNotices() {
			lines = append(lines, v.vc.styles.title.Render(notice))
		}
		return strings.Join(append(lines, v.topicViewport.View()), "\n")
	}
	if v.searchActive {
		if v.notice != "" {
			return v.vc.styles.title.Render(v.notice) + "\n" + v.searchList.view()
		}
		return v.searchList.view()
	}
	return v.listView()
}

// listView is the posting list and whatever stands above it, which is what an overlay
// modal draws itself over.
func (v *mailView) listView() string {
	return v.listHeader() + v.postingList.view()
}

// openModal puts a form or a picker over the list, sized to the screen it opens on.
func (v *mailView) openModal(open modal) {
	v.modal = open
	open.resize(v.vc.width, v.vc.height)
}

// listHeader carries the one-shot notice, the standing word that the list has stopped
// following the server, and the Screener's standing invitation above the posting list.
func (v *mailView) listHeader() string {
	var lines []string
	if v.notice != "" {
		lines = append(lines, v.vc.styles.title.Render(v.notice))
	}
	if v.liveUpdatesOver && !isOrganizedMailSource(v.currentSourceKind()) {
		lines = append(lines, v.vc.styles.title.Render("Not live any more — press ctrl+r to reload"))
	}
	if hint := v.screenerHint(); hint != "" {
		lines = append(lines, centerText(v.vc.styles.pill.Render(hint), v.vc.width), "")
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func (v *mailView) screenerHint() string {
	if v.screenerCount <= 0 {
		return ""
	}
	return fmt.Sprintf("Screen %d %s · ctrl+s", v.screenerCount, firstTimeSenderNoun(v.screenerCount))
}

func firstTimeSenderNoun(count int) string {
	if count == 1 {
		return "first-time sender"
	}
	return "first-time senders"
}

func (v *mailView) updateSourceDiscoveryNotice() {
	switch {
	case v.folderDiscoveryErr != "" && v.collectionDiscoveryErr != "":
		v.notice = "Could not load labels or collections — press b or n to retry"
	case v.folderDiscoveryErr != "":
		v.notice = "Could not load labels — press b to retry"
	case v.collectionDiscoveryErr != "":
		v.notice = "Could not load collections — press n to retry"
	case v.notice == "Retrying labels…" || v.notice == "Retrying collections…":
		v.notice = ""
	}
}

// CapturingInput reports whether a form or picker is open and wants every key.
func (v *mailView) CapturingInput() bool {
	return v.modal != nil
}

func (v *mailView) AccountSwitchBlocked() bool {
	return v.pendingMutations > 0
}

func (v *mailView) HelpBindings() []helpBinding {
	if v.modal != nil {
		return v.modal.helpBindings()
	}
	if v.inThread {
		bindings := []helpBinding{{"r", "reply"}, {"f", "forward"}}
		if len(v.entries) > 1 {
			bindings = append(bindings, helpBinding{"j/k", "next/previous message"})
		}
		if len(v.attachments) > 0 {
			bindings = append(bindings,
				helpBinding{"[", "previous attachment"},
				helpBinding{"]", "next attachment"},
				helpBinding{"s", "save attachment"},
				helpBinding{"o", "open attachment"},
			)
		}
		return bindings
	}
	if v.searchActive {
		return []helpBinding{{"enter", "open"}, {"/", "new search"}}
	}
	ignoreBinding := helpBinding{"-", "ignore"}
	if selected := v.postingList.selectedPosting(); selected != nil && selected.Muted {
		ignoreBinding = helpBinding{"+", "stop ignoring"}
	}
	folderBinding := helpBinding{"b", "labels"}
	if v.folderDiscoveryErr != "" {
		folderBinding = helpBinding{"b", "retry labels"}
	}
	collectionBinding := helpBinding{"n", "collections"}
	if v.collectionDiscoveryErr != "" {
		collectionBinding = helpBinding{"n", "retry collections"}
	}
	bindings := []helpBinding{
		{"/", "search"},
		{"ctrl+s", "screener"},
		{"c", "compose"},
		{"space", "select"},
		{"ctrl+b", "bulk reply"},
		{"r", "reply"},
		{"f", "forward"},
		{"v", "move"},
		folderBinding,
		collectionBinding,
		{"e", "seen"},
		{"u", "unseen"},
		{"i", "imbox"},
		{"l", "reply later"},
		{"a", "set aside"},
		{"d", "feed"},
	}
	bindings = append(bindings,
		helpBinding{"p", "paper trail"},
		helpBinding{"t", "trash"},
		helpBinding{"!", "spam"},
		ignoreBinding,
		helpBinding{"ctrl+r", "reload"},
	)
	if v.postingList.cover != coverNone {
		peek := helpBinding{"x", "peek under cover"}
		if v.postingList.coverPeeked {
			peek = helpBinding{"x", "cover"}
		}
		bindings = append(bindings, peek)
	}
	if v.showsImbox() {
		bindings = append(bindings, helpBinding{"ctrl+v", "cover art"})
	}
	if v.lastBulkReplyID != 0 {
		bindings = append(bindings, helpBinding{"ctrl+u", "undo bulk reply"})
	}
	return modifiersLast(bindings)
}

func (v *mailView) SubnavItems() ([]navItem, int, string, bool) {
	if v.searchActive || v.searchOpen() {
		label := "Search"
		if v.searchQuery != "" {
			label = "Search: " + v.searchQuery
		}
		if v.searchLoadingMore {
			label += " · loading more…"
		}
		return nil, 0, label, true
	}
	label := "Mail"
	if v.boxIndex >= 0 && v.boxIndex < len(v.boxes) {
		label = terminal.SanitizeLine(v.boxes[v.boxIndex].Name)
		if v.postingPaging.loading {
			label += " · loading more…"
		}
	}

	// Labels and Collections each use one tab whose modal chooses the source.
	tabIndexes := v.tabBoxIndexes()
	boxes := make([]mail.Source, len(tabIndexes))
	selected := 0
	for i, boxIndex := range tabIndexes {
		boxes[i] = v.boxes[boxIndex]
		if boxIndex == v.boxIndex {
			selected = i
		}
	}
	items := boxNavItems(boxes)
	if v.hasLabels() {
		items = append(items, navItem{label: "Labels"})
		if v.currentSourceKind() == mail.KindFolder {
			selected = len(items) - 1
		}
	}
	if v.hasCollections() {
		items = append(items, navItem{shortcut: "K", label: "Collections"})
		if v.currentSourceKind() == mail.KindCollection {
			selected = len(items) - 1
		}
	}
	return items, selected, label, true
}

// tabBoxIndexes returns the sources shown as their own tabs.
func (v *mailView) tabBoxIndexes() []int {
	indexes := make([]int, 0, len(v.boxes))
	for i, source := range v.boxes {
		if !isOrganizedMailSource(source.Kind) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (v *mailView) hasLabels() bool {
	return v.hasSourceKind(mail.KindFolder)
}

func (v *mailView) hasCollections() bool {
	return v.hasSourceKind(mail.KindCollection)
}

func (v *mailView) hasSourceKind(kind mail.Kind) bool {
	for _, source := range v.boxes {
		if source.Kind == kind {
			return true
		}
	}
	return false
}

// searchOpen reports whether the search form is up, which is what puts the subnav on
// the search rather than on a box.
func (v *mailView) searchOpen() bool {
	return modalOf[*mailSearchForm](v) != nil
}

func (v *mailView) openLabels() {
	v.openModal(newLabelPicker(v.boxes, v.boxIndex))
}

func (v *mailView) openCollections() {
	v.openModal(newCollectionNavPicker(v.boxes, v.boxIndex))
}

func (v *mailView) SubnavLeft() tea.Cmd {
	if v.searchActive || v.searchOpen() {
		return nil
	}
	tabIndexes := v.tabBoxIndexes()
	switch v.currentSourceKind() {
	case mail.KindCollection:
		if v.hasLabels() {
			v.openLabels()
			return nil
		}
		if len(tabIndexes) > 0 {
			return v.switchBox(tabIndexes[len(tabIndexes)-1])
		}
		return nil
	case mail.KindFolder:
		if len(tabIndexes) > 0 {
			return v.switchBox(tabIndexes[len(tabIndexes)-1])
		}
		return nil
	case mail.KindBox:
		for i, boxIndex := range tabIndexes {
			if boxIndex == v.boxIndex && i > 0 {
				return v.switchBox(tabIndexes[i-1])
			}
		}
	}
	return nil
}

func (v *mailView) SubnavRight() tea.Cmd {
	if v.searchActive || v.searchOpen() {
		return nil
	}
	switch v.currentSourceKind() {
	case mail.KindFolder:
		if v.hasCollections() {
			v.openCollections()
		} else {
			v.openLabels()
		}
		return nil
	case mail.KindCollection:
		v.openCollections()
		return nil
	case mail.KindBox:
		tabIndexes := v.tabBoxIndexes()
		for i, boxIndex := range tabIndexes {
			if boxIndex != v.boxIndex {
				continue
			}
			if i+1 < len(tabIndexes) {
				return v.switchBox(tabIndexes[i+1])
			}
			if v.hasLabels() {
				v.openLabels()
			} else if v.hasCollections() {
				v.openCollections()
			}
			return nil
		}
	}
	return nil
}

func (v *mailView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	v.notice = ""

	// The open modal has every key. Escaping out of one and committing a choice in one
	// both end here, which is why a modal says it is finished rather than closing itself.
	if v.modal != nil {
		cmd, open := v.modal.handleKey(v, msg)
		if !open {
			v.modal = nil
		}
		return cmd
	}

	if v.inThread {
		switch msg.String() {
		case "r", "R":
			if v.topicID != 0 {
				return v.loadReplyContext(v.topicID, v.topicName)
			}
		case "f", "F":
			if v.topicID != 0 {
				return v.loadForwardContext(v.topicID, v.topicName)
			}
		case "[":
			v.moveAttachmentCursor(-1)
			return nil
		case "]":
			v.moveAttachmentCursor(1)
			return nil
		case "s":
			return v.saveSelectedAttachment()
		case "o":
			return v.openSelectedAttachment()
		case "j":
			if len(v.entryOffsets) > 1 {
				v.jumpEntry(1)
				return nil
			}
		case "k":
			if len(v.entryOffsets) > 1 {
				v.jumpEntry(-1)
				return nil
			}
		}
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd
	}

	if v.searchActive {
		switch msg.Key().Code {
		case tea.KeyUp:
			v.searchList.moveUp()
		case tea.KeyDown:
			v.searchList.moveDown()
			return v.loadMoreSearchResults()
		case tea.KeyEnter:
			return v.openSelected()
		default:
			switch msg.String() {
			case "/", "s", "S":
				return v.startSearch()
			}
		}
		return nil
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		v.postingList.moveUp()
	case tea.KeyDown:
		v.postingList.moveDown()
		return v.loadMorePostings()
	case tea.KeyEnter:
		return v.openSelected()
	default:
		switch msg.String() {
		case "/", "s", "S":
			return v.startSearch()
		case "c":
			return v.startCompose()
		case " ", "space":
			v.postingList.toggleSelected()
			return nil
		case "ctrl+b":
			return v.startBulkReply()
		case "ctrl+u":
			return v.undoBulkReply()
		case "v", "V":
			v.startMove()
			return nil
		case "b", "B":
			return v.startFolderPicker()
		case "n", "N":
			return v.startCollectionPicker()
		case "x":
			v.postingList.toggleCoverPeek()
			return nil
		case "ctrl+v":
			return v.startCoverPicker()
		case "ctrl+r":
			return v.reloadPostings()
		default:
			return v.handlePostingAction(msg.String())
		}
	}
	return nil
}

func (v *mailView) InThread() bool { return v.inThread || v.searchActive }

func (v *mailView) ExitDetail(key string) {
	if key == "q" && v.searchActive && !v.inThread && (v.requests.kind == mailRequestTopic || v.requests.kind == mailRequestSearch) {
		v.requests.cancel()
		v.clearSearch()
		return
	}
	v.ExitThread()
}

func (v *mailView) ExitThread() {
	if v.searchActive && !v.inThread && (v.requests.kind == mailRequestTopic || v.requests.kind == mailRequestSearch) {
		v.requests.cancel()
		return
	}
	if v.inThread {
		v.inThread = false
		v.threadNotice = ""
		v.modal = nil
		v.requests.cancel()
		return
	}
	v.clearSearch()
	v.requests.cancel()
}

func (v *mailView) clearSearch() {
	v.searchActive = false
	v.searchQuery = ""
	v.notice = ""
	v.searchNextPage = 0
	v.searchLoadingMore = false
	v.searchMoreID++
	v.searchList.setPostings(nil)
	v.modal = nil
}

func (v *mailView) CancelPendingDetail() bool {
	if v.requests.kind != mailRequestTopic && v.requests.kind != mailRequestReply && v.requests.kind != mailRequestForward && v.requests.kind != mailRequestSearch && v.requests.kind != mailRequestBulkReply {
		return false
	}
	v.requests.cancel()
	return true
}

func (v *mailView) Loading() bool { return v.requests.loading }

// Restyle rebuilds the cached thread content and hands the new styles to any open
// form. The Kitty image placeholders in imageContent encode image IDs as colors, so
// they are reused as-is rather than recolored.
func (v *mailView) Restyle() {
	if v.inThread {
		offset := v.topicViewport.YOffset()
		v.rebuildTopicContent()
		v.topicViewport.SetYOffset(offset)
	}
	if v.modal != nil {
		v.modal.restyle(v.vc.styles)
	}
}

func (v *mailView) Resize(width, height int) {
	if v.modal != nil {
		v.modal.resize(width, height)
	}
	v.postingList.setSize(width, height)
	v.searchList.setSize(width, height)
	v.topicViewport.SetWidth(width)
	v.contentHeight = height
	v.fitThreadViewport()
}

// threadNotices is what is shown above an open thread's viewport: the partial-read
// notice for as long as the thread is open, and the one-shot notice while it is up. Each
// is one row, truncated to the width, so the rows they take can be counted, and the
// thread itself keeps at least one: in a section too short for both, a notice gives
// way rather than pushing the viewport out.
func (v *mailView) threadNotices() []string {
	var notices []string
	for _, notice := range []string{v.threadNotice, v.notice} {
		if notice != "" {
			notices = append(notices, truncateToWidth(notice, max(v.vc.width, 4)))
		}
	}
	if room := max(v.contentHeight-1, 0); len(notices) > room {
		notices = notices[:room]
	}
	return notices
}

// fitThreadViewport gives the thread's viewport the rows its notices leave, so the
// section never draws more rows than it has. The one-shot notice comes and goes from
// dozens of sites, so the fit is also checked where the thread is drawn.
func (v *mailView) fitThreadViewport() {
	height := max(v.contentHeight-len(v.threadNotices()), 1)
	if v.topicViewport.Height() != height {
		v.topicViewport.SetHeight(height)
	}
}

// handleBoxShortcut handles number-key shortcuts for switching boxes.
func (v *mailView) handleBoxShortcut(key string) tea.Cmd {
	if v.CapturingInput() {
		return nil
	}
	switch key {
	case "K":
		if v.hasCollections() {
			v.openCollections()
			return func() tea.Msg { return nil }
		}
	}
	return v.switchBox(boxForShortcut(key, v.boxes))
}

func (v *mailView) switchBox(index int) tea.Cmd {
	if index < 0 || index >= len(v.boxes) || index == v.boxIndex {
		return nil
	}
	v.inThread = false
	v.threadNotice = ""
	v.clearSearch()
	v.requests.cancel()
	v.notice = ""
	v.postingList.setPostings(nil)
	v.boxIndex = index
	return v.requestPostings(v.boxes[index])
}

func (v *mailView) currentSource() *mail.Source {
	if v.boxIndex < 0 || v.boxIndex >= len(v.boxes) {
		return nil
	}
	return &v.boxes[v.boxIndex]
}

func (v *mailView) currentBoxID() int64 {
	if source := v.currentSource(); source != nil {
		return source.ID
	}
	return 0
}

// showsImbox reports whether the source on screen is the Imbox: the only box HEY splits
// New for You from Previously Seen in, and the only one it lets you cover. A source is
// asked what it is rather than what it is called — a label named "Imbox" is a label.
func (v *mailView) showsImbox() bool {
	source := v.currentSource()
	return source != nil && source.Coverable()
}

func (v *mailView) currentSourceKind() mail.Kind {
	if source := v.currentSource(); source != nil {
		return source.Kind
	}
	return ""
}

func (v *mailView) currentSourceIdentity() (int64, mail.Kind) {
	if source := v.currentSource(); source != nil {
		return source.ID, source.Kind
	}
	return 0, ""
}

func sourceIndex(sources []mail.Source, id int64, kind mail.Kind) int {
	for i, source := range sources {
		if source.ID == id && source.Kind == kind {
			return i
		}
	}
	return 0
}

func (v *mailView) applySources(sources []mail.Source) tea.Cmd {
	currentID, currentKind := v.currentSourceIdentity()
	v.boxes = orderBoxes(sources)
	v.requests.loading = false
	if len(v.boxes) == 0 {
		return nil
	}
	v.boxIndex = sourceIndex(v.boxes, currentID, currentKind)
	return v.requestPostings(v.boxes[v.boxIndex])
}

// requestSources reads the boxes, labels and collections. It is not the lane's read —
// there is nothing to cancel and nothing else it could be confused with — but it is the
// same spinner, so it turns the lane's light on and applySources turns it off again.
func (v *mailView) requestSources() tea.Cmd {
	v.sourceRequestID++
	v.requests.loading = true
	return v.fetchSources(v.sourceRequestID)
}

func (v *mailView) acceptsPostingsLoaded(msg postingsLoadedMsg) bool {
	return v.requests.accepts(newRequestResult(msg.requestID, msg.err)) &&
		msg.boxID == v.currentBoxID() &&
		(msg.sourceKind == "" || msg.sourceKind == v.currentSourceKind())
}

// requestPostings reads a source from its top page. Every list starts there and grows
// downwards from it, so a read the user asked for is also what puts the list back to the
// depth it opens at.
func (v *mailView) requestPostings(source mail.Source) tea.Cmd {
	v.postingPaging.reset()
	v.moreRequestID++
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestPostings)
	return v.fetchPostings(ctx, requestID, source, "")
}

// loadMorePostings reads the page below the one the reader has scrolled to, or the one
// below a list they can already see the end of. One page is asked for at a time, in its own
// lane and on the view's own context: the reader is still looking at what is there, so this
// must not cancel or be cancelled by the read they are waiting on.
func (v *mailView) loadMorePostings() tea.Cmd {
	source := v.currentSource()
	if source == nil || v.postingPaging.loading || !v.postingPaging.hasMore() {
		return nil
	}
	if v.postingList.hasRowsBelow() && len(v.postingList.postings)-v.postingList.cursor > loadMoreThreshold {
		return nil
	}

	v.postingPaging.loading = true
	v.moreRequestID++
	return v.fetchMorePostings(v.vc.ctx, v.moreRequestID, *source, v.postingPaging.nextPage)
}

// reloadPostings reads the box on screen again, on the user's say-so. It is how a list
// that stopped being live is caught up, and how anything else is put right.
func (v *mailView) reloadPostings() tea.Cmd {
	source := v.currentSource()
	if source == nil {
		return nil
	}
	v.liveUpdatesOver = false
	return v.requestPostings(*source)
}

// boxChanged is the doorbell: something arrived in, left, or was changed in a box. Only
// the box on screen is worth re-reading, and one re-read is armed at a time — a delivery
// rings once per posting, and a catch-up after a reconnect rings for everything at once.
func (v *mailView) boxChanged(boxID int64) tea.Cmd {
	if v.liveRefreshDue || !v.showsBox(boxID) {
		return nil
	}
	v.liveRefreshDue = true
	return refreshMailLaterCmd(boxID, liveRefreshDelay)
}

// refreshBox re-reads the box, unless something is open over the list — a form, a picker,
// a write that hasn't landed. Then the change waits: it is the reader's place in the list
// that a re-read would disturb, and holding onto it costs a timer rather than the change.
func (v *mailView) refreshBox(boxID int64) tea.Cmd {
	v.liveRefreshDue = false
	if !v.showsBox(boxID) {
		return nil
	}
	if v.CapturingInput() || v.pendingMutations > 0 {
		v.liveRefreshDue = true
		return refreshMailLaterCmd(boxID, liveRetryDelay)
	}

	v.liveRequestID++
	return v.fetchBoxRefresh(v.vc.ctx, v.liveRequestID, *v.currentSource())
}

// showsBox reports whether the list on screen is that box's. Labels and collections page
// through their own feeds, and a change that names no box stands for every actual box.
func (v *mailView) showsBox(boxID int64) bool {
	source := v.currentSource()
	if source == nil || isOrganizedMailSource(source.Kind) {
		return false
	}
	return boxID == AnyBoxChanged || boxID == source.ID
}

// liveUpdatesStopped stands above the list until the box is read again: what was live is
// a snapshot now, and a reader has no other way of telling.
func (v *mailView) liveUpdatesStopped() {
	v.liveUpdatesOver = true
	v.liveRefreshDue = false
}

// liveUpdatesUnavailable is the other way it goes wrong: there was never a stream to
// begin with, which is worth saying once rather than standing over the list forever.
func (v *mailView) liveUpdatesUnavailable(err error) {
	v.noteFailure("Live updates unavailable", err)
}

// screenerUpdatesUnavailable is said in the mail list because that is where The Screener
// announces itself — the count above the threads is what stops keeping up.
func (v *mailView) screenerUpdatesUnavailable(err error) {
	v.noteFailure("The Screener won't update live", err)
}

// screenerUpdatesStopped is said once rather than standing over the list: opening The
// Screener reads the count again anyway, and so does the next look at the labels.
func (v *mailView) screenerUpdatesStopped() {
	v.notice = "The Screener stopped updating live"
}

// noteFailure keeps the reason to a line, in the words the CLI would use for it.
// `hey watch` is where the whole of it is.
func (v *mailView) noteFailure(what string, err error) {
	v.notice = truncateToWidth(errorNotice(what, err), max(v.vc.width-2, 40))
}

func (v *mailView) startSearch() tea.Cmd {
	if v.requests.loading || len(v.boxes) == 0 {
		return nil
	}
	form := newMailSearchForm(v.searchQuery, v.vc.styles)
	v.openModal(form)
	return form.init()
}

// requestSearch runs a search from its first page. Results grow downwards from there, the
// same way a box does.
func (v *mailView) requestSearch(query string) tea.Cmd {
	v.searchNextPage = 0
	v.searchLoadingMore = false
	v.searchMoreID++
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestSearch)
	return v.fetchSearchResults(ctx, requestID, query, 1)
}

// loadMoreSearchResults reads the page of matches below the ones the reader has scrolled
// to, or below results they can already see the end of.
func (v *mailView) loadMoreSearchResults() tea.Cmd {
	if !v.searchActive || v.searchLoadingMore || v.searchNextPage == 0 {
		return nil
	}
	if v.searchList.hasRowsBelow() && len(v.searchList.postings)-v.searchList.cursor > loadMoreThreshold {
		return nil
	}

	v.searchLoadingMore = true
	v.searchMoreID++
	return v.fetchMoreSearchResults(v.vc.ctx, v.searchMoreID, v.searchQuery, v.searchNextPage)
}

func (v *mailView) requestTopic(boxID, topicID, postingID int64, title string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestTopic)
	return v.fetchTopic(ctx, requestID, boxID, topicID, postingID, title)
}

func (v *mailView) postingIndex(postingID int64) int {
	for i := range v.postingList.postings {
		if v.postingList.postings[i].ID == postingID {
			return i
		}
	}
	return -1
}

func (v *mailView) removePostingAt(index int) {
	if index < 0 || index >= len(v.postingList.postings) {
		return
	}
	v.postingList.postings = append(v.postingList.postings[:index], v.postingList.postings[index+1:]...)
	if v.postingList.cursor > index {
		v.postingList.cursor--
	}
	v.postingList.settleCover()
}

func (v *mailView) moveAttachmentCursor(delta int) {
	if len(v.attachments) == 0 {
		return
	}
	v.attachmentCursor = max(0, min(v.attachmentCursor+delta, len(v.attachments)-1))
	v.rebuildTopicContent()
	if marker := strings.Index(v.topicContent, "│ › "); marker >= 0 {
		v.topicViewport.EnsureVisible(strings.Count(v.topicContent[:marker], "\n"), 0, 0)
	}
}

func (v *mailView) saveSelectedAttachment() tea.Cmd {
	attachment := v.selectedAttachment()
	if attachment == nil || v.vc.saveAttachment == nil {
		return nil
	}
	topicID := v.topicID
	return func() tea.Msg {
		destination, err := attachmentfiles.Destination("", attachment.Filename)
		if err == nil {
			_, err = v.vc.saveAttachment(v.vc.ctx, destination, attachment.URL, false)
		}
		return attachmentSavedMsg{topicID: topicID, attachmentID: attachment.ID, path: destination, err: err}
	}
}

func (v *mailView) openSelectedAttachment() tea.Cmd {
	attachment := v.selectedAttachment()
	if attachment == nil || v.vc.saveAttachment == nil || v.vc.openAttachment == nil || v.vc.newAttachmentTempDir == nil {
		return nil
	}
	topicID := v.topicID
	return func() tea.Msg {
		directory, err := v.vc.newAttachmentTempDir()
		if err != nil {
			return attachmentOpenedMsg{topicID: topicID, attachmentID: attachment.ID, filename: attachment.Filename, err: err}
		}
		destination, err := attachmentfiles.Destination(directory, attachment.Filename)
		if err == nil {
			_, err = v.vc.saveAttachment(v.vc.ctx, destination, attachment.URL, false)
		}
		if err == nil {
			err = v.vc.openAttachment(destination)
		}
		if err != nil {
			_ = os.RemoveAll(directory)
		}
		return attachmentOpenedMsg{topicID: topicID, attachmentID: attachment.ID, filename: attachment.Filename, err: err}
	}
}

func (v *mailView) selectedAttachment() *messageAttachment {
	if v.attachmentCursor < 0 || v.attachmentCursor >= len(v.attachments) {
		return nil
	}
	return &v.attachments[v.attachmentCursor]
}

func (v *mailView) currentAttachmentAction(topicID int64, attachmentID string) bool {
	if !v.inThread || topicID != v.topicID {
		return false
	}
	for _, attachment := range v.attachments {
		if attachment.ID == attachmentID {
			return true
		}
	}
	return false
}

// jumpEntry scrolls the thread to the next or previous message header.
func (v *mailView) jumpEntry(delta int) {
	if len(v.entryOffsets) == 0 {
		return
	}
	current := v.topicViewport.YOffset()
	if delta > 0 {
		for _, offset := range v.entryOffsets {
			if offset > current {
				v.topicViewport.SetYOffset(offset)
				return
			}
		}
		return
	}
	for i := len(v.entryOffsets) - 1; i >= 0; i-- {
		if v.entryOffsets[i] < current {
			v.topicViewport.SetYOffset(v.entryOffsets[i])
			return
		}
	}
	v.topicViewport.GotoTop()
}

func (v *mailView) rebuildTopicContent() {
	rendered, offsets := v.renderEntries(v.entries)
	v.topicContent = rendered + v.imageContent
	v.entryOffsets = offsets
	v.topicViewport.SetContent(v.topicContent)
}

func (v *mailView) openSelected() tea.Cmd {
	selected := v.postingList.selectedPosting()
	if v.searchActive {
		selected = v.searchList.selectedPosting()
	}
	if selected == nil {
		return nil
	}
	topicID := selected.TopicID
	if topicID == 0 {
		topicID = selected.ID
	}
	title := selected.Summary
	if v.searchActive {
		title = selected.Name
	}
	return v.requestTopic(v.currentBoxID(), topicID, selected.ID, title)
}

// markPostingSeen marks a thread as seen once it has been opened, the way the
// web app beacons an observation after it renders a topic. A thread that is
// already seen costs no request, and a bubbled up one is left alone: reading it
// does not dismiss it, only the seen key does. The server draws the same line
// in Posting#observed.
func (v *mailView) markPostingSeen(boxID, postingID int64) tea.Cmd {
	opened := v.openedPosting(postingID)
	if opened == nil || opened.Seen || opened.BubbledUp {
		return nil
	}
	sourceKind := v.currentSourceKind()
	v.pendingMutations++
	return func() tea.Msg {
		err := v.vc.sdk.Postings().MarkSeen(v.vc.ctx, []int64{postingID})
		return postingSeenMsg{boxID: boxID, sourceKind: sourceKind, postingID: postingID, err: err}
	}
}

func (v *mailView) openedPosting(postingID int64) *mail.Posting {
	list := &v.postingList
	if v.searchActive {
		list = &v.searchList
	}
	for i := range list.postings {
		if list.postings[i].ID == postingID {
			return &list.postings[i]
		}
	}
	return nil
}

// --- Posting actions ---

func (v *mailView) startMove() {
	selected := v.postingList.selectedPosting()
	currentSource := v.currentSource()
	if selected == nil || currentSource == nil {
		return
	}
	picker := newMovePicker(*selected, v.boxes, *currentSource)
	if len(picker.destinations) == 0 {
		v.notice = "No other boxes available"
		return
	}
	v.openModal(picker)
}

// startCoverPicker opens the cover picker. Only the Imbox can be covered, which
// is haystack's rule, so anywhere else the key says why rather than doing nothing.
func (v *mailView) startCoverPicker() tea.Cmd {
	if !v.showsImbox() {
		v.notice = "Only the Imbox can be covered"
		return nil
	}
	v.openModal(newCoverPicker(v.cover))
	return nil
}

// applyCover puts the chosen art over Previously Seen and remembers it. The cover is
// on screen either way; failing to write it only costs the choice on the next run,
// which is worth a notice and not a refusal to change the cover.
func (v *mailView) applyCover(preset coverPreset) {
	v.cover = preset
	v.postingList.setCover(v.cover)
	if v.vc.saveCover != nil {
		if err := v.vc.saveCover(string(v.cover)); err != nil {
			v.notice = "Could not remember the cover: " + err.Error()
		}
	}
}

func (v *mailView) startFolderPicker() tea.Cmd {
	if v.folderDiscoveryErr != "" {
		v.notice = "Retrying labels…"
		return v.requestSources()
	}
	selected := v.postingList.selectedPosting()
	if selected == nil {
		return nil
	}
	v.openModal(newFolderPicker(*selected, v.boxes))
	return nil
}

func (v *mailView) filePosting(postingID, folderID int64, folderName string) tea.Cmd {
	return v.doFolderAction("Label "+terminal.SanitizeLine(folderName)+" added", false, func() error {
		return v.vc.sdk.Postings().File(v.vc.ctx, folderID, postingID)
	})
}

func (v *mailView) createFolderForPosting(postingID int64, folderName string) tea.Cmd {
	return v.doFolderAction("Label "+terminal.SanitizeLine(folderName)+" created", true, func() error {
		return v.vc.sdk.Postings().CreateFolder(v.vc.ctx, folderName, postingID)
	})
}

func (v *mailView) unfilePosting(postingID, folderID int64, folderName string) tea.Cmd {
	label := "All labels removed"
	if folderID != 0 {
		label = "Label " + terminal.SanitizeLine(folderName) + " removed"
	}
	return v.doFolderAction(label, false, func() error {
		return v.vc.sdk.Postings().Unfile(v.vc.ctx, folderID, postingID)
	})
}

func (v *mailView) doFolderAction(label string, created bool, fn func() error) tea.Cmd {
	sourceID, sourceKind := v.currentSourceIdentity()
	v.pendingMutations++
	return func() tea.Msg {
		return folderActionDoneMsg{
			action:     label,
			sourceID:   sourceID,
			sourceKind: sourceKind,
			created:    created,
			err:        fn(),
		}
	}
}

func (v *mailView) startCollectionPicker() tea.Cmd {
	if v.collectionDiscoveryErr != "" {
		v.notice = "Retrying collections…"
		return v.requestSources()
	}
	selected := v.postingList.selectedPosting()
	if selected == nil {
		return nil
	}
	if selected.TopicID == 0 {
		v.notice = "This item does not identify an email thread"
		return nil
	}
	picker := newCollectionMembershipPicker(*selected, v.boxes)
	if len(picker.collections) == 0 {
		v.notice = "No collections available"
		return nil
	}
	v.openModal(picker)
	return nil
}

func (v *mailView) addPostingToCollection(postingID, topicID int64, collection mail.Collection) tea.Cmd {
	label := "Added to collection " + terminal.SanitizeLine(collection.Name)
	return v.doCollectionAction(label, postingID, topicID, collection, true)
}

func (v *mailView) removePostingFromCollection(postingID, topicID int64, collection mail.Collection) tea.Cmd {
	label := "Removed from collection " + terminal.SanitizeLine(collection.Name)
	return v.doCollectionAction(label, postingID, topicID, collection, false)
}

func (v *mailView) doCollectionAction(label string, postingID, topicID int64, collection mail.Collection, added bool) tea.Cmd {
	sourceID, sourceKind := v.currentSourceIdentity()
	v.pendingMutations++
	return func() tea.Msg {
		var err error
		if added {
			err = v.vc.sdk.Collections().AddTopic(v.vc.ctx, topicID, collection.ID)
		} else {
			err = v.vc.sdk.Collections().RemoveTopic(v.vc.ctx, topicID, collection.ID)
		}
		return collectionActionDoneMsg{
			action:     label,
			sourceID:   sourceID,
			sourceKind: sourceKind,
			postingID:  postingID,
			collection: collection,
			added:      added,
			err:        err,
		}
	}
}

func (v *mailView) updatePostingCollection(index int, collection mail.Collection, added bool) {
	memberships := v.postingList.postings[index].Collections
	for i, membership := range memberships {
		if membership.ID != collection.ID {
			continue
		}
		if !added {
			v.postingList.postings[index].Collections = append(memberships[:i], memberships[i+1:]...)
		}
		return
	}
	if added {
		v.postingList.postings[index].Collections = append(memberships, collection)
	}
}

func (v *mailView) movePostingToBox(postingID int64, destination mail.Source) tea.Cmd {
	return v.doPostingAction("Thread moved to "+destination.Name, v.boxMoveEffect(), v.currentBoxID(), postingID, func() error {
		return v.vc.sdk.Postings().Move(v.vc.ctx, destination.ID, postingID)
	})
}

func (v *mailView) handlePostingAction(key string) tea.Cmd {
	selected := v.postingList.selectedPosting()
	if selected == nil {
		return nil
	}
	p := *selected
	boxID := v.currentBoxID()

	switch key {
	case "l", "L":
		return v.moveSelectedToKnownBox("Reply Later", hey.BoxKindLater, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToReplyLater(v.vc.ctx, p.ID)
		})
	case "a", "A":
		return v.moveSelectedToKnownBox("Set Aside", hey.BoxKindSetAside, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToSetAside(v.vc.ctx, p.ID)
		})
	case "e", "E":
		return v.doPostingAction("Thread marked as seen", postingActionSeen, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MarkSeen(v.vc.ctx, []int64{p.ID})
		})
	case "u", "U":
		if p.Muted {
			v.notice = "Stop ignoring this thread to mark it unseen"
			return nil
		}
		if !p.Seen && !p.BubbledUp {
			v.notice = "Thread is already unseen"
			return nil
		}
		return v.doPostingAction("Thread marked as unseen", postingActionUnseen, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MarkUnseen(v.vc.ctx, []int64{p.ID})
		})
	case "i", "I":
		return v.moveSelectedToImbox(boxID, p.ID)
	case "d", "D":
		return v.moveSelectedToKnownBox("The Feed", hey.BoxKindFeed, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToFeed(v.vc.ctx, p.ID)
		})
	case "p", "P":
		return v.moveSelectedToKnownBox("Paper Trail", hey.BoxKindTrail, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToPaperTrail(v.vc.ctx, p.ID)
		})
	case "t", "T":
		return v.doPostingAction("Thread moved to Trash", postingActionRemove, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToTrash(v.vc.ctx, p.ID)
		})
	case "!":
		return v.doPostingAction("Thread marked as spam", postingActionRemove, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MarkSpam(v.vc.ctx, p.ID)
		})
	case "-":
		if p.Muted {
			v.notice = "Already ignoring thread"
			return nil
		}
		return v.doPostingAction("Thread ignored", postingActionIgnore, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().Mute(v.vc.ctx, p.ID)
		})
	case "+":
		if !p.Muted {
			v.notice = "Thread is not ignored"
			return nil
		}
		return v.doPostingAction("Stopped ignoring thread", postingActionStopIgnoring, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().Unmute(v.vc.ctx, p.ID)
		})
	case "r", "R":
		topicID := p.TopicID
		if topicID == 0 {
			topicID = p.ID
		}
		return v.loadReplyContext(topicID, p.Summary)
	case "f", "F":
		topicID := p.TopicID
		if topicID == 0 {
			topicID = p.ID
		}
		return v.loadForwardContext(topicID, p.Summary)
	}
	return nil
}

func (v *mailView) moveSelectedToImbox(boxID, postingID int64) tea.Cmd {
	for _, source := range v.boxes {
		if source.Kind == mail.KindBox && source.BoxKind == hey.BoxKindImbox {
			imboxID := source.ID
			return v.moveSelectedToKnownBox("Imbox", hey.BoxKindImbox, boxID, postingID, func() error {
				return v.vc.sdk.Postings().Move(v.vc.ctx, imboxID, postingID)
			})
		}
	}
	v.notice = "Imbox is unavailable"
	return nil
}

func (v *mailView) moveSelectedToKnownBox(name, kind string, boxID, postingID int64, fn func() error) tea.Cmd {
	if !v.movesOutOfCurrentBox(kind) {
		v.notice = "Already in " + name
		return nil
	}
	return v.doPostingAction("Thread moved to "+name, v.boxMoveEffect(), boxID, postingID, fn)
}

func (v *mailView) boxMoveEffect() postingActionEffect {
	if isOrganizedMailSource(v.currentSourceKind()) {
		return postingActionNone
	}
	return postingActionRemove
}

// movesOutOfCurrentBox reports whether a key that files a thread somewhere would move it
// at all. The destination is one of HEY's own box kinds, so it is the box's kind that
// answers — a label or a collection carries none and is never the destination.
func (v *mailView) movesOutOfCurrentBox(destinationBoxKind string) bool {
	source := v.currentSource()
	if source == nil {
		return true
	}
	return source.BoxKind != destinationBoxKind
}

func (v *mailView) doPostingAction(label string, effect postingActionEffect, boxID, postingID int64, fn func() error) tea.Cmd {
	sourceKind := v.currentSourceKind()
	v.pendingMutations++
	return func() tea.Msg {
		err := fn()
		return postingActionDoneMsg{
			action:     label,
			boxID:      boxID,
			sourceKind: sourceKind,
			postingID:  postingID,
			effect:     effect,
			err:        err,
		}
	}
}

func (v *mailView) finishMutation() {
	if v.pendingMutations > 0 {
		v.pendingMutations--
	}
}

// --- SDK type converters ---

// searchMatchToPosting makes a search match look like a posting, so the results list is
// the same list as a box's. A match names its own topic, where a posting only carries the
// URL of one, and the entry that matched is the row's date, excerpt and sender.
func searchMatchToPosting(match generated.SearchMatch) mail.Posting {
	// A search match is sanitized here as a posting is in mail.NewPosting: it is a row
	// in the same list.
	posting := mail.Posting{
		ID:        match.PostingId,
		TopicID:   match.Topic.Id,
		Name:      terminal.SanitizeLine(match.Topic.Name),
		CreatedAt: match.Topic.UpdatedAt,
		Creator: mail.Contact{
			ID:           match.Topic.Creator.Id,
			Name:         terminal.SanitizeLine(match.Topic.Creator.Name),
			EmailAddress: terminal.SanitizeLine(match.Topic.Creator.EmailAddress),
		},
	}
	if len(match.Entries) > 0 {
		entry := match.Entries[0]
		posting.CreatedAt = entry.CreatedAt
		posting.Summary = terminal.SanitizeLine(entry.Summary)
		posting.AlternativeSenderName = terminal.SanitizeLine(entry.AlternativeSenderName)
		posting.Creator = mail.Contact{
			ID:           entry.Creator.Id,
			Name:         terminal.SanitizeLine(entry.Creator.Name),
			EmailAddress: terminal.SanitizeLine(entry.Creator.EmailAddress),
		}
	}
	return posting
}

// --- Fetch commands ---

func (v *mailView) fetchSources(requestID uint64) tea.Cmd {
	return func() tea.Msg {
		result, err := v.vc.sdk.Boxes().List(v.vc.ctx)
		if err != nil {
			return errMsg{err}
		}
		var sdkBoxes []generated.Box
		if result != nil {
			sdkBoxes = *result
		}
		sources := make([]mail.Source, 0, len(sdkBoxes))
		for _, box := range sdkBoxes {
			sources = append(sources, mail.ListedBoxSource(box))
		}

		folders, folderErr := internalfolders.List(v.vc.ctx, v.vc.sdk)
		if folderErr == nil {
			for _, label := range folders {
				sources = append(sources, mail.Source{Kind: mail.KindFolder, ID: label.ID, Name: label.Name, AppURL: label.AppURL})
			}
		}
		collections, collectionErr := v.vc.sdk.Collections().List(v.vc.ctx)
		if collectionErr == nil && collections != nil {
			for _, collection := range *collections {
				sources = append(sources, mail.Source{Kind: mail.KindCollection, ID: collection.Id, Name: collection.Name, AppURL: collection.AppUrl})
			}
		}
		message := mailSourcesLoadedMsg{requestID: requestID, sources: sources, folderErr: folderErr, collectionErr: collectionErr}
		if screener, err := v.vc.sdk.Clearances().Summary(v.vc.ctx); err == nil && screener != nil {
			message.screenerCount = int(screener.PendingClearancesCount)
			message.screenerStream = screener.SignedStreamName
		}
		return message
	}
}

func (v *mailView) refreshScreenerCount() tea.Cmd {
	return func() tea.Msg {
		summary, err := v.vc.sdk.Clearances().Summary(v.vc.ctx)
		if err != nil {
			return screenerCountLoadedMsg{err: err}
		}
		if summary == nil {
			return screenerCountLoadedMsg{}
		}
		return screenerCountLoadedMsg{
			count:          int(summary.PendingClearancesCount),
			screenerStream: summary.SignedStreamName,
		}
	}
}

func (v *mailView) fetchPostings(ctx context.Context, requestID uint64, source mail.Source, page string) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readPostingsPage(ctx, source, page)
		return postingsLoadedMsg{
			requestID: requestID, boxID: source.ID, sourceKind: source.Kind,
			postings: postings, nextPage: nextPage, err: err,
		}
	}
}

// fetchMorePostings reads the page below the list, in the growing lane and without the
// spinner: what the reader is looking at is already on screen.
func (v *mailView) fetchMorePostings(ctx context.Context, requestID uint64, source mail.Source, page string) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readPostingsPage(ctx, source, page)
		return postingsAppendedMsg{
			requestID: requestID, boxID: source.ID, sourceKind: source.Kind,
			postings: postings, nextPage: nextPage, err: err,
		}
	}
}

// fetchBoxRefresh re-reads the top page of a box for the live update. It reads that and
// nothing else: the pages the reader scrolled down to, a search and a thread all stay as
// they were.
func (v *mailView) fetchBoxRefresh(ctx context.Context, requestID uint64, source mail.Source) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readPostingsPage(ctx, source, "")
		return postingsRefreshedMsg{
			requestID: requestID, boxID: source.ID, sourceKind: source.Kind,
			postings: postings, nextPage: nextPage, err: err,
		}
	}
}

// readPostingsPage reads one page of a source and answers the cursor for the page after
// it, empty once the source has nothing more to give. An empty page reads the first one.
// Which endpoint that is is internal/mail's business: a box is read on its own route,
// where HEY's ordering for it lives.
func (v *mailView) readPostingsPage(ctx context.Context, source mail.Source, page string) ([]mail.Posting, string, error) {
	read, err := mail.ReadPage(ctx, v.vc.sdk, source, page)
	if err != nil {
		return nil, "", err
	}
	return mail.Postings(read.Postings), read.Cursor, nil
}

func (v *mailView) fetchSearchResults(ctx context.Context, requestID uint64, query string, page int) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readSearchPage(ctx, query, page)
		return searchResultsLoadedMsg{requestID: requestID, query: query, postings: postings, nextPage: nextPage, err: err}
	}
}

// fetchMoreSearchResults reads the page of matches below the ones on screen, in the growing
// lane and without the spinner.
func (v *mailView) fetchMoreSearchResults(ctx context.Context, requestID uint64, query string, page int) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readSearchPage(ctx, query, page)
		return searchResultsAppendedMsg{requestID: requestID, query: query, postings: postings, nextPage: nextPage, err: err}
	}
}

// readSearchPage reads one page of matches and answers the number of the page after it,
// zero once the search has nothing more to give. Search numbers its pages where a box
// cursors them, so this is a page number rather than a token.
func (v *mailView) readSearchPage(ctx context.Context, query string, page int) ([]mail.Posting, int, error) {
	results, err := v.vc.sdk.Search().SearchPage(ctx, hey.SearchParams{Query: query, Page: max(page, 1)})
	if err != nil {
		return nil, 0, err
	}
	var matches []generated.SearchMatch
	if results != nil && results.Result != nil {
		matches = results.Result.Matches
	}
	postings := make([]mail.Posting, 0, len(matches))
	for _, match := range matches {
		postings = append(postings, searchMatchToPosting(match))
	}
	nextPage := 0
	if results != nil {
		nextPage = results.NextPage
	}
	return postings, nextPage, nil
}

// tuiThreadLimits is what the TUI reads a thread within: threadload's defaults, at the
// TUI's own concurrency.
var tuiThreadLimits = func() threadload.Limits {
	limits := threadload.DefaultLimits
	limits.Concurrency = maxConcurrentMessageFetches
	return limits
}()

// fetchTopic reads a whole thread through threadload — every page of the index, every
// body within the limits — and then its inline images within the image budget. A
// thread read only in part is shown with a notice rather than refused: the reader is
// looking at it, and can see what is missing.
func (v *mailView) fetchTopic(ctx context.Context, requestID uint64, boxID, topicID, postingID int64, title string) tea.Cmd {
	return func() tea.Msg {
		thread, err := threadload.Load(ctx, threadload.NewSDKSource(v.vc.sdk), threadload.Request{
			TopicID: topicID,
			Hydrate: true,
			Limits:  tuiThreadLimits,
		})
		if err != nil {
			return topicLoadedMsg{requestID: requestID, boxID: boxID, topicID: topicID, title: title, err: err}
		}
		if len(thread.Entries) == 0 {
			return topicLoadedMsg{requestID: requestID, boxID: boxID, topicID: topicID, title: title, err: fmt.Errorf("topic %d returned no data", topicID)}
		}

		entries := make([]mail.Entry, len(thread.Entries))
		var attachments []messageAttachment
		var imageURLs []string
		// Only a terminal that can draw the images pays for finding them.
		wantImages := v.vc.imageRenderer.protocol() == imageProtocolKitty && v.vc.imageFetcher != nil
		for i, loaded := range thread.Entries {
			entries[i] = mail.LoadedEntry(loaded)
			if loaded.Message == nil {
				continue
			}
			for position, attachment := range htmlutil.ExtractAttachments(loaded.Message.Content) {
				attachments = append(attachments, messageAttachment{
					ID:          fmt.Sprintf("%d:%d", loaded.Entry.Id, position+1),
					MessageID:   loaded.Entry.Id,
					Filename:    attachment.Filename,
					ContentType: attachment.ContentType,
					ByteSize:    attachment.ByteSize,
					URL:         attachment.URL,
				})
			}
			if wantImages {
				imageURLs = append(imageURLs, extractImageURLs(loaded.Message.Content)...)
			}
			// The loader's copy is released once the entry has what it shows.
			thread.Entries[i].Message = nil
		}

		var images [][]byte
		if wantImages {
			images = newImageBudget().fetchImages(ctx, v.vc.imageFetcher, imageURLs)
		}

		return topicLoadedMsg{
			requestID:   requestID,
			boxID:       boxID,
			topicID:     topicID,
			postingID:   postingID,
			title:       title,
			entries:     entries,
			attachments: attachments,
			images:      images,
			notice:      thread.Notice(tuiThreadLimits),
			complete:    thread.Complete(),
		}
	}
}

// --- Entry rendering ---

// renderEntries renders the thread's messages and returns the content along
// with the line each message header starts on, for j/k jumps.
func (v *mailView) renderEntries(entries []mail.Entry) (string, []int) {
	var b strings.Builder
	offsets := make([]int, 0, len(entries))
	lineCount := 0
	sepWidth := max(v.vc.width-4, 40)
	sep := v.vc.styles.separator.Render(strings.Repeat("─", sepWidth))

	for i, e := range entries {
		if i > 0 {
			fmt.Fprintf(&b, "%s\n", sep)
			lineCount++
		}
		offsets = append(offsets, lineCount)
		entryStart := b.Len()

		from := e.Creator.Name
		if from == "" {
			from = e.Creator.EmailAddress
		}
		if e.AlternativeSenderName != "" {
			from = e.AlternativeSenderName
		}

		fmt.Fprintf(&b, "%s  %s\n", v.vc.styles.entryFrom.Render(terminal.SanitizeLine(from)), v.vc.styles.entryDate.Render(formatDisplayDateTime(e.CreatedAt)))
		if e.Summary != "" {
			fmt.Fprintf(&b, "%s\n", terminal.SanitizeLine(e.Summary))
		}
		switch {
		case !e.Body.IsEmpty():
			fmt.Fprintf(&b, "\n%s\n", v.vc.styles.entryBody.Render(markdown.Render(e.Body, sepWidth)))
		case e.BodyState == string(threadload.StateOverLimit), e.BodyState == string(threadload.StateFailed):
			fmt.Fprintf(&b, "\n%s\n", v.vc.styles.entryDate.Render("(body not read: "+e.BodyState+")"))
		}
		entryAttachments := attachmentsForMessage(v.attachments, e.ID)
		if panel := renderAttachmentPanel(entryAttachments, selectedAttachmentForMessage(v.attachments, v.attachmentCursor, e.ID)); panel != "" {
			fmt.Fprintf(&b, "\n%s\n", panel)
		}
		b.WriteString("\n")
		lineCount += strings.Count(b.String()[entryStart:], "\n")
	}

	return b.String(), offsets
}
