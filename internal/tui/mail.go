package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
	internalfolders "github.com/basecamp/hey-cli/internal/folders"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/models"
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

type boxesLoadedMsg []models.Box

type mailSourcesLoadedMsg struct {
	requestID uint64
	sources   []models.Box
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
	sourceKind string
	nextPage   string
	postings   []models.Posting
	err        error
}

// postingsAppendedMsg is the page below the one on screen, read because the reader
// scrolled towards the bottom of the list. It has its own lane so it can never be mistaken
// for the read the user is waiting on, and so it lands under the cursor rather than
// carrying it back to the top.
type postingsAppendedMsg struct {
	requestID  uint64
	boxID      int64
	sourceKind string
	nextPage   string
	postings   []models.Posting
	err        error
}

// postingsRefreshedMsg is a box re-read after it changed underneath the reader. It has
// its own lane so it can never be mistaken for a read the user asked for, and so a list
// that is on screen is updated in place rather than replaced.
type postingsRefreshedMsg struct {
	requestID  uint64
	boxID      int64
	sourceKind string
	nextPage   string
	postings   []models.Posting
	err        error
}

type topicLoadedMsg struct {
	requestID   uint64
	boxID       int64
	topicID     int64
	postingID   int64
	title       string
	entries     []models.Entry
	attachments []messageAttachment
	images      [][]byte
	err         error
}

type searchResultsLoadedMsg struct {
	requestID uint64
	query     string
	nextPage  int
	postings  []models.Posting
	err       error
}

// searchResultsAppendedMsg is the page of matches below the ones on screen, read because
// the reader scrolled towards the bottom of the results. Its own lane, like a box's.
type searchResultsAppendedMsg struct {
	requestID uint64
	query     string
	nextPage  int
	postings  []models.Posting
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
	postingActionIgnore
	postingActionStopIgnoring
)

type postingActionDoneMsg struct {
	action     string
	boxID      int64
	sourceKind string
	postingID  int64
	effect     postingActionEffect
	err        error
}

// postingSeenMsg reports the mark-seen that opening a thread triggers on its
// own, as the web app does out of band once it has rendered the topic.
type postingSeenMsg struct {
	boxID      int64
	sourceKind string
	postingID  int64
	err        error
}

type screenerCountLoadedMsg struct {
	count int
	err   error
}

type folderActionDoneMsg struct {
	action     string
	sourceID   int64
	sourceKind string
	created    bool
	err        error
}

type collectionActionDoneMsg struct {
	action     string
	sourceID   int64
	sourceKind string
	postingID  int64
	collection models.Collection
	added      bool
	err        error
}

// --- Mail section view ---

type mailView struct {
	vc *viewContext

	boxes    []models.Box
	boxIndex int

	postingPaging    listPaging
	postingList      contentList
	topicViewport    viewport.Model
	topicContent     string
	topicID          int64
	topicName        string
	entries          []models.Entry
	attachments      []messageAttachment
	attachmentCursor int
	imageContent     string
	inThread         bool
	loading          bool

	compose                *composeForm                // non-nil while a message, reply or forward is being written
	bulkReply              *bulkReplyForm              // non-nil while a bulk reply is being previewed or written
	movePicker             *movePicker                 // non-nil while a destination box is being selected
	folderPicker           *folderPicker               // non-nil while folder labels are being managed
	collectionPicker       *collectionMembershipPicker // non-nil while collection membership is being managed
	labels                 *labelPicker                // non-nil while a label is being chosen
	collections            *collectionNavPicker        // non-nil while a collection is being chosen
	searchForm             *mailSearchForm             // non-nil while a search query is being entered
	searchList             contentList
	searchActive           bool
	searchQuery            string
	searchNextPage         int    // the page of matches after the ones on screen, zero at the last
	searchLoadingMore      bool   // a page of matches is already on its way
	screenerCount          int    // senders waiting in The Screener
	lastBulkReplyID        int64  // delayed delivery currently available for undo
	pendingMutations       int    // writes that must finish before changing the account context
	notice                 string // one-shot confirmation shown above the posting list
	activeRequestID        uint64 // identifies the only mail read allowed to update the view
	activeRequestKind      mailRequestKind
	sourceRequestID        uint64
	folderDiscoveryErr     string
	collectionDiscoveryErr string
	requestCancel          context.CancelFunc

	liveRequestID   uint64 // identifies the only live re-read allowed to update the list
	liveRefreshDue  bool   // a re-read is already on its way
	liveUpdatesOver bool   // the changes stream closed, so the list is a snapshot again
	moreRequestID   uint64 // identifies the only page-below read allowed to grow the list
	searchMoreID    uint64 // the same, for the search results
}

func newMailView(vc *viewContext) *mailView {
	return &mailView{
		vc:            vc,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		searchList:    contentList{hideSeenState: true},
	}
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
				if source.Kind == mailSourceKindFolder {
					sources = append(sources, source)
				}
			}
		} else {
			v.folderDiscoveryErr = ""
		}
		if msg.collectionErr != nil {
			v.collectionDiscoveryErr = msg.collectionErr.Error()
			for _, source := range v.boxes {
				if source.Kind == mailSourceKindCollection {
					sources = append(sources, source)
				}
			}
		} else {
			v.collectionDiscoveryErr = ""
		}
		v.updateSourceDiscoveryNotice()
		return v.applySources(sources), true

	case boxesLoadedMsg:
		return v.applySources([]models.Box(msg)), true

	case screenerCountLoadedMsg:
		if msg.err == nil {
			v.screenerCount = msg.count
		}
		return nil, true

	case postingsLoadedMsg:
		if !v.acceptsPostingsLoaded(msg) {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		// Only the Imbox separates New for You from Previously Seen and marks
		// unread threads with the dot; every other source is one flat list. The
		// Imbox is also the only box HEY lets you cover.
		isImbox := v.currentBoxIsImbox()
		v.postingList.hideSeenState = !isImbox
		if isImbox {
			v.postingList.setCover(imboxCover())
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
			v.notice = "Could not load more mail: " + msg.err.Error()
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
			v.notice = "Could not refresh mail: " + msg.err.Error()
			return nil, true
		}
		v.postingList.refreshHead(msg.postings, v.postingPaging.headIDs)
		v.postingPaging.refreshed(postingIDs(msg.postings), msg.nextPage)
		return nil, true

	case searchResultsLoadedMsg:
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
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
			v.notice = "Could not load more results: " + msg.err.Error()
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
		if msg.requestID != v.activeRequestID || msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.inThread = true
		v.topicID = msg.topicID
		v.topicName = msg.title
		v.entries = msg.entries
		v.attachments = msg.attachments
		v.attachmentCursor = 0
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
		return tea.Batch(append(uploadCmds, v.markPostingSeen(msg.boxID, msg.postingID))...), true

	case replyContextLoadedMsg:
		if msg.requestID != v.activeRequestID || msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.compose = newReplyForm(msg, v.vc.styles)
		v.compose.resize(v.vc.width, v.vc.height)
		return v.compose.init(), true

	case forwardContextLoadedMsg:
		if msg.requestID != v.activeRequestID || msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.compose = newForwardForm(msg, v.vc.styles)
		v.compose.resize(v.vc.width, v.vc.height)
		return v.compose.init(), true

	case bulkReplyDraftLoadedMsg:
		if msg.requestID != v.activeRequestID || msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			v.notice = "Could not preview bulk reply: " + msg.err.Error()
			return nil, true
		}
		if msg.draft == nil || len(msg.draft.Entries) == 0 {
			v.notice = "No replyable threads found; nothing was sent"
			return nil, true
		}
		v.bulkReply = newBulkReplyForm(msg.postingIDs, msg.draft, v.vc.styles)
		v.bulkReply.resize(v.vc.width, v.vc.height)
		return v.bulkReply.init(), true

	case bulkReplySentMsg:
		if v.bulkReply == nil {
			return nil, true
		}
		if msg.err != nil {
			v.bulkReply.sending = false
			v.bulkReply.status = "Send failed: " + msg.err.Error()
			v.bulkReply.isError = true
			return nil, true
		}
		if msg.delivery == nil {
			v.bulkReply.sending = false
			v.bulkReply.status = "Send failed: HEY returned no delivery"
			v.bulkReply.isError = true
			return nil, true
		}
		v.bulkReply = nil
		v.postingList.clearSelected()
		count := int(msg.delivery.EntriesCount)
		v.notice = fmt.Sprintf("%d bulk %s sent", count, replyNoun(count))
		v.lastBulkReplyID = 0
		if msg.delivery.Delayed {
			v.notice = fmt.Sprintf("%d bulk %s queued with undo available", count, replyNoun(count))
			if msg.delivery.Id > 0 {
				v.lastBulkReplyID = msg.delivery.Id
				v.notice += " — press u to undo"
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
			v.notice = "Could not undo bulk reply: " + msg.err.Error()
			return nil, true
		}
		v.lastBulkReplyID = 0
		v.notice = "Bulk reply recalled"
		return nil, true

	case composeSentMsg:
		if v.compose == nil {
			return nil, true
		}
		if msg.err != nil {
			v.compose.sending = false
			v.compose.setStatus("Send failed: "+msg.err.Error(), true)
			return nil, true
		}
		v.compose = nil
		v.notice = msg.label
		return nil, true

	case attachmentSavedMsg:
		if !v.currentAttachmentAction(msg.topicID, msg.attachmentID) {
			return nil, true
		}
		if msg.err != nil {
			saveErr := apierr.AsError(msg.err)
			if saveErr.Code == "usage" && strings.HasPrefix(saveErr.Message, "destination already exists:") {
				v.notice = "Attachment already exists: " + terminalSafeAttachmentText(msg.path)
			} else {
				v.notice = "Could not save attachment: " + msg.err.Error()
			}
			return nil, true
		}
		v.notice = "Saved attachment to " + terminalSafeAttachmentText(msg.path)
		return nil, true

	case attachmentOpenedMsg:
		if !v.currentAttachmentAction(msg.topicID, msg.attachmentID) {
			return nil, true
		}
		if msg.err != nil {
			v.notice = "Could not open attachment: " + msg.err.Error()
			return nil, true
		}
		v.notice = "Opened attachment " + terminalSafeAttachmentText(msg.filename)
		return nil, true

	case postingActionDoneMsg:
		v.finishMutation()
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		if msg.boxID != v.currentBoxID() || (msg.sourceKind != "" && msg.sourceKind != v.currentSourceKind()) {
			return nil, true
		}
		v.notice = msg.action
		idx := v.postingIndex(msg.postingID)
		if idx >= 0 {
			switch msg.effect {
			case postingActionNone:
			case postingActionRemove:
				v.postingList.postings = append(v.postingList.postings[:idx], v.postingList.postings[idx+1:]...)
				if v.postingList.cursor > idx {
					v.postingList.cursor--
				}
				if v.postingList.cursor >= len(v.postingList.postings) && v.postingList.cursor > 0 {
					v.postingList.cursor--
				}
				v.postingList.ensureVisible()
			case postingActionSeen:
				v.postingList.markSeen(idx)
			case postingActionIgnore:
				v.postingList.postings[idx].Muted = true
			case postingActionStopIgnoring:
				v.postingList.postings[idx].Muted = false
			}
		}
		if v.activeRequestKind == mailRequestPostings {
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
			v.notice = "Could not mark thread as seen: " + msg.err.Error()
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
				v.notice = "Could not update labels: " + msg.err.Error()
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
			v.notice = "Could not update collections: " + terminalSafeCollectionText(msg.err.Error())
			return nil, true
		}
		v.notice = msg.action
		if index := v.postingIndex(msg.postingID); index >= 0 {
			v.updatePostingCollection(index, msg.collection, msg.added)
			if !msg.added && msg.sourceKind == mailSourceKindCollection && msg.collection.ID == msg.sourceID {
				v.removePostingAt(index)
			}
		}
		if msg.sourceKind == mailSourceKindCollection && msg.collection.ID == msg.sourceID {
			if source := v.currentSource(); source != nil {
				return v.requestPostings(*source), true
			}
		}
		return nil, true
	}

	// Cursor blinks and other component messages go to the open form. The
	// form owns the message while it is open, whether or not it yields a cmd.
	if v.compose != nil {
		return v.compose.update(msg), true
	}
	if v.bulkReply != nil {
		return v.bulkReply.update(msg), true
	}
	if v.searchForm != nil {
		return v.searchForm.update(msg), true
	}
	if v.folderPicker != nil && v.folderPicker.creating {
		return v.folderPicker.update(msg), true
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
	if v.compose != nil {
		return v.compose.view()
	}
	if v.bulkReply != nil {
		return v.bulkReply.view()
	}
	if v.searchForm != nil {
		return v.searchForm.view()
	}
	if v.movePicker != nil {
		return v.movePicker.view(v.vc.styles, v.vc.width)
	}
	if v.folderPicker != nil {
		return v.folderPicker.view(v.vc.styles, v.vc.width)
	}
	if v.collectionPicker != nil {
		return v.collectionPicker.view(v.vc.styles, v.vc.width)
	}
	if v.labels != nil {
		base := v.listHeader() + v.postingList.view()
		return v.labels.overlay(base, v.vc.width, v.vc.height)
	}
	if v.collections != nil {
		base := v.listHeader() + v.postingList.view()
		return v.collections.overlay(base, v.vc.width, v.vc.height)
	}
	if v.inThread {
		if v.notice != "" {
			return v.vc.styles.title.Render(v.notice) + "\n" + v.topicViewport.View()
		}
		return v.topicViewport.View()
	}
	if v.searchActive {
		if v.notice != "" {
			return v.vc.styles.title.Render(v.notice) + "\n" + v.searchList.view()
		}
		return v.searchList.view()
	}
	return v.listHeader() + v.postingList.view()
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
		v.notice = "Could not load labels or collections — press g or k to retry"
	case v.folderDiscoveryErr != "":
		v.notice = "Could not load labels — press g to retry"
	case v.collectionDiscoveryErr != "":
		v.notice = "Could not load collections — press k to retry"
	case v.notice == "Retrying labels…" || v.notice == "Retrying collections…":
		v.notice = ""
	}
}

// CapturingInput reports whether a form or picker is open and wants every key.
func (v *mailView) CapturingInput() bool {
	return v.compose != nil || v.bulkReply != nil || v.movePicker != nil || v.folderPicker != nil || v.collectionPicker != nil || v.labels != nil || v.collections != nil || v.searchForm != nil
}

func (v *mailView) AccountSwitchBlocked() bool {
	return v.pendingMutations > 0
}

func (v *mailView) HelpBindings() []helpBinding {
	if v.compose != nil {
		return v.compose.helpBindings()
	}
	if v.bulkReply != nil {
		return v.bulkReply.helpBindings()
	}
	if v.searchForm != nil {
		return v.searchForm.helpBindings()
	}
	if v.movePicker != nil {
		return v.movePicker.helpBindings()
	}
	if v.folderPicker != nil {
		return v.folderPicker.helpBindings()
	}
	if v.collectionPicker != nil {
		return v.collectionPicker.helpBindings()
	}
	if v.labels != nil {
		return v.labels.helpBindings()
	}
	if v.collections != nil {
		return v.collections.helpBindings()
	}
	if v.inThread {
		bindings := []helpBinding{{"r", "reply"}, {"f", "forward"}}
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
	folderBinding := helpBinding{"g", "labels"}
	if v.folderDiscoveryErr != "" {
		folderBinding = helpBinding{"g", "retry labels"}
	}
	collectionBinding := helpBinding{"k", "collections"}
	if v.collectionDiscoveryErr != "" {
		collectionBinding = helpBinding{"k", "retry collections"}
	}
	bindings := []helpBinding{
		{"/", "search"},
		{"ctrl+s", "screener"},
		{"c", "compose"},
		{"space", "select"},
		{"b", "bulk reply"},
		{"r", "reply"},
		{"f", "forward"},
		{"m", "move"},
		folderBinding,
		collectionBinding,
		{"e", "seen"},
		{"l", "reply later"},
		{"a", "set aside"},
		{"d", "feed"},
	}
	bindings = append(bindings,
		helpBinding{"p", "paper trail"},
		helpBinding{"t", "trash"},
		helpBinding{"s", "spam"},
		ignoreBinding,
		helpBinding{"ctrl+r", "reload"},
	)
	if v.postingList.cover != coverNone {
		peek := helpBinding{"v", "peek under cover"}
		if v.postingList.coverPeeked {
			peek = helpBinding{"v", "cover"}
		}
		bindings = append(bindings, peek)
	}
	if v.lastBulkReplyID != 0 {
		bindings = append(bindings, helpBinding{"u", "undo bulk reply"})
	}
	return bindings
}

func (v *mailView) SubnavItems() ([]navItem, int, string, bool) {
	if v.searchActive || v.searchForm != nil {
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
		label = v.boxes[v.boxIndex].Name
		if isOrganizedMailSource(v.boxes[v.boxIndex].Kind) {
			label = terminalSafeFolderText(label)
		}
		if v.postingPaging.loading {
			label += " · loading more…"
		}
	}

	// Labels and Collections each use one tab whose modal chooses the source.
	tabIndexes := v.tabBoxIndexes()
	boxes := make([]models.Box, len(tabIndexes))
	selected := 0
	for i, boxIndex := range tabIndexes {
		boxes[i] = v.boxes[boxIndex]
		if boxIndex == v.boxIndex {
			selected = i
		}
	}
	items := boxNavItems(boxes)
	if v.hasLabels() {
		items = append(items, navItem{shortcut: "L", label: "Labels"})
		if v.currentSourceKind() == mailSourceKindFolder {
			selected = len(items) - 1
		}
	}
	if v.hasCollections() {
		items = append(items, navItem{shortcut: "K", label: "Collections"})
		if v.currentSourceKind() == mailSourceKindCollection {
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
	return v.hasSourceKind(mailSourceKindFolder)
}

func (v *mailView) hasCollections() bool {
	return v.hasSourceKind(mailSourceKindCollection)
}

func (v *mailView) hasSourceKind(kind string) bool {
	for _, source := range v.boxes {
		if source.Kind == kind {
			return true
		}
	}
	return false
}

func (v *mailView) openLabels() {
	v.labels = newLabelPicker(v.boxes, v.boxIndex)
}

func (v *mailView) openCollections() {
	v.collections = newCollectionNavPicker(v.boxes, v.boxIndex)
}

func (v *mailView) SubnavLeft() tea.Cmd {
	if v.searchActive || v.searchForm != nil {
		return nil
	}
	tabIndexes := v.tabBoxIndexes()
	switch v.currentSourceKind() {
	case mailSourceKindCollection:
		if v.hasLabels() {
			v.openLabels()
			return nil
		}
		if len(tabIndexes) > 0 {
			return v.switchBox(tabIndexes[len(tabIndexes)-1])
		}
		return nil
	case mailSourceKindFolder:
		if len(tabIndexes) > 0 {
			return v.switchBox(tabIndexes[len(tabIndexes)-1])
		}
		return nil
	}
	for i, boxIndex := range tabIndexes {
		if boxIndex == v.boxIndex && i > 0 {
			return v.switchBox(tabIndexes[i-1])
		}
	}
	return nil
}

func (v *mailView) SubnavRight() tea.Cmd {
	if v.searchActive || v.searchForm != nil {
		return nil
	}
	switch v.currentSourceKind() {
	case mailSourceKindFolder:
		if v.hasCollections() {
			v.openCollections()
		} else {
			v.openLabels()
		}
		return nil
	case mailSourceKindCollection:
		v.openCollections()
		return nil
	}
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
	return nil
}

func (v *mailView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	v.notice = ""

	if v.compose != nil {
		if msg.Key().Code == tea.KeyEscape && !v.compose.sending {
			v.compose = nil
			return nil
		}
		cmd, submit := v.compose.handleKey(msg)
		if submit {
			return v.send()
		}
		return cmd
	}

	if v.bulkReply != nil {
		if msg.Key().Code == tea.KeyEscape && !v.bulkReply.sending {
			v.bulkReply = nil
			return nil
		}
		cmd, submit := v.bulkReply.handleKey(msg)
		if submit {
			return v.sendBulkReply()
		}
		return cmd
	}

	if v.searchForm != nil {
		if msg.Key().Code == tea.KeyEscape {
			v.searchForm = nil
			return nil
		}
		cmd, query, submit := v.searchForm.handleKey(msg)
		if submit {
			v.searchForm = nil
			return v.requestSearch(query)
		}
		return cmd
	}

	if v.movePicker != nil {
		if msg.Key().Code == tea.KeyEscape {
			v.movePicker = nil
			return nil
		}
		if msg.Key().Code == tea.KeyEnter {
			picker := v.movePicker
			destination := picker.selected()
			v.movePicker = nil
			if destination == nil {
				return nil
			}
			return v.movePostingToBox(picker.postingID, *destination)
		}
		v.movePicker.update(msg)
		return nil
	}

	if v.labels != nil {
		switch msg.Key().Code {
		case tea.KeyEscape:
			v.labels = nil
			return nil
		case tea.KeyEnter:
			index := v.labels.selectedBoxIndex()
			v.labels = nil
			if index < 0 {
				return nil
			}
			return v.switchBox(index)
		}
		v.labels.update(msg)
		return nil
	}

	if v.collections != nil {
		switch msg.Key().Code {
		case tea.KeyEscape:
			v.collections = nil
			return nil
		case tea.KeyEnter:
			index := v.collections.selectedSourceIndex()
			v.collections = nil
			if index < 0 {
				return nil
			}
			return v.switchBox(index)
		}
		v.collections.update(msg)
		return nil
	}

	if v.folderPicker != nil {
		picker := v.folderPicker
		if msg.Key().Code == tea.KeyEscape {
			if picker.creating {
				picker.cancelCreate()
				return nil
			}
			v.folderPicker = nil
			return nil
		}
		if picker.creating {
			if msg.Key().Code == tea.KeyEnter {
				name, ok := picker.createName()
				if !ok {
					return nil
				}
				v.folderPicker = nil
				return v.createFolderForPosting(picker.posting.ID, name)
			}
			return picker.handleKey(msg)
		}
		if msg.Key().Code == tea.KeyEnter {
			selection := picker.selected()
			if selection == nil {
				return nil
			}
			switch selection.kind {
			case folderPickerExisting:
				v.folderPicker = nil
				if picker.postingHasFolder(selection.folder.ID) {
					return v.unfilePosting(picker.posting.ID, selection.folder.ID, selection.folder.Name)
				}
				return v.filePosting(picker.posting.ID, selection.folder.ID, selection.folder.Name)
			case folderPickerCreate:
				return picker.startCreate()
			case folderPickerRemoveAll:
				v.folderPicker = nil
				return v.unfilePosting(picker.posting.ID, 0, "")
			}
		}
		return picker.handleKey(msg)
	}

	if v.collectionPicker != nil {
		picker := v.collectionPicker
		if msg.Key().Code == tea.KeyEscape {
			v.collectionPicker = nil
			return nil
		}
		if msg.Key().Code == tea.KeyEnter {
			collection := picker.selected()
			if collection == nil {
				return nil
			}
			topicID := picker.posting.ResolveTopicID()
			if topicID == 0 {
				v.collectionPicker = nil
				v.notice = "This item does not identify an email thread"
				return nil
			}
			v.collectionPicker = nil
			if picker.postingHasCollection(collection.ID) {
				return v.removePostingFromCollection(picker.posting.ID, topicID, *collection)
			}
			return v.addPostingToCollection(picker.posting.ID, topicID, *collection)
		}
		picker.update(msg)
		return nil
	}

	if v.inThread {
		switch msg.String() {
		case "r":
			if v.topicID != 0 {
				return v.loadReplyContext(v.topicID, v.topicName)
			}
		case "f":
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
			if msg.String() == "/" {
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
		case "/":
			return v.startSearch()
		case "c":
			return v.startCompose()
		case " ", "space":
			v.postingList.toggleSelected()
			return nil
		case "b":
			return v.startBulkReply()
		case "u":
			return v.undoBulkReply()
		case "m":
			v.startMove()
			return nil
		case "g":
			return v.startFolderPicker()
		case "k":
			return v.startCollectionPicker()
		case "v":
			v.postingList.toggleCoverPeek()
			return nil
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
	if key == "q" && v.searchActive && !v.inThread && (v.activeRequestKind == mailRequestTopic || v.activeRequestKind == mailRequestSearch) {
		v.cancelRequest()
		v.clearSearch()
		return
	}
	v.ExitThread()
}

func (v *mailView) ExitThread() {
	if v.searchActive && !v.inThread && (v.activeRequestKind == mailRequestTopic || v.activeRequestKind == mailRequestSearch) {
		v.cancelRequest()
		return
	}
	if v.inThread {
		v.inThread = false
		v.compose = nil
		v.movePicker = nil
		v.folderPicker = nil
		v.collectionPicker = nil
		v.cancelRequest()
		return
	}
	v.clearSearch()
	v.cancelRequest()
}

func (v *mailView) clearSearch() {
	v.searchActive = false
	v.searchQuery = ""
	v.notice = ""
	v.searchNextPage = 0
	v.searchLoadingMore = false
	v.searchMoreID++
	v.searchList.setPostings(nil)
	v.searchForm = nil
}

func (v *mailView) CancelPendingDetail() bool {
	if v.activeRequestKind != mailRequestTopic && v.activeRequestKind != mailRequestReply && v.activeRequestKind != mailRequestForward && v.activeRequestKind != mailRequestSearch && v.activeRequestKind != mailRequestBulkReply {
		return false
	}
	v.cancelRequest()
	return true
}

func (v *mailView) Loading() bool { return v.loading }

// Restyle rebuilds the cached thread content and hands the new styles to any open
// form. The Kitty image placeholders in imageContent encode image IDs as colors, so
// they are reused as-is rather than recolored.
func (v *mailView) Restyle() {
	if v.inThread {
		offset := v.topicViewport.YOffset()
		v.rebuildTopicContent()
		v.topicViewport.SetYOffset(offset)
	}
	if v.compose != nil {
		v.compose.styles = v.vc.styles
	}
	if v.searchForm != nil {
		v.searchForm.styles = v.vc.styles
	}
	if v.bulkReply != nil {
		v.bulkReply.styles = v.vc.styles
		v.bulkReply.resize(v.bulkReply.width, v.bulkReply.height)
	}
}

func (v *mailView) Resize(width, height int) {
	if v.compose != nil {
		v.compose.resize(width, height)
	}
	if v.bulkReply != nil {
		v.bulkReply.resize(width, height)
	}
	if v.searchForm != nil {
		v.searchForm.resize(width, height)
	}
	if v.folderPicker != nil {
		v.folderPicker.resize(width, height)
	}
	if v.collectionPicker != nil {
		v.collectionPicker.resize(height)
	}
	v.postingList.setSize(width, height)
	v.searchList.setSize(width, height)
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
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
	}
	return v.switchBox(boxForShortcut(key, v.boxes))
}

func (v *mailView) switchBox(index int) tea.Cmd {
	if index < 0 || index >= len(v.boxes) || index == v.boxIndex {
		return nil
	}
	v.inThread = false
	v.clearSearch()
	v.cancelRequest()
	v.notice = ""
	v.postingList.setPostings(nil)
	v.boxIndex = index
	return v.requestPostings(v.boxes[index])
}

func (v *mailView) currentSource() *models.Box {
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

// currentBoxIsImbox reports whether the active source is the Imbox.
func (v *mailView) currentBoxIsImbox() bool {
	source := v.currentSource()
	if source == nil {
		return false
	}
	return strings.EqualFold(source.Kind, hey.BoxKindImbox) ||
		(!isOrganizedMailSource(source.Kind) && strings.EqualFold(source.Name, "Imbox"))
}

func (v *mailView) currentSourceKind() string {
	if source := v.currentSource(); source != nil {
		return source.Kind
	}
	return ""
}

func (v *mailView) currentSourceIdentity() (int64, string) {
	if source := v.currentSource(); source != nil {
		return source.ID, source.Kind
	}
	return 0, ""
}

func sourceIndex(sources []models.Box, id int64, kind string) int {
	for i, source := range sources {
		if source.ID == id && source.Kind == kind {
			return i
		}
	}
	return 0
}

func (v *mailView) applySources(sources []models.Box) tea.Cmd {
	currentID, currentKind := v.currentSourceIdentity()
	v.boxes = orderBoxes(sources)
	v.loading = false
	if len(v.boxes) == 0 {
		return nil
	}
	v.boxIndex = sourceIndex(v.boxes, currentID, currentKind)
	return v.requestPostings(v.boxes[v.boxIndex])
}

func (v *mailView) requestSources() tea.Cmd {
	v.sourceRequestID++
	v.loading = true
	return v.fetchSources(v.sourceRequestID)
}

func (v *mailView) beginRequest(kind mailRequestKind) (uint64, context.Context) {
	if v.requestCancel != nil {
		v.requestCancel()
	}
	v.activeRequestID++
	ctx, cancel := context.WithCancel(v.vc.ctx)
	v.activeRequestKind = kind
	v.requestCancel = cancel
	v.loading = true
	return v.activeRequestID, ctx
}

func (v *mailView) acceptsPostingsLoaded(msg postingsLoadedMsg) bool {
	return msg.requestID == v.activeRequestID &&
		msg.boxID == v.currentBoxID() &&
		(msg.sourceKind == "" || msg.sourceKind == v.currentSourceKind())
}

func (v *mailView) finishRequest(requestID uint64) {
	if requestID != v.activeRequestID {
		return
	}
	if v.requestCancel != nil {
		v.requestCancel()
	}
	v.activeRequestKind = mailRequestNone
	v.requestCancel = nil
	v.loading = false
}

func (v *mailView) cancelRequest() {
	if v.requestCancel != nil {
		v.requestCancel()
	}
	v.activeRequestID++
	v.activeRequestKind = mailRequestNone
	v.requestCancel = nil
	v.loading = false
}

// requestPostings reads a source from its top page. Every list starts there and grows
// downwards from it, so a read the user asked for is also what puts the list back to the
// depth it opens at.
func (v *mailView) requestPostings(source models.Box) tea.Cmd {
	v.postingPaging.reset()
	v.moreRequestID++
	requestID, ctx := v.beginRequest(mailRequestPostings)
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

// noteFailure keeps the reason to a line. `hey watch` is where the whole of it is.
func (v *mailView) noteFailure(what string, err error) {
	v.notice = truncateToWidth(what+": "+err.Error(), max(v.vc.width-2, 40))
}

func (v *mailView) startSearch() tea.Cmd {
	if v.loading || len(v.boxes) == 0 {
		return nil
	}
	v.searchForm = newMailSearchForm(v.searchQuery, v.vc.styles)
	v.searchForm.resize(v.vc.width, v.vc.height)
	return v.searchForm.init()
}

// requestSearch runs a search from its first page. Results grow downwards from there, the
// same way a box does.
func (v *mailView) requestSearch(query string) tea.Cmd {
	v.searchNextPage = 0
	v.searchLoadingMore = false
	v.searchMoreID++
	requestID, ctx := v.beginRequest(mailRequestSearch)
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
	requestID, ctx := v.beginRequest(mailRequestTopic)
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
	if v.postingList.cursor >= len(v.postingList.postings) && v.postingList.cursor > 0 {
		v.postingList.cursor--
	}
	v.postingList.ensureVisible()
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

func (v *mailView) rebuildTopicContent() {
	v.topicContent = v.renderEntries(v.entries) + v.imageContent
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
	topicID := selected.ResolveTopicID()
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

func (v *mailView) openedPosting(postingID int64) *models.Posting {
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
	v.movePicker = picker
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
	v.folderPicker = newFolderPicker(*selected, v.boxes)
	v.folderPicker.resize(v.vc.width, v.vc.height)
	return nil
}

func (v *mailView) filePosting(postingID, folderID int64, folderName string) tea.Cmd {
	return v.doFolderAction("Label "+terminalSafeFolderText(folderName)+" added", false, func() error {
		return v.vc.sdk.Postings().File(v.vc.ctx, folderID, postingID)
	})
}

func (v *mailView) createFolderForPosting(postingID int64, folderName string) tea.Cmd {
	return v.doFolderAction("Label "+terminalSafeFolderText(folderName)+" created", true, func() error {
		return v.vc.sdk.Postings().CreateFolder(v.vc.ctx, folderName, postingID)
	})
}

func (v *mailView) unfilePosting(postingID, folderID int64, folderName string) tea.Cmd {
	label := "All labels removed"
	if folderID != 0 {
		label = "Label " + terminalSafeFolderText(folderName) + " removed"
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
	if selected.ResolveTopicID() == 0 {
		v.notice = "This item does not identify an email thread"
		return nil
	}
	picker := newCollectionMembershipPicker(*selected, v.boxes)
	if len(picker.collections) == 0 {
		v.notice = "No collections available"
		return nil
	}
	picker.resize(v.vc.height)
	v.collectionPicker = picker
	return nil
}

func (v *mailView) addPostingToCollection(postingID, topicID int64, collection models.Collection) tea.Cmd {
	label := "Added to collection " + terminalSafeCollectionText(collection.Name)
	return v.doCollectionAction(label, postingID, topicID, collection, true)
}

func (v *mailView) removePostingFromCollection(postingID, topicID int64, collection models.Collection) tea.Cmd {
	label := "Removed from collection " + terminalSafeCollectionText(collection.Name)
	return v.doCollectionAction(label, postingID, topicID, collection, false)
}

func (v *mailView) doCollectionAction(label string, postingID, topicID int64, collection models.Collection, added bool) tea.Cmd {
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

func (v *mailView) updatePostingCollection(index int, collection models.Collection, added bool) {
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

func (v *mailView) movePostingToBox(postingID int64, destination models.Box) tea.Cmd {
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
	case "l":
		return v.moveSelectedToKnownBox("Reply Later", hey.BoxKindLater, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToReplyLater(v.vc.ctx, p.ID)
		})
	case "a":
		return v.moveSelectedToKnownBox("Set Aside", hey.BoxKindSetAside, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToSetAside(v.vc.ctx, p.ID)
		})
	case "e":
		return v.doPostingAction("Thread marked as seen", postingActionSeen, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MarkSeen(v.vc.ctx, []int64{p.ID})
		})
	case "d":
		return v.moveSelectedToKnownBox("The Feed", hey.BoxKindFeed, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToFeed(v.vc.ctx, p.ID)
		})
	case "p":
		return v.moveSelectedToKnownBox("Paper Trail", hey.BoxKindTrail, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToPaperTrail(v.vc.ctx, p.ID)
		})
	case "t":
		return v.doPostingAction("Thread moved to Trash", postingActionRemove, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToTrash(v.vc.ctx, p.ID)
		})
	case "s":
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
	case "r":
		topicID := p.ResolveTopicID()
		if topicID == 0 {
			topicID = p.ID
		}
		return v.loadReplyContext(topicID, p.Summary)
	case "f":
		topicID := p.ResolveTopicID()
		if topicID == 0 {
			topicID = p.ID
		}
		return v.loadForwardContext(topicID, p.Summary)
	}
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

func (v *mailView) movesOutOfCurrentBox(destinationKind string) bool {
	if v.boxIndex < 0 || v.boxIndex >= len(v.boxes) {
		return true
	}
	return !strings.EqualFold(v.boxes[v.boxIndex].Kind, destinationKind)
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

func sdkBoxToModel(b generated.Box) models.Box {
	return models.Box{ID: b.Id, Kind: b.Kind, Name: b.Name}
}

func sdkPostingToModel(p generated.Posting) models.Posting {
	folders := make([]models.Folder, len(p.Folders))
	for i, folder := range p.Folders {
		folders[i] = models.Folder{ID: folder.Id, Name: folder.Name, AppURL: folder.AppUrl}
	}
	collections := make([]models.Collection, len(p.Collections))
	for i, collection := range p.Collections {
		collections[i] = models.Collection{ID: collection.Id, Name: collection.Name, AppURL: collection.AppUrl}
	}
	return models.Posting{
		ID:                    p.Id,
		CreatedAt:             formatTimestamp(p.CreatedAt),
		UpdatedAt:             formatTimestamp(p.UpdatedAt),
		Kind:                  p.Kind,
		Name:                  p.Name,
		Seen:                  p.Seen,
		BubbledUp:             p.BubbledUp,
		Bundled:               p.Bundled,
		Muted:                 p.Muted,
		Summary:               p.Summary,
		EntryKind:             p.EntryKind,
		AppURL:                p.AppUrl,
		AlternativeSenderName: p.AlternativeSenderName,
		VisibleEntryCount:     p.VisibleEntryCount,
		Extenzions:            sdkExtenzionsToModel(p.Extenzions),
		Folders:               folders,
		Collections:           collections,
		Creator: models.Contact{
			ID:           p.Creator.Id,
			Name:         p.Creator.Name,
			EmailAddress: p.Creator.EmailAddress,
		},
	}
}

func sdkSearchMatchToModel(match generated.SearchMatch) models.Posting {
	posting := models.Posting{
		ID:        match.PostingId,
		TopicID:   match.Topic.Id,
		Name:      match.Topic.Name,
		AppURL:    match.Topic.AppUrl,
		CreatedAt: formatTimestamp(match.Topic.UpdatedAt),
		UpdatedAt: formatTimestamp(match.Topic.UpdatedAt),
		Creator: models.Contact{
			ID:           match.Topic.Creator.Id,
			Name:         match.Topic.Creator.Name,
			EmailAddress: match.Topic.Creator.EmailAddress,
		},
	}
	if len(match.Entries) > 0 {
		entry := match.Entries[0]
		posting.CreatedAt = formatTimestamp(entry.CreatedAt)
		posting.Summary = entry.Summary
		posting.AlternativeSenderName = entry.AlternativeSenderName
		posting.Creator = models.Contact{
			ID:           entry.Creator.Id,
			Name:         entry.Creator.Name,
			EmailAddress: entry.Creator.EmailAddress,
		}
	}
	return posting
}

func sdkExtenzionsToModel(exts []generated.Extenzion) []models.Extenzion {
	if len(exts) == 0 {
		return nil
	}
	result := make([]models.Extenzion, len(exts))
	for i, e := range exts {
		result[i] = models.Extenzion{ID: e.Id, Name: e.Name}
	}
	return result
}

func sdkMessageToEntry(entry generated.Entry, message generated.Message) models.Entry {
	creator := entry.Creator
	if creator.Id == 0 {
		creator = message.Creator
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = message.CreatedAt
	}
	updatedAt := entry.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = message.UpdatedAt
	}
	summary := entry.Summary
	if summary == "" {
		summary = message.Subject
	}
	appURL := entry.AppUrl
	if appURL == "" {
		appURL = message.Url
	}

	return models.Entry{
		ID:                    entry.Id,
		CreatedAt:             formatTimestamp(createdAt),
		UpdatedAt:             formatTimestamp(updatedAt),
		AlternativeSenderName: entry.AlternativeSenderName,
		Summary:               summary,
		Kind:                  entry.Kind,
		AppURL:                appURL,
		Body:                  htmlToMarkdown(message.Content),
		BodyHTML:              message.Content,
		Creator: models.Contact{
			ID:           creator.Id,
			Name:         creator.Name,
			EmailAddress: creator.EmailAddress,
		},
	}
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
		boxes := make([]models.Box, 0, len(sdkBoxes))
		for _, box := range sdkBoxes {
			boxes = append(boxes, sdkBoxToModel(box))
		}

		folders, folderErr := internalfolders.List(v.vc.ctx, v.vc.sdk)
		if folderErr == nil {
			for _, label := range folders {
				boxes = append(boxes, models.Box{ID: label.ID, Kind: mailSourceKindFolder, Name: label.Name, AppURL: label.AppURL})
			}
		}
		collections, collectionErr := v.vc.sdk.Collections().List(v.vc.ctx)
		if collectionErr == nil && collections != nil {
			for _, collection := range *collections {
				boxes = append(boxes, models.Box{ID: collection.Id, Kind: mailSourceKindCollection, Name: collection.Name, AppURL: collection.AppUrl})
			}
		}
		message := mailSourcesLoadedMsg{requestID: requestID, sources: boxes, folderErr: folderErr, collectionErr: collectionErr}
		if screener, err := v.vc.sdk.Clearances().Summary(v.vc.ctx); err == nil && screener != nil {
			message.screenerCount = int(screener.PendingClearancesCount)
			message.screenerStream = screener.SignedStreamName
		}
		return message
	}
}

func (v *mailView) refreshScreenerCount() tea.Cmd {
	return func() tea.Msg {
		count, err := v.vc.sdk.Clearances().PendingCount(v.vc.ctx)
		return screenerCountLoadedMsg{count: count, err: err}
	}
}

func (v *mailView) fetchPostings(ctx context.Context, requestID uint64, source models.Box, page string) tea.Cmd {
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
func (v *mailView) fetchMorePostings(ctx context.Context, requestID uint64, source models.Box, page string) tea.Cmd {
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
func (v *mailView) fetchBoxRefresh(ctx context.Context, requestID uint64, source models.Box) tea.Cmd {
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
func (v *mailView) readPostingsPage(ctx context.Context, source models.Box, page string) ([]models.Posting, string, error) {
	var sdkPostings []generated.Posting
	var nextPage string

	switch source.Kind {
	case mailSourceKindFolder:
		var params *generated.GetFolderParams
		if page != "" {
			params = &generated.GetFolderParams{Page: &page}
		}
		result, err := v.vc.sdk.Folders().GetPage(ctx, source.ID, params)
		if err != nil {
			return nil, "", err
		}
		if result != nil {
			nextPage = result.NextPage
			if result.Folder != nil {
				sdkPostings = result.Folder.Postings
			}
		}
	case mailSourceKindCollection:
		var params *generated.GetCollectionParams
		if page != "" {
			params = &generated.GetCollectionParams{Page: &page}
		}
		result, err := v.vc.sdk.Collections().GetPage(ctx, source.ID, params)
		if err != nil {
			return nil, "", err
		}
		if result != nil {
			nextPage = result.NextPage
			if result.Collection != nil {
				sdkPostings = result.Collection.Postings
			}
		}
	default:
		var params *generated.GetBoxParams
		if page != "" {
			params = &generated.GetBoxParams{Page: &page}
		}
		result, err := v.vc.sdk.Boxes().GetPage(ctx, source.ID, params)
		if err != nil {
			return nil, "", err
		}
		if result != nil {
			nextPage = result.NextPage
			if result.Box != nil {
				sdkPostings = result.Box.Postings
			}
		}
	}

	postings := make([]models.Posting, 0, len(sdkPostings))
	for _, posting := range sdkPostings {
		postings = append(postings, sdkPostingToModel(posting))
	}
	return postings, nextPage, nil
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
func (v *mailView) readSearchPage(ctx context.Context, query string, page int) ([]models.Posting, int, error) {
	results, err := v.vc.sdk.Search().SearchPage(ctx, hey.SearchParams{Query: query, Page: max(page, 1)})
	if err != nil {
		return nil, 0, err
	}
	var matches []generated.SearchMatch
	if results != nil && results.Result != nil {
		matches = results.Result.Matches
	}
	postings := make([]models.Posting, 0, len(matches))
	for _, match := range matches {
		postings = append(postings, sdkSearchMatchToModel(match))
	}
	nextPage := 0
	if results != nil {
		nextPage = results.NextPage
	}
	return postings, nextPage, nil
}

func (v *mailView) fetchTopic(ctx context.Context, requestID uint64, boxID, topicID, postingID int64, title string) tea.Cmd {
	return func() tea.Msg {
		topic, err := v.vc.sdk.Topics().Get(ctx, topicID)
		if err != nil {
			return topicLoadedMsg{requestID: requestID, boxID: boxID, topicID: topicID, title: title, err: err}
		}
		if topic == nil {
			return topicLoadedMsg{requestID: requestID, boxID: boxID, topicID: topicID, title: title, err: fmt.Errorf("topic %d returned no data", topicID)}
		}

		messages := make([]generated.Message, len(topic.Entries))
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(maxConcurrentMessageFetches)
		for i, entry := range topic.Entries {
			group.Go(func() error {
				message, getErr := v.vc.sdk.Messages().Get(groupCtx, entry.Id)
				if getErr != nil {
					return fmt.Errorf("get message %d: %w", entry.Id, getErr)
				}
				if message == nil {
					return fmt.Errorf("message %d returned no data", entry.Id)
				}
				messages[i] = *message
				return nil
			})
		}
		if getErr := group.Wait(); getErr != nil {
			return topicLoadedMsg{
				requestID: requestID,
				boxID:     boxID,
				topicID:   topicID,
				title:     title,
				err:       getErr,
			}
		}

		entries := make([]models.Entry, len(topic.Entries))
		var attachments []messageAttachment
		for i, entry := range topic.Entries {
			entries[i] = sdkMessageToEntry(entry, messages[i])
			for position, attachment := range htmlutil.ExtractAttachments(messages[i].Content) {
				attachments = append(attachments, messageAttachment{
					ID:          fmt.Sprintf("%d:%d", entry.Id, position+1),
					MessageID:   entry.Id,
					Filename:    attachment.Filename,
					ContentType: attachment.ContentType,
					ByteSize:    attachment.ByteSize,
					URL:         attachment.URL,
				})
			}
		}

		var images [][]byte
		if v.vc.imageRenderer.protocol() == imageProtocolKitty && v.vc.imageFetcher != nil {
			for _, entry := range entries {
				for _, imageURL := range extractImageURLs(entry.BodyHTML) {
					data, fetchErr := v.vc.imageFetcher.Fetch(ctx, imageURL)
					if fetchErr == nil && len(data) > 0 {
						images = append(images, data)
					}
				}
			}
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
		}
	}
}

// --- Entry rendering ---

func (v *mailView) renderEntries(entries []models.Entry) string {
	var b strings.Builder
	sepWidth := max(v.vc.width-4, 40)
	sep := v.vc.styles.separator.Render(strings.Repeat("─", sepWidth))

	for i, e := range entries {
		if i > 0 {
			fmt.Fprintf(&b, "%s\n", sep)
		}

		from := e.Creator.Name
		if from == "" {
			from = e.Creator.EmailAddress
		}
		if e.AlternativeSenderName != "" {
			from = e.AlternativeSenderName
		}

		date := ""
		if len(e.CreatedAt) >= 16 {
			date = e.CreatedAt[:16]
		}

		fmt.Fprintf(&b, "%s  %s\n", v.vc.styles.entryFrom.Render(from), v.vc.styles.entryDate.Render(date))
		if e.Summary != "" {
			fmt.Fprintf(&b, "%s\n", e.Summary)
		}
		if e.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", v.vc.styles.entryBody.Render(markdown.Render(e.Body, sepWidth)))
		}
		entryAttachments := attachmentsForMessage(v.attachments, e.ID)
		if panel := renderAttachmentPanel(entryAttachments, selectedAttachmentForMessage(v.attachments, v.attachmentCursor, e.ID)); panel != "" {
			fmt.Fprintf(&b, "\n%s\n", panel)
		}
		b.WriteString("\n")
	}

	return b.String()
}
