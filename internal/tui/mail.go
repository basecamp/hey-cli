package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	mailRequestBundle
	mailRequestSeen
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

// bundleLoadedMsg is the first page of what a bundle row opens: the unseen threads
// inside an unread bundle, or — for a bundle that has been read through — every thread
// with its contact, which is where the web app sends a read bundle. contactID says
// which; zero is the unseen list for postingID's bundle.
type bundleLoadedMsg struct {
	requestID uint64
	boxID     int64
	postingID int64
	contactID int64
	title     string
	nextPage  string
	postings  []mail.Posting
	err       error
}

// bundleAppendedMsg is the page of a bundle's threads below the ones on screen, read
// because the reader scrolled towards the bottom. Its own lane, like a box's.
type bundleAppendedMsg struct {
	requestID uint64
	postingID int64
	contactID int64
	nextPage  string
	postings  []mail.Posting
	err       error
}

// seenLoadedMsg is the first page of the Imbox's Previously Seen threads, read on
// their own route when the reader jumps to the seen screen. The screen is never
// re-read live — the box underneath still refreshes through its own lane, and
// reopening reads the list fresh.
type seenLoadedMsg struct {
	requestID uint64
	nextPage  string
	postings  []mail.Posting
	err       error
}

// seenAppendedMsg is the page of seen threads below the ones on screen, read because
// the reader scrolled towards the bottom. Its own lane, like a box's.
type seenAppendedMsg struct {
	requestID uint64
	nextPage  string
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
	seen       bool // the action was taken on the Previously Seen screen
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
	seen       bool // the action was taken on the Previously Seen screen
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
	threadPostingID  int64 // the posting the open thread was opened from, zero when it has none
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
	searchNextPage         int  // the page of matches after the ones on screen, zero at the last
	searchLoadingMore      bool // a page of matches is already on its way
	bundleList             contentList
	bundleActive           bool
	bundlePostingID        int64  // the bundle row the open list belongs to
	bundleContactID        int64  // set when the list is the contact's threads instead of the bundle's unseen
	bundleTitle            string // what the list is, sanitized: "New from X" or "All threads with X"
	bundleNextPage         string // the cursor for the page below, empty at the last
	bundleLoadingMore      bool   // a page of the bundle is already on its way
	seenList               contentList
	seenActive             bool
	seenNextPage           string // the cursor for the page below, empty at the last
	seenLoadingMore        bool   // a page of seen threads is already on its way
	screenerCount          int    // senders waiting in The Screener
	lastBulkReplyID        int64  // delayed delivery currently available for undo
	pendingMutations       int    // writes that must finish before changing the account context
	notice                 string // one-shot confirmation shown above the posting list
	requests               requestLane[mailRequestKind]
	sourceRequestID        uint64
	folderDiscoveryErr     string
	collectionDiscoveryErr string

	liveRequestID  uint64 // identifies the only live re-read allowed to update the list
	liveRefreshDue bool   // a re-read is already on its way
	moreRequestID  uint64 // identifies the only page-below read allowed to grow the list
	searchMoreID   uint64 // the same, for the search results
	bundleMoreID   uint64 // the same, for an open bundle's threads
	seenMoreID     uint64 // the same, for the Previously Seen screen
}

func newMailView(vc *viewContext) *mailView {
	view := &mailView{
		vc:            vc,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		searchList:    contentList{hideSeenState: true},
		bundleList:    contentList{hideSeenState: true},
		seenList:      contentList{hideSeenState: true},
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

	case bundleLoadedMsg:
		if msg.boxID != v.currentBoxID() {
			return nil, true
		}
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		v.bundleActive = true
		v.bundlePostingID = msg.postingID
		v.bundleContactID = msg.contactID
		v.bundleTitle = msg.title
		v.bundleNextPage = msg.nextPage
		v.bundleLoadingMore = false
		v.bundleList.setPostings(msg.postings)
		return v.loadMoreBundlePostings(), true

	case bundleAppendedMsg:
		if msg.requestID != v.bundleMoreID || !v.bundleActive || msg.postingID != v.bundlePostingID || msg.contactID != v.bundleContactID {
			return nil, true
		}
		v.bundleLoadingMore = false
		if msg.err != nil {
			v.noteFailure("Could not load more mail", msg.err)
			return nil, true
		}
		v.bundleList.growPostings(msg.postings)
		if len(msg.postings) == 0 {
			v.bundleNextPage = ""
		} else {
			v.bundleNextPage = msg.nextPage
		}
		return v.loadMoreBundlePostings(), true

	case seenLoadedMsg:
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		if !v.seenActive {
			return nil, true
		}
		v.seenNextPage = msg.nextPage
		v.seenLoadingMore = false
		v.seenList.setPostings(msg.postings)
		return v.loadMoreSeenPostings(), true

	case seenAppendedMsg:
		if msg.requestID != v.seenMoreID || !v.seenActive {
			return nil, true
		}
		v.seenLoadingMore = false
		if msg.err != nil {
			v.noteFailure("Could not load more mail", msg.err)
			return nil, true
		}
		v.seenList.growPostings(msg.postings)
		if len(msg.postings) == 0 {
			v.seenNextPage = ""
		} else {
			v.seenNextPage = msg.nextPage
		}
		return v.loadMoreSeenPostings(), true

	case topicLoadedMsg:
		// A zero box identifies a topic opened directly rather than selected from
		// the current list. It remains valid while sources load or another section
		// is on screen.
		if msg.boxID != 0 && msg.boxID != v.currentBoxID() {
			return nil, true
		}
		if cmd, ok := v.requests.settle(newRequestResult(msg.requestID, msg.err)); !ok {
			return cmd, true
		}
		v.inThread = true
		v.topicID = msg.topicID
		v.threadPostingID = msg.postingID
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
		if !v.requests.accepts(newRequestResult(msg.requestID, msg.err)) || msg.boxID != v.currentBoxID() || msg.seen != v.seenActive {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			v.noteFailure("Could not preview bulk reply", msg.err)
			return nil, true
		}
		if msg.draft == nil || len(msg.draft.Entries) == 0 {
			return notify("No replyable threads found; nothing was sent"), true
		}
		form := newBulkReplyForm(msg.postingIDs, msg.draft, msg.seen, v.vc.styles)
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
		if msg.seen {
			v.seenList.clearSelected()
		} else {
			v.postingList.clearSelected()
		}
		count := int(msg.delivery.EntriesCount)
		sent := fmt.Sprintf("%d bulk %s sent", count, replyNoun(count))
		v.lastBulkReplyID = 0
		if msg.delivery.Delayed {
			sent = fmt.Sprintf("%d bulk %s queued with undo available", count, replyNoun(count))
			// The undo stands in the help bar for as long as it is available, so the
			// toast can say so and go.
			if msg.delivery.Id > 0 {
				v.lastBulkReplyID = msg.delivery.Id
				sent += " — press ctrl+u to undo"
			}
		}
		if msg.skipped > 0 {
			sent += fmt.Sprintf("; %d skipped", msg.skipped)
		}
		return notify(sent), true

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
		return notify("Bulk reply recalled"), true

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
		return notify(msg.label), true

	case attachmentSavedMsg:
		if !v.currentAttachmentAction(msg.topicID, msg.attachmentID) {
			return nil, true
		}
		if msg.err != nil {
			saveErr := apierr.AsError(msg.err)
			if saveErr.Code == "usage" && strings.HasPrefix(saveErr.Message, "destination already exists:") {
				return notify("Attachment already exists: " + msg.path), true
			}
			v.noteFailure("Could not save attachment", msg.err)
			return nil, true
		}
		return notify("Saved attachment to " + msg.path), true

	case attachmentOpenedMsg:
		if !v.currentAttachmentAction(msg.topicID, msg.attachmentID) {
			return nil, true
		}
		if msg.err != nil {
			v.noteFailure("Could not open attachment", msg.err)
			return nil, true
		}
		return notify("Opened attachment " + msg.filename), true

	case postingActionDoneMsg:
		v.finishMutation()
		if msg.seen {
			return v.applySeenPostingAction(msg), true
		}
		if msg.boxID != v.currentBoxID() || (msg.sourceKind != "" && msg.sourceKind != v.currentSourceKind()) {
			return nil, true
		}
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		done := notify(msg.action)
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
				return tea.Batch(done, v.requestPostings(*source)), true
			}
		}
		// A thread leaving the list can uncover the bottom of it, so what is below comes up
		// to fill the gap rather than leaving a short list with more waiting behind it.
		return tea.Batch(done, v.loadMorePostings()), true

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
		var done tea.Cmd
		if msg.sourceID == v.currentBoxID() && msg.sourceKind == v.currentSourceKind() {
			done = notify(msg.action)
		}
		if msg.created {
			return tea.Batch(done, v.requestSources()), true
		}
		if source := v.currentSource(); source != nil && msg.sourceID == source.ID && msg.sourceKind == source.Kind {
			return tea.Batch(done, v.requestPostings(*source)), true
		}
		return done, true

	case collectionActionDoneMsg:
		v.finishMutation()
		if msg.seen {
			if !v.seenActive {
				return nil, true
			}
			if msg.err != nil {
				v.notice = terminal.SanitizeLine(errorNotice("Could not update collections", msg.err))
				return nil, true
			}
			if index := postingIndexIn(v.seenList.postings, msg.postingID); index >= 0 {
				updatePostingCollection(&v.seenList, index, msg.collection, msg.added)
			}
			return notify(msg.action), true
		}
		if msg.sourceID != v.currentBoxID() || msg.sourceKind != v.currentSourceKind() {
			return nil, true
		}
		if msg.err != nil {
			v.notice = terminal.SanitizeLine(errorNotice("Could not update collections", msg.err))
			return nil, true
		}
		done := notify(msg.action)
		if index := v.postingIndex(msg.postingID); index >= 0 {
			updatePostingCollection(&v.postingList, index, msg.collection, msg.added)
			if !msg.added && msg.sourceKind == mail.KindCollection && msg.collection.ID == msg.sourceID {
				v.removePostingAt(index)
			}
		}
		if msg.sourceKind == mail.KindCollection && msg.collection.ID == msg.sourceID {
			if source := v.currentSource(); source != nil {
				return tea.Batch(done, v.requestPostings(*source)), true
			}
		}
		return done, true
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
	if v.bundleActive {
		view := v.bundleList.view()
		if len(v.bundleList.postings) == 0 {
			// A contact with no threads is HEY's own empty case; an unseen list
			// answering none means the bundle was read since its row was drawn.
			if v.bundleContactID != 0 {
				view = styleMuted.Render("  No emails with this contact.")
			} else {
				view = styleMuted.Render("  Nothing unseen here any more — reload the box to catch it up.")
			}
		}
		if v.notice != "" {
			return v.vc.styles.title.Render(v.notice) + "\n" + view
		}
		return view
	}
	if v.seenActive {
		view := v.seenList.view()
		if len(v.seenList.postings) == 0 && !v.requests.loading {
			view = styleMuted.Render("  Nothing has been seen yet.")
		}
		if v.notice != "" {
			return v.vc.styles.title.Render(v.notice) + "\n" + view
		}
		return view
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

// listHeader carries the one-shot notice and The Screener's standing invitation above
// the posting list. Connection status belongs to the app header, where every section
// can see it.
func (v *mailView) listHeader() string {
	var lines []string
	if v.notice != "" {
		lines = append(lines, v.vc.styles.title.Render(v.notice))
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
		if v.fileablePosting() != nil {
			bindings = append(bindings, helpBinding{"l", "reply later"}, helpBinding{"a", "set aside"})
		}
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
	if v.bundleActive {
		return []helpBinding{{"enter", "open"}, {"esc", "back"}}
	}
	if v.seenActive {
		ignoreBinding := helpBinding{"-", "ignore"}
		if selected := v.seenList.selectedPosting(); selected != nil && selected.Muted {
			ignoreBinding = helpBinding{"+", "stop ignoring"}
		}
		bindings := []helpBinding{
			{"enter", "open"},
			{"space", "select"},
			{"ctrl+b", "bulk reply"},
			{"r", "reply"},
			{"f", "forward"},
			{"v", "move"},
			{"b", "labels"},
			{"n", "collections"},
			{"u", "unseen"},
			{"l", "reply later"},
			{"a", "set aside"},
			{"d", "feed"},
			{"p", "paper trail"},
			{"t", "trash"},
			{"!", "spam"},
			ignoreBinding,
		}
		if v.lastBulkReplyID != 0 {
			bindings = append(bindings, helpBinding{"ctrl+u", "undo bulk reply"})
		}
		return modifiersLast(bindings)
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
		helpBinding{"9", "previously seen"},
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
	if v.bundleActive {
		label := v.bundleTitle
		if v.bundleLoadingMore {
			label += " · loading more…"
		}
		return nil, 0, label, true
	}
	// The seen screen keeps the box row — its threads are the Imbox's, the number keys
	// still work, and esc lands on the tab that stays highlighted — under its own label.
	label := "Mail"
	if v.seenActive {
		label = "Previously Seen"
		if v.seenLoadingMore {
			label += " · loading more…"
		}
	} else if v.boxIndex >= 0 && v.boxIndex < len(v.boxes) {
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
	// The screen is the Imbox's, so its tab arrives with the boxes rather than
	// standing alone while they load.
	if v.imboxSource() != nil {
		items = append(items, navItem{shortcut: "9", label: "Previously Seen"})
		if v.seenActive {
			selected = len(items) - 1
		}
	}
	if v.hasLabels() {
		items = append(items, navItem{shortcut: "L", label: "Labels"})
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
	if v.searchActive || v.searchOpen() || v.bundleActive {
		return nil
	}
	tabIndexes := v.tabBoxIndexes()
	if v.seenActive {
		if len(tabIndexes) > 0 {
			return v.switchBox(tabIndexes[len(tabIndexes)-1])
		}
		return nil
	}
	switch v.currentSourceKind() {
	case mail.KindCollection:
		if v.hasLabels() {
			v.openLabels()
			return nil
		}
		return v.openPreviouslySeen()
	case mail.KindFolder:
		return v.openPreviouslySeen()
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
	if v.searchActive || v.searchOpen() || v.bundleActive {
		return nil
	}
	if v.seenActive {
		if v.hasLabels() {
			v.openLabels()
		} else if v.hasCollections() {
			v.openCollections()
		}
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
			return v.openPreviouslySeen()
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
		case "a", "A", "l":
			return v.fileOpenThread(msg.String())
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
			case "k":
				v.searchList.moveUp()
			case "j":
				v.searchList.moveDown()
				return v.loadMoreSearchResults()
			case "/", "s", "S":
				return v.startSearch()
			}
		}
		return nil
	}

	if v.bundleActive {
		switch msg.Key().Code {
		case tea.KeyUp:
			v.bundleList.moveUp()
		case tea.KeyDown:
			v.bundleList.moveDown()
			return v.loadMoreBundlePostings()
		case tea.KeyEnter:
			return v.openSelected()
		default:
			switch msg.String() {
			case "k":
				v.bundleList.moveUp()
			case "j":
				v.bundleList.moveDown()
				return v.loadMoreBundlePostings()
			}
		}
		return nil
	}

	if v.seenActive {
		switch msg.Key().Code {
		case tea.KeyUp:
			v.seenList.moveUp()
		case tea.KeyDown:
			v.seenList.moveDown()
			return v.loadMoreSeenPostings()
		case tea.KeyEnter:
			return v.openSelected()
		default:
			switch msg.String() {
			case "k":
				v.seenList.moveUp()
			case "j":
				v.seenList.moveDown()
				return v.loadMoreSeenPostings()
			case " ", "space":
				v.seenList.toggleSelected()
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
			default:
				return v.handlePostingAction(msg.String())
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
		case "k":
			v.postingList.moveUp()
		case "j":
			v.postingList.moveDown()
			return v.loadMorePostings()
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

func (v *mailView) InThread() bool {
	return v.inThread || v.searchActive || v.bundleActive || v.seenActive
}

func (v *mailView) ExitDetail(key string) {
	if key == "q" && (v.searchActive || v.bundleActive || v.seenActive) && !v.inThread && (v.requests.kind == mailRequestTopic || v.requests.kind == mailRequestSearch) {
		v.requests.cancel()
		v.clearSearch()
		v.clearBundle()
		v.clearSeen()
		return
	}
	v.ExitThread()
}

func (v *mailView) ExitThread() {
	if (v.searchActive || v.bundleActive || v.seenActive) && !v.inThread && (v.requests.kind == mailRequestTopic || v.requests.kind == mailRequestSearch) {
		v.requests.cancel()
		return
	}
	if v.inThread {
		v.inThread = false
		v.threadNotice = ""
		v.threadPostingID = 0
		v.modal = nil
		v.requests.cancel()
		return
	}
	if v.bundleActive {
		v.clearBundle()
		v.requests.cancel()
		return
	}
	if v.seenActive {
		v.clearSeen()
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

func (v *mailView) clearBundle() {
	v.bundleActive = false
	v.bundlePostingID = 0
	v.bundleContactID = 0
	v.bundleTitle = ""
	v.bundleNextPage = ""
	v.bundleLoadingMore = false
	v.bundleMoreID++
	v.bundleList.setPostings(nil)
	v.notice = ""
	v.modal = nil
}

func (v *mailView) clearSeen() {
	v.seenActive = false
	v.seenNextPage = ""
	v.seenLoadingMore = false
	v.seenMoreID++
	v.seenList.setPostings(nil)
	v.notice = ""
	v.modal = nil
}

// applySeenPostingAction lands a thread action taken on the Previously Seen screen. A
// thread moved out of the Imbox, trashed, marked spam or marked unseen is not previously
// seen any more, so it leaves the screen, and what is below comes up to fill the gap.
func (v *mailView) applySeenPostingAction(msg postingActionDoneMsg) tea.Cmd {
	if !v.seenActive {
		return nil
	}
	if msg.err != nil {
		return func() tea.Msg { return errMsg{msg.err} }
	}
	if idx := postingIndexIn(v.seenList.postings, msg.postingID); idx >= 0 {
		switch msg.effect {
		case postingActionNone:
		case postingActionRemove, postingActionUnseen:
			v.seenList.removeAt(idx)
		case postingActionSeen:
			v.seenList.markSeen(idx)
		case postingActionIgnore:
			v.seenList.postings[idx].Muted = true
		case postingActionStopIgnoring:
			v.seenList.postings[idx].Muted = false
		}
	}
	return tea.Batch(notify(msg.action), v.loadMoreSeenPostings())
}

func (v *mailView) CancelPendingDetail() bool {
	if v.requests.kind != mailRequestTopic && v.requests.kind != mailRequestReply && v.requests.kind != mailRequestForward && v.requests.kind != mailRequestSearch && v.requests.kind != mailRequestBundle && v.requests.kind != mailRequestSeen && v.requests.kind != mailRequestBulkReply {
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
	v.bundleList.setSize(width, height)
	v.seenList.setSize(width, height)
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
	case "L":
		if v.hasLabels() {
			v.openLabels()
			return func() tea.Msg { return nil }
		}
	case "K":
		if v.hasCollections() {
			v.openCollections()
			return func() tea.Msg { return nil }
		}
	case "9":
		return v.openPreviouslySeen()
	}
	return v.switchBox(boxForShortcut(key, v.boxes))
}

func (v *mailView) switchBox(index int) tea.Cmd {
	if index < 0 || index >= len(v.boxes) {
		return nil
	}
	if index == v.boxIndex {
		// The box under the seen screen: its number or tab closes the screen the
		// way esc does, landing on the list that is already there.
		if v.seenActive {
			v.clearSeen()
			v.requests.cancel()
		}
		return nil
	}
	v.inThread = false
	v.threadNotice = ""
	v.threadPostingID = 0
	v.clearSearch()
	v.clearBundle()
	v.clearSeen()
	v.requests.cancel()
	v.notice = ""
	v.postingList.setPostings(nil)
	v.boxIndex = index
	return v.requestPostings(v.boxes[index])
}

// openPreviouslySeen jumps to the Imbox's Previously Seen threads on their own screen,
// the web app's 9 shortcut. It opens over whichever source is on screen — the route is
// account-scoped, so nothing is asked of the current box — and esc returns there. The
// screen shows every seen thread flat, which is also the way to see what a covered
// Imbox hides.
func (v *mailView) openPreviouslySeen() tea.Cmd {
	if v.seenActive {
		return nil
	}
	v.inThread = false
	v.threadNotice = ""
	v.threadPostingID = 0
	v.clearSearch()
	v.clearBundle()
	v.notice = ""
	// The screen opens before its first page answers, the way a box switch does: the
	// tab is selected there and then, so the ribbon reads on past it to Labels rather
	// than asking for the screen again.
	v.seenActive = true
	v.seenList.setPostings(nil)
	v.seenNextPage = ""
	v.seenLoadingMore = false
	v.seenMoreID++
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestSeen)
	return v.fetchSeenPostings(ctx, requestID)
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

// reloadPostings reads the box on screen again, on the user's say-so.
func (v *mailView) reloadPostings() tea.Cmd {
	source := v.currentSource()
	if source == nil {
		return nil
	}
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

// requestBundle opens an unread bundle row: the unseen threads it groups, from their
// first page. They grow downwards from there, the same way a box does.
func (v *mailView) requestBundle(postingID int64) tea.Cmd {
	v.bundleNextPage = ""
	v.bundleLoadingMore = false
	v.bundleMoreID++
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestBundle)
	return v.fetchBundle(ctx, requestID, v.currentBoxID(), postingID)
}

// requestContactThreads opens a read bundle row: every thread with its contact, from
// the first page, in the same list the unseen bundle uses.
func (v *mailView) requestContactThreads(contactID int64) tea.Cmd {
	v.bundleNextPage = ""
	v.bundleLoadingMore = false
	v.bundleMoreID++
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestBundle)
	return v.fetchContactThreads(ctx, requestID, v.currentBoxID(), contactID)
}

// loadMoreBundlePostings reads the page of the bundle below the ones the reader has
// scrolled to, or below threads they can already see the end of.
func (v *mailView) loadMoreBundlePostings() tea.Cmd {
	if !v.bundleActive || v.bundleLoadingMore || v.bundleNextPage == "" {
		return nil
	}
	if v.bundleList.hasRowsBelow() && len(v.bundleList.postings)-v.bundleList.cursor > loadMoreThreshold {
		return nil
	}

	v.bundleLoadingMore = true
	v.bundleMoreID++
	if v.bundleContactID != 0 {
		return v.fetchMoreContactThreads(v.vc.ctx, v.bundleMoreID, v.bundleContactID, v.bundleNextPage)
	}
	return v.fetchMoreBundlePostings(v.vc.ctx, v.bundleMoreID, v.bundlePostingID, v.bundleNextPage)
}

// loadMoreSeenPostings reads the page of seen threads below the ones the reader has
// scrolled to, or below threads they can already see the end of.
func (v *mailView) loadMoreSeenPostings() tea.Cmd {
	if !v.seenActive || v.seenLoadingMore || v.seenNextPage == "" {
		return nil
	}
	if v.seenList.hasRowsBelow() && len(v.seenList.postings)-v.seenList.cursor > loadMoreThreshold {
		return nil
	}

	v.seenLoadingMore = true
	v.seenMoreID++
	return v.fetchMoreSeenPostings(v.vc.ctx, v.seenMoreID, v.seenNextPage)
}

func (v *mailView) requestTopic(boxID, topicID, postingID int64, title string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestTopic)
	return v.fetchTopic(ctx, requestID, boxID, topicID, postingID, title)
}

func (v *mailView) postingIndex(postingID int64) int {
	return postingIndexIn(v.postingList.postings, postingID)
}

func postingIndexIn(postings []mail.Posting, postingID int64) int {
	for i := range postings {
		if postings[i].ID == postingID {
			return i
		}
	}
	return -1
}

func (v *mailView) removePostingAt(index int) {
	v.postingList.removeAt(index)
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
	if v.seenActive {
		selected = v.seenList.selectedPosting()
	}
	if v.bundleActive {
		selected = v.bundleList.selectedPosting()
	}
	if selected == nil {
		return nil
	}
	// A bundle names a topic only when it holds one unseen thread — otherwise its row
	// opens the bundle itself: the unseen threads while there are any, or every thread
	// with its contact once it has been read through, which is where the web app sends
	// a read bundle. The posting's own id is never a topic id, so anything else without
	// one has nowhere to go.
	if selected.TopicID == 0 {
		if selected.IsBundle {
			if selected.Seen {
				return v.requestContactThreads(selected.Creator.ID)
			}
			return v.requestBundle(selected.ID)
		}
		err := fmt.Errorf("this item does not identify an email thread")
		return func() tea.Msg { return errMsg{err} }
	}
	// Posting.Name is the thread's subject; Summary is only the last message's
	// excerpt, kept as the fallback for a posting with no name.
	title := selected.Name
	if title == "" {
		title = selected.Summary
	}
	return v.requestTopic(v.currentBoxID(), selected.TopicID, selected.ID, title)
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
	if v.seenActive {
		list = &v.seenList
	}
	if v.bundleActive {
		list = &v.bundleList
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
	selected := v.actionList().selectedPosting()
	currentSource := v.actionSource()
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
	selected := v.actionList().selectedPosting()
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
	selected := v.actionList().selectedPosting()
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
	seen := v.seenActive
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
			seen:       seen,
			err:        err,
		}
	}
}

func updatePostingCollection(list *contentList, index int, collection mail.Collection, added bool) {
	memberships := list.postings[index].Collections
	for i, membership := range memberships {
		if membership.ID != collection.ID {
			continue
		}
		if !added {
			list.postings[index].Collections = append(memberships[:i], memberships[i+1:]...)
		}
		return
	}
	if added {
		list.postings[index].Collections = append(memberships, collection)
	}
}

func (v *mailView) movePostingToBox(postingID int64, destination mail.Source) tea.Cmd {
	return v.doPostingAction("Thread moved to "+destination.Name, v.boxMoveEffect(), v.currentBoxID(), postingID, func() error {
		return v.vc.sdk.Postings().Move(v.vc.ctx, destination.ID, postingID)
	})
}

// actionList is the list a thread action works on: the Previously Seen screen's list
// while it is open, the box list otherwise. Search results and bundles navigate only.
func (v *mailView) actionList() *contentList {
	if v.seenActive {
		return &v.seenList
	}
	return &v.postingList
}

// actionSource is the box a thread action files out of: the Imbox while the Previously
// Seen screen is open — its threads are the Imbox's whatever source the screen was
// opened over — and the source on screen otherwise.
func (v *mailView) actionSource() *mail.Source {
	if v.seenActive {
		return v.imboxSource()
	}
	return v.currentSource()
}

func (v *mailView) imboxSource() *mail.Source {
	for i := range v.boxes {
		if v.boxes[i].Kind == mail.KindBox && v.boxes[i].BoxKind == hey.BoxKindImbox {
			return &v.boxes[i]
		}
	}
	return nil
}

// fileOpenThread files the thread on screen the way the same key files it on the
// list, matching the web app's topic toolbar keeping its hotkeys live while a
// thread is open. Only a thread opened from a filing list — a box or Previously
// Seen — has a posting row to act on: over search results, bundles, and topics
// opened directly the key answers with a notice instead of silence.
func (v *mailView) fileOpenThread(key string) tea.Cmd {
	if posting := v.fileablePosting(); posting != nil {
		return v.postingAction(key, *posting)
	}
	v.notice = "Can't file this thread from here"
	return nil
}

// fileablePosting is the row the open thread files on: the posting the thread was
// opened from, found by id rather than under the cursor because the mark-seen that
// opening triggers can resort the list, slide the row under the cover, and clamp
// the cursor onto some other row while the thread is on screen.
func (v *mailView) fileablePosting() *mail.Posting {
	if v.searchActive || v.bundleActive || v.threadPostingID == 0 {
		return nil
	}
	return v.openedPosting(v.threadPostingID)
}

func (v *mailView) handlePostingAction(key string) tea.Cmd {
	selected := v.actionList().selectedPosting()
	if selected == nil {
		return nil
	}
	return v.postingAction(key, *selected)
}

func (v *mailView) postingAction(key string, p mail.Posting) tea.Cmd {
	boxID := v.currentBoxID()

	switch key {
	// Only lowercase moves to Reply Later: Shift+L navigates to Labels, the
	// way Shift+K reaches Collections.
	case "l":
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
		if p.TopicID == 0 {
			return v.openSelected()
		}
		return v.loadReplyContext(p.TopicID, p.Summary)
	case "f", "F":
		if p.TopicID == 0 {
			return v.openSelected()
		}
		return v.loadForwardContext(p.TopicID, p.Summary)
	}
	return nil
}

func (v *mailView) moveSelectedToImbox(boxID, postingID int64) tea.Cmd {
	if source := v.imboxSource(); source != nil {
		imboxID := source.ID
		return v.moveSelectedToKnownBox("Imbox", hey.BoxKindImbox, boxID, postingID, func() error {
			return v.vc.sdk.Postings().Move(v.vc.ctx, imboxID, postingID)
		})
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
	if v.seenActive {
		return postingActionRemove
	}
	if isOrganizedMailSource(v.currentSourceKind()) {
		return postingActionNone
	}
	return postingActionRemove
}

// movesOutOfCurrentBox reports whether a key that files a thread somewhere would move it
// at all. The destination is one of HEY's own box kinds, so it is the box's kind that
// answers — a label or a collection carries none and is never the destination.
func (v *mailView) movesOutOfCurrentBox(destinationBoxKind string) bool {
	source := v.actionSource()
	if source == nil {
		return true
	}
	return source.BoxKind != destinationBoxKind
}

func (v *mailView) doPostingAction(label string, effect postingActionEffect, boxID, postingID int64, fn func() error) tea.Cmd {
	sourceKind := v.currentSourceKind()
	seen := v.seenActive
	v.pendingMutations++
	return func() tea.Msg {
		err := fn()
		return postingActionDoneMsg{
			action:     label,
			boxID:      boxID,
			sourceKind: sourceKind,
			postingID:  postingID,
			effect:     effect,
			seen:       seen,
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

func (v *mailView) fetchBundle(ctx context.Context, requestID uint64, boxID, postingID int64) tea.Cmd {
	return func() tea.Msg {
		postings, title, nextPage, err := v.readBundlePage(ctx, postingID, "")
		return bundleLoadedMsg{requestID: requestID, boxID: boxID, postingID: postingID, title: title, postings: postings, nextPage: nextPage, err: err}
	}
}

// fetchMoreBundlePostings reads the page of the bundle below the ones on screen, in the
// growing lane and without the spinner.
func (v *mailView) fetchMoreBundlePostings(ctx context.Context, requestID uint64, postingID int64, cursor string) tea.Cmd {
	return func() tea.Msg {
		postings, _, nextPage, err := v.readBundlePage(ctx, postingID, cursor)
		return bundleAppendedMsg{requestID: requestID, postingID: postingID, postings: postings, nextPage: nextPage, err: err}
	}
}

// readBundlePage reads one page of the unseen threads a bundle posting groups, titled
// the way the web app titles its bundle view.
func (v *mailView) readBundlePage(ctx context.Context, postingID int64, cursor string) ([]mail.Posting, string, string, error) {
	page, err := v.vc.sdk.Postings().BundleUnseenPage(ctx, postingID, cursor)
	if err != nil {
		return nil, "", "", err
	}
	return mail.Postings(page.Postings), "New from " + terminal.SanitizeLine(page.Contact.Name), page.NextPage, nil
}

func (v *mailView) fetchSeenPostings(ctx context.Context, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readSeenPage(ctx, "")
		return seenLoadedMsg{requestID: requestID, postings: postings, nextPage: nextPage, err: err}
	}
}

// fetchMoreSeenPostings reads the page of seen threads below the ones on screen, in the
// growing lane and without the spinner.
func (v *mailView) fetchMoreSeenPostings(ctx context.Context, requestID uint64, cursor string) tea.Cmd {
	return func() tea.Msg {
		postings, nextPage, err := v.readSeenPage(ctx, cursor)
		return seenAppendedMsg{requestID: requestID, postings: postings, nextPage: nextPage, err: err}
	}
}

// readSeenPage reads one page of the Imbox's Previously Seen threads on their own
// route, where HEY orders them by when they were seen.
func (v *mailView) readSeenPage(ctx context.Context, cursor string) ([]mail.Posting, string, error) {
	page, err := mail.ReadSeenPage(ctx, v.vc.sdk, cursor)
	if err != nil {
		return nil, "", err
	}
	return mail.Postings(page.Postings), page.Cursor, nil
}

func (v *mailView) fetchContactThreads(ctx context.Context, requestID uint64, boxID, contactID int64) tea.Cmd {
	return func() tea.Msg {
		postings, title, nextPage, err := v.readContactThreadsPage(ctx, contactID, "")
		return bundleLoadedMsg{requestID: requestID, boxID: boxID, contactID: contactID, title: title, postings: postings, nextPage: nextPage, err: err}
	}
}

// fetchMoreContactThreads reads the page of the contact's threads below the ones on
// screen, in the growing lane and without the spinner.
func (v *mailView) fetchMoreContactThreads(ctx context.Context, requestID uint64, contactID int64, cursor string) tea.Cmd {
	return func() tea.Msg {
		postings, _, nextPage, err := v.readContactThreadsPage(ctx, contactID, cursor)
		return bundleAppendedMsg{requestID: requestID, contactID: contactID, postings: postings, nextPage: nextPage, err: err}
	}
}

// readContactThreadsPage reads one page of the threads a contact is on, titled with
// HEY's own heading for the list.
func (v *mailView) readContactThreadsPage(ctx context.Context, contactID int64, cursor string) ([]mail.Posting, string, string, error) {
	page, err := v.vc.sdk.Contacts().ThreadsPage(ctx, contactID, cursor)
	if err != nil {
		return nil, "", "", err
	}
	title := terminal.SanitizeLine(page.Contact.EntriesTitle)
	if title == "" {
		title = "All threads with " + terminal.SanitizeLine(page.Contact.Name)
	}
	return mail.Postings(page.Contact.Postings), title, page.NextPage, nil
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
		if title == "" {
			title = entries[0].Summary
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

	// The subject heads the thread, centered over the first message the way
	// the web app titles a topic.
	if subject := terminal.SanitizeLine(v.topicName); subject != "" {
		centered := lipgloss.NewStyle().Width(sepWidth).Align(lipgloss.Center).Foreground(colorBright).Bold(true).
			Render(truncateStr(subject, sepWidth))
		fmt.Fprintf(&b, "%s\n\n", centered)
		lineCount += 2
	}

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

		// A full blank line separates the header from whatever follows it —
		// the summary here, or the body's own leading blank when there is none.
		fmt.Fprintf(&b, "%s  %s\n", v.vc.styles.entryFrom.Render(terminal.SanitizeLine(from)), v.vc.styles.entryDate.Render(formatDisplayDateTime(e.CreatedAt)))
		if e.Summary != "" {
			fmt.Fprintf(&b, "\n%s\n", terminal.SanitizeLine(e.Summary))
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
