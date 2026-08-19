package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/models"
)

// --- Mail messages ---

type boxesLoadedMsg []models.Box

type postingsLoadedMsg struct {
	postings []models.Posting
}

type topicLoadedMsg struct {
	title   string
	entries []models.Entry
	images  [][]byte
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

	compose *composeForm // non-nil while a new message or reply is being written
	notice  string       // one-shot confirmation shown above the posting list
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
		v.loading = true
		return v.fetchPostings(v.boxes[v.boxIndex].ID)
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
			v.loading = true
			return v.fetchPostings(v.boxes[0].ID), true
		}
		return nil, true

	case postingsLoadedMsg:
		v.loading = false
		v.postingList.setPostings(msg.postings)
		return nil, true

	case topicLoadedMsg:
		v.loading = false
		v.inThread = true
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
		v.loading = false
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
		if msg.boxID != v.currentBoxID() {
			return nil, true
		}
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
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
		} else if msg.action == "marked as seen" && idx >= 0 {
			v.postingList.postings[idx].Seen = true
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
			v.loading = true
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
func (v *mailView) ExitThread()    { v.inThread = false; v.compose = nil }
func (v *mailView) Loading() bool  { return v.loading }

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
	v.boxIndex = index
	v.loading = true
	return v.fetchPostings(v.boxes[index].ID)
}

func (v *mailView) currentBoxID() int64 {
	if v.boxIndex < 0 || v.boxIndex >= len(v.boxes) {
		return 0
	}
	return v.boxes[v.boxIndex].ID
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
	v.topicID = topicID
	v.topicName = p.Summary
	v.loading = true
	return v.fetchTopic(topicID, p.Summary)
}

// --- Posting actions ---

func (v *mailView) handlePostingAction(key string) tea.Cmd {
	p := v.postingList.selectedPosting()
	if p == nil {
		return nil
	}
	boxID := v.currentBoxID()

	switch key {
	case "l":
		return v.doPostingAction("moved to Reply Later", false, boxID, p.ID, func() error {
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
		v.topicID = topicID
		v.topicName = p.Summary
		v.loading = true
		return v.loadReplyContext(topicID, p.Summary)
	case "f":
		topicID := p.ResolveTopicID()
		if topicID == 0 {
			topicID = p.ID
		}
		v.topicID = topicID
		v.topicName = p.Summary
		v.loading = true
		return v.fetchTopic(topicID, p.Summary)
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

func (v *mailView) fetchPostings(boxID int64) tea.Cmd {
	return func() tea.Msg {
		resp, err := v.vc.sdk.Boxes().Get(v.vc.ctx, boxID, nil)
		if err != nil {
			return errMsg{err}
		}
		postings := make([]models.Posting, 0, len(resp.Postings))
		for _, p := range resp.Postings {
			postings = append(postings, sdkPostingToModel(p))
		}
		return postingsLoadedMsg{postings: postings}
	}
}

func (v *mailView) fetchTopic(topicID int64, title string) tea.Cmd {
	return func() tea.Msg {
		resp, err := v.vc.sdk.GetHTML(v.vc.ctx, fmt.Sprintf("/topics/%d/entries", topicID))
		if err != nil {
			return errMsg{err}
		}
		entries := htmlutil.ParseTopicEntriesHTML(string(resp.Data))

		var images [][]byte
		for _, e := range entries {
			for _, imgURL := range extractImageURLs(e.Body) {
				var data []byte
				if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
					data = fetchImageData(imgURL)
				} else {
					sdkResp, getErr := v.vc.sdk.Get(v.vc.ctx, imgURL)
					if getErr == nil && sdkResp != nil {
						data = sdkResp.Data
					}
				}
				if len(data) > 0 {
					images = append(images, data)
				}
			}
		}

		return topicLoadedMsg{title: title, entries: entries, images: images}
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
