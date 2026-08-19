package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

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
)

type boxesLoadedMsg []models.Box

type postingsLoadedMsg struct {
	requestID uint64
	boxID     int64
	postings  []models.Posting
	err       error
}

type topicLoadedMsg struct {
	requestID uint64
	boxID     int64
	topicID   int64
	title     string
	entries   []models.Entry
	images    [][]byte
	err       error
}

type postingActionDoneMsg struct {
	action    string
	boxID     int64
	postingID int64
	removes   bool
	err       error
}

// --- Mail section view ---

type mailView struct {
	vc *viewContext

	boxes    []models.Box
	boxIndex int

	postingList   contentList
	topicViewport viewport.Model
	topicContent  string
	topicID       int64
	topicName     string
	inThread      bool
	loading       bool

	compose           *composeForm // non-nil while a new message or reply is being written
	notice            string       // one-shot confirmation shown above the posting list
	activeRequestID   uint64       // identifies the only mail read allowed to update the view
	activeRequestKind mailRequestKind
	requestCancel     context.CancelFunc
}

func newMailView(vc *viewContext) *mailView {
	return &mailView{
		vc:            vc,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *mailView) Init() tea.Cmd {
	if len(v.boxes) == 0 {
		v.loading = true
		return v.fetchBoxes()
	}
	if v.boxIndex < len(v.boxes) {
		return v.requestPostings(v.boxes[v.boxIndex].ID)
	}
	return nil
}

func (v *mailView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case boxesLoadedMsg:
		v.boxes = orderBoxes([]models.Box(msg))
		v.loading = false
		if len(v.boxes) > 0 {
			v.boxIndex = 0
			return v.requestPostings(v.boxes[0].ID), true
		}
		return nil, true

	case postingsLoadedMsg:
		if msg.requestID != v.activeRequestID || msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.postingList.setPostings(msg.postings)
		return nil, true

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
		v.topicContent = v.renderEntries(msg.entries)
		v.topicViewport.SetContent(v.topicContent)
		v.topicViewport.GotoTop()
		var uploadCmds []tea.Cmd
		for i, imgData := range msg.images {
			imageID := i + 1
			cols, rows := imageDimensions(imgData, v.vc.width-4)
			v.topicContent += "\n\n" + renderImagePlaceholder(imageID, cols, rows)
			v.topicViewport.SetContent(v.topicContent)
			seq := kittyUploadAndPlace(imgData, imageID, cols, rows)
			uploadCmds = append(uploadCmds, tea.Raw(seq))
		}
		if len(uploadCmds) > 0 {
			return tea.Batch(uploadCmds...), true
		}
		return nil, true

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

	case postingActionDoneMsg:
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		if msg.boxID != v.currentBoxID() {
			return nil, true
		}
		v.notice = msg.action
		idx := v.postingIndex(msg.postingID)
		if msg.removes && idx >= 0 {
			v.postingList.postings = append(v.postingList.postings[:idx], v.postingList.postings[idx+1:]...)
			if v.postingList.cursor > idx {
				v.postingList.cursor--
			}
			if v.postingList.cursor >= len(v.postingList.postings) && v.postingList.cursor > 0 {
				v.postingList.cursor--
			}
			v.postingList.ensureVisible()
		} else if msg.action == "marked as seen" && idx >= 0 {
			v.postingList.postings[idx].Seen = true
		}
		if v.activeRequestKind == mailRequestPostings {
			return v.requestPostings(v.currentBoxID()), true
		}
		return nil, true
	}

	// Cursor blinks and other component messages go to the open form. The
	// form owns the message while it is open, whether or not it yields a cmd.
	if v.compose != nil {
		return v.compose.update(msg), true
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
	if v.inThread {
		return v.topicViewport.View()
	}
	if v.notice != "" {
		return v.vc.styles.title.Render(v.notice) + "\n" + v.postingList.view()
	}
	return v.postingList.view()
}

// CapturingInput reports whether a form is open and wants every key.
func (v *mailView) CapturingInput() bool { return v.compose != nil }

func (v *mailView) HelpBindings() []helpBinding {
	if v.compose != nil {
		return v.compose.helpBindings()
	}
	if v.inThread {
		return []helpBinding{{"r", "reply"}}
	}
	return []helpBinding{
		{"c", "compose"},
		{"r", "reply"},
		{"f", "forward"},
		{"e", "seen"},
		{"l", "reply later"},
		{"a", "set aside"},
		{"d", "feed"},
		{"p", "paper trail"},
		{"t", "trash"},
		{"-", "mute"},
	}
}

func (v *mailView) SubnavItems() ([]navItem, int, string, bool) {
	label := "Mail"
	if v.boxIndex >= 0 && v.boxIndex < len(v.boxes) {
		label = v.boxes[v.boxIndex].Name
	}
	return boxNavItems(v.boxes), v.boxIndex, label, true
}

func (v *mailView) SubnavLeft() tea.Cmd {
	return v.switchBox(v.boxIndex - 1)
}

func (v *mailView) SubnavRight() tea.Cmd {
	return v.switchBox(v.boxIndex + 1)
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

	if v.inThread {
		if msg.String() == "r" && v.topicID != 0 {
			return v.loadReplyContext(v.topicID, v.topicName)
		}
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		v.postingList.moveUp()
	case tea.KeyDown:
		v.postingList.moveDown()
	case tea.KeyEnter:
		return v.openSelected()
	default:
		if msg.String() == "c" {
			return v.startCompose()
		}
		return v.handlePostingAction(msg.String())
	}
	return nil
}

func (v *mailView) InThread() bool { return v.inThread }
func (v *mailView) ExitThread() {
	v.inThread = false
	v.compose = nil
	v.cancelRequest()
}

func (v *mailView) CancelPendingDetail() bool {
	if v.activeRequestKind != mailRequestTopic && v.activeRequestKind != mailRequestReply {
		return false
	}
	v.cancelRequest()
	return true
}

func (v *mailView) Loading() bool { return v.loading }

func (v *mailView) Resize(width, height int) {
	if v.compose != nil {
		v.compose.resize(width, height)
	}
	v.postingList.setSize(width, height)
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
}

// handleBoxShortcut handles number-key shortcuts for switching boxes.
func (v *mailView) handleBoxShortcut(key string) tea.Cmd {
	return v.switchBox(boxForShortcut(key, v.boxes))
}

func (v *mailView) switchBox(index int) tea.Cmd {
	if index < 0 || index >= len(v.boxes) || index == v.boxIndex {
		return nil
	}
	v.ExitThread()
	v.notice = ""
	v.postingList.setPostings(nil)
	v.boxIndex = index
	return v.requestPostings(v.boxes[index].ID)
}

func (v *mailView) currentBoxID() int64 {
	if v.boxIndex < 0 || v.boxIndex >= len(v.boxes) {
		return 0
	}
	return v.boxes[v.boxIndex].ID
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

func (v *mailView) requestPostings(boxID int64) tea.Cmd {
	requestID, ctx := v.beginRequest(mailRequestPostings)
	return v.fetchPostings(ctx, requestID, boxID)
}

func (v *mailView) requestTopic(boxID, topicID int64, title string) tea.Cmd {
	requestID, ctx := v.beginRequest(mailRequestTopic)
	return v.fetchTopic(ctx, requestID, boxID, topicID, title)
}

func (v *mailView) postingIndex(postingID int64) int {
	for i := range v.postingList.postings {
		if v.postingList.postings[i].ID == postingID {
			return i
		}
	}
	return -1
}

func (v *mailView) openSelected() tea.Cmd {
	p := v.postingList.selectedPosting()
	if p == nil {
		return nil
	}
	topicID := p.ResolveTopicID()
	if topicID == 0 {
		topicID = p.ID
	}
	return v.requestTopic(v.currentBoxID(), topicID, p.Summary)
}

// --- Posting actions ---

func (v *mailView) handlePostingAction(key string) tea.Cmd {
	selected := v.postingList.selectedPosting()
	if selected == nil {
		return nil
	}
	p := *selected
	boxID := v.currentBoxID()

	switch key {
	case "l":
		return v.doPostingAction("moved to Reply Later", true, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToReplyLater(v.vc.ctx, p.ID)
		})
	case "a":
		return v.doPostingAction("moved to Set Aside", true, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToSetAside(v.vc.ctx, p.ID)
		})
	case "e":
		return v.doPostingAction("marked as seen", false, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MarkSeen(v.vc.ctx, []int64{p.ID})
		})
	case "d":
		return v.doPostingAction("moved to The Feed", true, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToFeed(v.vc.ctx, p.ID)
		})
	case "p":
		return v.doPostingAction("moved to Paper Trail", true, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToPaperTrail(v.vc.ctx, p.ID)
		})
	case "t":
		return v.doPostingAction("moved to Trash", true, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().MoveToTrash(v.vc.ctx, p.ID)
		})
	case "-":
		return v.doPostingAction("muted", true, boxID, p.ID, func() error {
			return v.vc.sdk.Postings().Mute(v.vc.ctx, p.ID)
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
		return v.requestTopic(v.currentBoxID(), topicID, p.Summary)
	}
	return nil
}

func (v *mailView) doPostingAction(label string, removes bool, boxID, postingID int64, fn func() error) tea.Cmd {
	return func() tea.Msg {
		err := fn()
		return postingActionDoneMsg{
			action:    label,
			boxID:     boxID,
			postingID: postingID,
			removes:   removes,
			err:       err,
		}
	}
}

// --- SDK type converters ---

func sdkBoxToModel(b generated.Box) models.Box {
	return models.Box{ID: b.Id, Kind: b.Kind, Name: b.Name}
}

func sdkPostingToModel(p generated.Posting) models.Posting {
	return models.Posting{
		ID:                    p.Id,
		CreatedAt:             formatTimestamp(p.CreatedAt),
		UpdatedAt:             formatTimestamp(p.UpdatedAt),
		Kind:                  p.Kind,
		Name:                  p.Name,
		Seen:                  p.Seen,
		Bundled:               p.Bundled,
		Muted:                 p.Muted,
		Summary:               p.Summary,
		EntryKind:             p.EntryKind,
		AppURL:                p.AppUrl,
		AlternativeSenderName: p.AlternativeSenderName,
		VisibleEntryCount:     p.VisibleEntryCount,
		Extenzions:            sdkExtenzionsToModel(p.Extenzions),
		Creator: models.Contact{
			ID:           p.Creator.Id,
			Name:         p.Creator.Name,
			EmailAddress: p.Creator.EmailAddress,
		},
	}
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
		Body:                  message.Content,
		BodyHTML:              message.Content,
		Creator: models.Contact{
			ID:           creator.Id,
			Name:         creator.Name,
			EmailAddress: creator.EmailAddress,
		},
	}
}

// --- Fetch commands ---

func (v *mailView) fetchBoxes() tea.Cmd {
	return func() tea.Msg {
		result, err := v.vc.sdk.Boxes().List(v.vc.ctx)
		if err != nil {
			return errMsg{err}
		}
		var sdkBoxes []generated.Box
		if result != nil {
			sdkBoxes = *result
		}
		boxes := make([]models.Box, len(sdkBoxes))
		for i, b := range sdkBoxes {
			boxes[i] = sdkBoxToModel(b)
		}
		return boxesLoadedMsg(boxes)
	}
}

func (v *mailView) fetchPostings(ctx context.Context, requestID uint64, boxID int64) tea.Cmd {
	return func() tea.Msg {
		resp, err := v.vc.sdk.Boxes().Get(ctx, boxID, nil)
		if err != nil {
			return postingsLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		postings := make([]models.Posting, 0, len(resp.Postings))
		for _, p := range resp.Postings {
			postings = append(postings, sdkPostingToModel(p))
		}
		return postingsLoadedMsg{requestID: requestID, boxID: boxID, postings: postings}
	}
}

func (v *mailView) fetchTopic(ctx context.Context, requestID uint64, boxID, topicID int64, title string) tea.Cmd {
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
		for i, entry := range topic.Entries {
			entries[i] = sdkMessageToEntry(entry, messages[i])
		}

		var images [][]byte
		for _, entry := range entries {
			for _, imgURL := range extractImageURLs(entry.Body) {
				var data []byte
				if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
					data = fetchImageData(ctx, imgURL)
				} else {
					sdkResp, getErr := v.vc.sdk.Get(ctx, imgURL)
					if getErr == nil && sdkResp != nil {
						data = sdkResp.Data
					}
				}
				if len(data) > 0 {
					images = append(images, data)
				}
			}
		}

		return topicLoadedMsg{
			requestID: requestID,
			boxID:     boxID,
			topicID:   topicID,
			title:     title,
			entries:   entries,
			images:    images,
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
			fmt.Fprintf(&b, "\n%s\n", v.vc.styles.entryBody.Render(htmlToText(e.Body)))
		}
		b.WriteString("\n")
	}

	return b.String()
}
