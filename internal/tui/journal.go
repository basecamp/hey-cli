package tui

import (
	"context"
	"errors"
	stdhtml "html"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	nethtml "golang.org/x/net/html"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
)

// --- Journal messages ---

type journalRequestKind int

const (
	journalRequestNone journalRequestKind = iota
	journalRequestEntry
	journalRequestMutation
)

type journalDetailMsg struct {
	requestResult
	content string
	body    htmlutil.Markdown
	images  [][]byte
}

type journalSavedMsg struct {
	requestResult
	removed bool
}

// --- Journal section view ---

type journalView struct {
	vc *viewContext

	dates     []string
	dateIndex int

	topicViewport viewport.Model
	topicContent  string
	editContent   string
	inThread      bool
	form          *journalForm
	notice        string
	requests      requestLane[journalRequestKind]
}

func newJournalView(vc *viewContext) *journalView {
	return &journalView{
		vc:            vc,
		dates:         generateJournalDates(30),
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *journalView) Init() tea.Cmd {
	v.dates = generateJournalDates(30)
	v.dateIndex = len(v.dates) - 1
	return v.requestJournalEntry()
}

func (v *journalView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case journalDetailMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.inThread = true
		v.editContent = msg.content
		v.topicContent = markdown.Render(msg.body, max(v.vc.width-4, 40))
		if msg.body.IsEmpty() {
			v.topicContent = "(empty)"
		}
		v.topicViewport.SetContent(v.topicContent)
		v.topicViewport.GotoTop()
		var uploadCmds []tea.Cmd
		for _, imgData := range msg.images {
			imageID := nextImageID()
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

	case journalSavedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			if v.form != nil {
				v.form.saving = false
				v.form.status = errorNotice("Save failed", msg.err)
				v.form.isError = true
			}
			return nil, true
		}
		v.form = nil
		v.setNotice("Journal entry saved")
		if msg.removed {
			v.setNotice("Journal entry removed")
		}
		return v.requestJournalEntry(), true
	}

	if v.form != nil {
		return v.form.update(msg), true
	}
	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd, cmd != nil
	}

	return nil, false
}

func (v *journalView) View() string {
	if v.form != nil {
		return v.form.view()
	}
	if v.notice != "" {
		return v.vc.styles.title.Render(v.notice) + "\n" + v.topicViewport.View()
	}
	return v.topicViewport.View()
}

func (v *journalView) HelpBindings() []helpBinding {
	if v.form != nil {
		return v.form.helpBindings()
	}
	return []helpBinding{{"e", "edit"}}
}

func (v *journalView) SubnavItems() ([]navItem, int, string, bool) {
	label := "Journal"
	if v.dateIndex >= 0 && v.dateIndex < len(v.dates) {
		label = v.dates[v.dateIndex]
	}
	return journalNavItems(v.dates), v.dateIndex, label, false
}

func (v *journalView) SubnavLeft() tea.Cmd {
	if v.dateIndex > 0 {
		v.dateIndex--
		v.setNotice("")
		return v.requestJournalEntry()
	}
	return nil
}

func (v *journalView) SubnavRight() tea.Cmd {
	if v.dateIndex < len(v.dates)-1 {
		v.dateIndex++
		v.setNotice("")
		return v.requestJournalEntry()
	}
	return nil
}

func (v *journalView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	if v.form != nil {
		if msg.Key().Code == tea.KeyEscape && !v.form.saving {
			v.form = nil
			return nil
		}
		cmd, submit := v.form.handleKey(msg)
		if submit {
			return v.saveJournalEntry()
		}
		return cmd
	}
	if msg.String() == "e" && v.inThread {
		v.setNotice("")
		v.form = newJournalForm(v.dates[v.dateIndex], v.editContent, v.vc.styles)
		v.form.resize(v.vc.width, v.vc.height)
		return v.form.init()
	}

	// Journal always shows content in viewport.
	var cmd tea.Cmd
	v.topicViewport, cmd = v.topicViewport.Update(msg)
	return cmd
}

func (v *journalView) InThread() bool { return v.inThread }
func (v *journalView) ExitThread()    {} // no-op: journal always shows content
func (v *journalView) Loading() bool  { return v.requests.loading }
func (v *journalView) CapturingInput() bool {
	return v.form != nil
}

func (v *journalView) AccountSwitchBlocked() bool {
	return v.requests.kind == journalRequestMutation
}

// Restyle hands the active palette to the form. Journal content itself is plain text
// and Kitty placeholders, so the viewport needs no repaint.
func (v *journalView) Restyle() {
	if v.form != nil {
		v.form.styles = v.vc.styles
	}
}

func (v *journalView) Resize(width, height int) {
	v.topicViewport.SetWidth(width)
	v.resizeViewport(height)
	if v.form != nil {
		v.form.resize(width, height)
	}
}

func (v *journalView) setNotice(notice string) {
	v.notice = notice
	v.resizeViewport(v.vc.height)
}

func (v *journalView) resizeViewport(height int) {
	if v.notice != "" {
		height--
	}
	v.topicViewport.SetHeight(max(height, 1))
}

// --- Fetch command ---

func (v *journalView) requestJournalEntry() tea.Cmd {
	v.inThread = false
	v.editContent = ""
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestEntry)
	return v.fetchJournalEntry(ctx, requestID, v.dates[v.dateIndex])
}

// A day HEY has nothing for answers 204, which opens as an empty editable page. A failed
// read remains an error so the editor cannot replace an entry whose content it never saw.
func (v *journalView) fetchJournalEntry(ctx context.Context, requestID uint64, date string) tea.Cmd {
	return func() tea.Msg {
		recording, err := v.vc.sdk.Journal().Get(ctx, date)
		if err != nil {
			return journalDetailMsg{requestResult: newRequestResult(requestID, err)}
		}
		if recording == nil {
			return journalDetailMsg{requestResult: newRequestResult(requestID, nil)}
		}

		editableContent := recording.ContentHtml
		renderedContent := recording.ContentHtml
		if renderedContent == "" {
			editableContent = recording.Content
			renderedContent = stdhtml.EscapeString(recording.Content)
		} else {
			editableContent = journalEditorContent(editableContent)
		}
		var images [][]byte
		if v.vc.imageRenderer.protocol() == imageProtocolKitty && v.vc.imageFetcher != nil {
			images = newImageBudget().fetchImages(ctx, v.vc.imageFetcher, extractImageURLs(renderedContent))
		}

		return journalDetailMsg{
			requestResult: newRequestResult(requestID, nil),
			content:       editableContent,
			body:          htmlToMarkdown(renderedContent),
			images:        images,
		}
	}
}

// journalEditorContent returns a stable rich-text document for HEY updates. It removes
// div.trix-content presentation containers and writes every other token byte-for-byte,
// preserving Trix attachment attributes across repeated edit-save cycles.
func journalEditorContent(content string) string {
	content = strings.TrimSpace(content)
	tokens := nethtml.NewTokenizer(strings.NewReader(content))
	var result strings.Builder
	var divStack []bool
	for {
		tokenType := tokens.Next()
		if tokenType == nethtml.ErrorToken {
			if errors.Is(tokens.Err(), io.EOF) {
				return strings.TrimSpace(result.String())
			}
			return content
		}

		raw := append([]byte(nil), tokens.Raw()...)
		token := tokens.Token()
		switch tokenType {
		case nethtml.StartTagToken:
			drop := token.Data == "div" && hasHTMLClass(token, "trix-content")
			if token.Data == "div" {
				divStack = append(divStack, drop)
			}
			if !drop {
				result.Write(raw)
			}
		case nethtml.SelfClosingTagToken:
			if token.Data != "div" || !hasHTMLClass(token, "trix-content") {
				result.Write(raw)
			}
		case nethtml.EndTagToken:
			drop := false
			if token.Data == "div" && len(divStack) > 0 {
				drop = divStack[len(divStack)-1]
				divStack = divStack[:len(divStack)-1]
			}
			if !drop {
				result.Write(raw)
			}
		default:
			result.Write(raw)
		}
	}
}

func hasHTMLClass(token nethtml.Token, name string) bool {
	for _, attribute := range token.Attr {
		if attribute.Key == "class" {
			for class := range strings.FieldsSeq(attribute.Val) {
				if class == name {
					return true
				}
			}
		}
	}
	return false
}

func (v *journalView) saveJournalEntry() tea.Cmd {
	content := v.form.content()
	date := v.form.date
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestMutation)
	return func() tea.Msg {
		_, err := v.vc.sdk.Journal().Update(ctx, date, content)
		return journalSavedMsg{
			requestResult: newRequestResult(requestID, err),
			removed:       content == "",
		}
	}
}

// --- Journal date generation ---

func generateJournalDates(n int) []string {
	dates := make([]string, n)
	today := time.Now()
	for i := range n {
		d := today.AddDate(0, 0, -(n - 1 - i))
		dates[i] = d.Format("2006-01-02")
	}
	return dates
}
