package tui

import (
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	nethtml "golang.org/x/net/html"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type journalRequestKind int

const (
	journalRequestNone journalRequestKind = iota
	journalRequestFeed
	journalRequestDetail
	journalRequestMutation
)

type journalPageLoadedMsg struct {
	requestResult
	entries  []journalSummary
	nextPage string
}

type journalPageAppendedMsg struct {
	requestID uint64
	entries   []journalSummary
	nextPage  string
	err       error
}

// journalPageRefreshedMsg is the top of the list read again for a change that arrived
// over the watch. It carries a bare counter of its own rather than riding the request
// lane, for the reason the appended page does: a live re-read must never cancel — or be
// cancelled by — the read the reader asked for, and it never shows the spinner.
type journalPageRefreshedMsg struct {
	requestID uint64
	entries   []journalSummary
	nextPage  string
	err       error
}

type journalDetailMsg struct {
	requestResult
	date    string
	content string
	body    htmlutil.Markdown
	images  [][]byte
	edit    bool
}

type journalSavedMsg struct {
	requestResult
	date    string
	removed bool
}

type journalPromptKind int

const (
	journalPromptNone journalPromptKind = iota
	journalPromptSearch
	journalPromptDate
)

type journalPrompt struct {
	kind   journalPromptKind
	input  textinput.Model
	status string
	styles styles
}

func newJournalPrompt(kind journalPromptKind, value string, styles styles) *journalPrompt {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(value)
	if kind == journalPromptSearch {
		input.Placeholder = "Search your journal…"
	} else {
		input.Placeholder = "YYYY-MM-DD"
	}
	return &journalPrompt{kind: kind, input: input, styles: styles}
}

func (p *journalPrompt) init() tea.Cmd { return p.input.Focus() }

func (p *journalPrompt) resize(width int) { p.input.SetWidth(max(width-14, 10)) }

func (p *journalPrompt) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

func (p *journalPrompt) view() string {
	title, label := "Search journal", "Search: "
	if p.kind == journalPromptDate {
		title, label = "Go to date", "Date: "
	}
	var b strings.Builder
	b.WriteString(p.styles.title.Render(title))
	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render(label))
	b.WriteString(p.input.View())
	if p.status != "" {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(terminal.SanitizeLine(p.status)))
	}
	return b.String()
}

type journalView struct {
	vc *viewContext

	list        journalList
	loaded      bool
	query       string
	nextPage    string
	loadingMore bool
	moreID      uint64
	liveID      uint64

	detailDate    string
	detailContent string
	detailBody    htmlutil.Markdown
	detailView    viewport.Model
	inDetail      bool
	form          *journalForm
	prompt        *journalPrompt
	confirmRemove bool
	selectDate    string

	requests requestLane[journalRequestKind]
}

func newJournalView(vc *viewContext) *journalView {
	return &journalView{
		vc:         vc,
		detailView: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *journalView) Init() tea.Cmd {
	if v.loaded {
		return nil
	}
	return v.requestFeed("")
}

func (v *journalView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case journalPageLoadedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.loaded = true
		v.loadingMore = false
		v.nextPage = msg.nextPage
		v.list.setEntries(msg.entries)
		if v.selectDate != "" {
			v.list.selectDate(v.selectDate)
			v.selectDate = ""
		}
		return v.loadMoreEntries(), true

	case journalPageAppendedMsg:
		if msg.requestID != v.moreID {
			return nil, true
		}
		v.loadingMore = false
		if msg.err != nil {
			return notifyError("Could not load older journal entries", msg.err), true
		}
		v.list.growEntries(msg.entries)
		v.nextPage = msg.nextPage
		return v.loadMoreEntries(), true

	case journalPageRefreshedMsg:
		if msg.requestID != v.liveID {
			return nil, true
		}
		if msg.err != nil {
			// A live re-read that failed costs staleness, not a notice: nothing the
			// reader did is waiting on it, and the next change rings again.
			return nil, true
		}
		v.spliceRefreshedEntries(msg.entries, msg.nextPage)
		return nil, true

	case journalDetailMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.detailDate = msg.date
		v.detailContent = msg.content
		v.detailBody = msg.body
		v.inDetail = true
		v.confirmRemove = false
		uploads := v.renderDetail(msg.images)
		if msg.edit {
			return tea.Batch(uploads, v.startEditor()), true
		}
		return uploads, true

	case journalSavedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			// A form that is open says so itself; everything else says it in a toast.
			if v.form != nil {
				v.form.saving = false
				v.form.status = errorNotice("Save failed", msg.err)
				v.form.isError = true
				return nil, true
			}
			return notifyError("Could not remove journal entry", msg.err), true
		}
		v.form = nil
		v.inDetail = false
		v.query = ""
		v.selectDate = msg.date
		saved := "Journal entry saved"
		if msg.removed {
			saved = "Journal entry removed"
		}
		return tea.Batch(notify(saved), v.requestFeed("")), true
	}

	if v.prompt != nil {
		return v.prompt.update(msg), true
	}
	if v.form != nil {
		return v.form.update(msg), true
	}
	if v.inDetail {
		var cmd tea.Cmd
		v.detailView, cmd = v.detailView.Update(msg)
		return cmd, cmd != nil
	}
	return nil, false
}

func (v *journalView) View() string {
	if v.prompt != nil {
		return v.prompt.view()
	}
	if v.form != nil {
		return v.form.view()
	}
	if v.inDetail {
		return v.detailView.View()
	}

	// What just happened is a toast now, so the heading is only ever about what the reader
	// is looking at.
	var heading string
	switch {
	case v.query != "":
		heading = fmt.Sprintf("Search: %s · %d results", terminal.SanitizeLine(v.query), len(v.list.entries))
	case len(v.list.entries) == 0 && v.loaded:
		heading = "No journal entries yet · press a to write about today"
	case !v.hasToday():
		heading = "Today is empty · press a to write"
	default:
		heading = "Journal · newest first"
	}
	if v.loadingMore {
		heading += " · loading older entries…"
	}
	return v.vc.styles.title.Render(heading) + "\n" + v.list.view()
}

func (v *journalView) HelpBindings() []helpBinding {
	if v.prompt != nil {
		label := "search"
		if v.prompt.kind == journalPromptDate {
			label = "go"
		}
		return []helpBinding{{"enter", label}, {"esc", "cancel"}}
	}
	if v.form != nil {
		return v.form.helpBindings()
	}
	if v.inDetail {
		bindings := []helpBinding{{"e", "edit"}, {"t", "today"}}
		if strings.TrimSpace(v.detailContent) != "" {
			label := "remove"
			if v.confirmRemove {
				label = "confirm remove"
			}
			bindings = append(bindings, helpBinding{"x", label})
		}
		return bindings
	}
	bindings := []helpBinding{{"enter", "open"}, {"a", "add today"}, {"/", "search"}, {"g", "go to date"}, {"t", "today"}, {"r", "refresh"}}
	if v.query != "" {
		bindings = append(bindings, helpBinding{"c", "clear search"})
	}
	return bindings
}

func (v *journalView) SubnavItems() ([]navItem, int, string, bool) {
	label := "Journal"
	if v.inDetail && v.detailDate != "" {
		label = "Journal · " + v.detailDate
	} else if v.query != "" {
		label = "Journal search"
	}
	return nil, 0, label, true
}

func (v *journalView) SubnavLeft() tea.Cmd  { return nil }
func (v *journalView) SubnavRight() tea.Cmd { return nil }

func (v *journalView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	if v.prompt != nil {
		return v.handlePromptKey(msg)
	}
	if v.form != nil {
		if msg.Key().Code == tea.KeyEscape && !v.form.saving {
			if v.form.canClose() {
				v.form = nil
			}
			return nil
		}
		cmd, action := v.form.handleKey(msg)
		switch action {
		case journalFormSave:
			return v.saveJournalEntry(false)
		case journalFormRemove:
			return v.saveJournalEntry(true)
		default:
			return cmd
		}
	}
	if v.requests.kind == journalRequestMutation {
		return nil
	}

	if msg.String() != "x" {
		v.confirmRemove = false
	}
	if v.inDetail {
		switch msg.String() {
		case "e":
			return v.startEditor()
		case "t":
			return v.requestDate(todayJournalDate(), false)
		case "x":
			if strings.TrimSpace(v.detailContent) == "" {
				return nil
			}
			// The help bar asks for the second x — "confirm remove" — as the habits
			// picker does. A toast would be gone before the reader answered.
			if !v.confirmRemove {
				v.confirmRemove = true
				return nil
			}
			return v.removeJournalEntry(v.detailDate)
		}
		var cmd tea.Cmd
		v.detailView, cmd = v.detailView.Update(msg)
		return cmd
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		v.list.moveUp()
	case tea.KeyDown:
		v.list.moveDown()
		return v.loadMoreEntries()
	case tea.KeyPgDown:
		for range v.list.visibleCount() {
			v.list.moveDown()
		}
		return v.loadMoreEntries()
	case tea.KeyPgUp:
		for range v.list.visibleCount() {
			v.list.moveUp()
		}
	case tea.KeyEnter:
		if entry := v.list.selected(); entry != nil {
			return v.requestDate(entry.Date, false)
		}
	default:
		switch msg.String() {
		case "j":
			v.list.moveDown()
			return v.loadMoreEntries()
		case "k":
			v.list.moveUp()
		case "a", "t":
			return v.requestDate(todayJournalDate(), msg.String() == "a")
		case "e":
			if entry := v.list.selected(); entry != nil {
				return v.requestDate(entry.Date, true)
			}
		case "/":
			return v.startPrompt(journalPromptSearch, v.query)
		case "g":
			return v.startPrompt(journalPromptDate, "")
		case "c":
			if v.query != "" {
				v.query = ""
				return v.requestFeed("")
			}
		case "r":
			return v.requestFeed(v.query)
		}
	}
	return nil
}

func (v *journalView) handlePromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.Key().Code == tea.KeyEscape {
		v.prompt = nil
		return nil
	}
	if msg.Key().Code != tea.KeyEnter {
		return v.prompt.update(msg)
	}

	value := strings.TrimSpace(v.prompt.input.Value())
	if v.prompt.kind == journalPromptSearch {
		v.prompt = nil
		v.query = value
		return v.requestFeed(value)
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		v.prompt.status = "Use a date in YYYY-MM-DD format"
		return nil
	}
	v.prompt = nil
	return v.requestDate(value, false)
}

func (v *journalView) startPrompt(kind journalPromptKind, value string) tea.Cmd {
	v.prompt = newJournalPrompt(kind, value, v.vc.styles)
	v.prompt.resize(v.vc.width)
	return v.prompt.init()
}

func (v *journalView) startEditor() tea.Cmd {
	v.form = newJournalForm(v.detailDate, v.detailContent, v.vc.styles)
	v.form.resize(v.vc.width, v.vc.height)
	return v.form.init()
}

func (v *journalView) InThread() bool { return v.inDetail }

func (v *journalView) ExitThread() {
	v.inDetail = false
	v.form = nil
	v.confirmRemove = false
	v.detailDate = ""
	v.detailContent = ""
	v.detailBody = htmlutil.Markdown{}
	v.requests.cancel()
}

func (v *journalView) CancelPendingDetail() bool {
	if v.requests.kind != journalRequestDetail {
		return false
	}
	v.requests.cancel()
	return true
}

func (v *journalView) CapturingInput() bool { return v.form != nil || v.prompt != nil }

func (v *journalView) AccountSwitchBlocked() bool {
	return v.requests.kind == journalRequestMutation
}

func (v *journalView) Loading() bool { return v.requests.loading }

func (v *journalView) Restyle() {
	if v.form != nil {
		v.form.styles = v.vc.styles
	}
	if v.prompt != nil {
		v.prompt.styles = v.vc.styles
	}
}

func (v *journalView) Resize(width, height int) {
	v.list.setSize(width, max(height-1, 1))
	v.detailView.SetWidth(width)
	v.detailView.SetHeight(height)
	if v.form != nil {
		v.form.resize(width, height)
	}
	if v.prompt != nil {
		v.prompt.resize(width)
	}
}

func (v *journalView) hasToday() bool {
	today := todayJournalDate()
	for _, entry := range v.list.entries {
		if entry.Date == today {
			return true
		}
	}
	return false
}

func todayJournalDate() string { return time.Now().Format("2006-01-02") }

func (v *journalView) requestFeed(query string) tea.Cmd {
	v.nextPage = ""
	v.loadingMore = false
	v.moreID++
	v.query = query
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestFeed)
	return v.fetchJournalPage(ctx, requestID, "", query)
}

func (v *journalView) loadMoreEntries() tea.Cmd {
	if v.loadingMore || v.nextPage == "" || v.query != "" {
		return nil
	}
	if v.list.hasRowsBelow() && len(v.list.entries)-v.list.cursor > loadMoreThreshold {
		return nil
	}
	v.loadingMore = true
	v.moreID++
	return v.fetchMoreJournalPage(v.vc.ctx, v.moreID, v.nextPage)
}

// refreshLive re-reads the top of the list for a change that arrived over the watch, and
// says it is held when the view cannot take one right now: a form or a prompt is the
// reader's focus, a removal is waiting on its second x, or a read they asked for is in
// flight. A search is left alone — its results are a snapshot of a question already
// answered — and an open entry is too: the list under it is spliced, but what the reader
// is reading does not change under them.
func (v *journalView) refreshLive() (tea.Cmd, bool) {
	if v.form != nil || v.prompt != nil || v.confirmRemove || v.requests.loading {
		return nil, true
	}
	if !v.loaded || v.query != "" {
		return nil, false
	}
	v.liveID++
	return v.fetchRefreshedJournalPage(v.vc.ctx, v.liveID), false
}

func (v *journalView) fetchRefreshedJournalPage(ctx context.Context, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		result, err := v.vc.sdk.Journal().ListPage(ctx, "", "")
		if err != nil {
			return journalPageRefreshedMsg{requestID: requestID, err: err}
		}
		if result == nil {
			return journalPageRefreshedMsg{requestID: requestID}
		}
		return journalPageRefreshedMsg{
			requestID: requestID,
			entries:   journalSummaries(result.Entries),
			nextPage:  result.NextPage,
		}
	}
}

// spliceRefreshedEntries puts the fresh top page in front of whatever the list had grown
// past it, the way the mail list's refreshHead does. The journal is one entry per day,
// newest first, so the fresh page covers every date down to its last entry and the old
// tail keeps the rest until a full re-read. The cursor stays on its day, and the cursor
// for what comes next only moves while the top page is the whole list.
func (v *journalView) spliceRefreshedEntries(fresh []journalSummary, nextPage string) {
	var selectedDate string
	if entry := v.list.selected(); entry != nil {
		selectedDate = entry.Date
	}

	if len(fresh) == 0 {
		v.list.setEntries(nil)
		v.nextPage = ""
		return
	}

	cutoff := fresh[len(fresh)-1].Date
	var tail []journalSummary
	for _, entry := range v.list.entries {
		if entry.Date < cutoff {
			tail = append(tail, entry)
		}
	}
	if len(tail) == 0 {
		v.nextPage = nextPage
	}
	v.list.setEntries(append(fresh, tail...))
	if selectedDate != "" {
		v.list.selectDate(selectedDate)
	}
}

func (v *journalView) fetchJournalPage(ctx context.Context, requestID uint64, page, query string) tea.Cmd {
	return func() tea.Msg {
		result, err := v.vc.sdk.Journal().ListPage(ctx, page, query)
		if err != nil {
			return journalPageLoadedMsg{requestResult: newRequestResult(requestID, err)}
		}
		if result == nil {
			return journalPageLoadedMsg{requestResult: newRequestResult(requestID, nil)}
		}
		return journalPageLoadedMsg{
			requestResult: newRequestResult(requestID, nil),
			entries:       journalSummaries(result.Entries),
			nextPage:      result.NextPage,
		}
	}
}

func (v *journalView) fetchMoreJournalPage(ctx context.Context, requestID uint64, page string) tea.Cmd {
	return func() tea.Msg {
		result, err := v.vc.sdk.Journal().ListPage(ctx, page, "")
		if err != nil {
			return journalPageAppendedMsg{requestID: requestID, err: err}
		}
		if result == nil {
			return journalPageAppendedMsg{requestID: requestID}
		}
		return journalPageAppendedMsg{
			requestID: requestID,
			entries:   journalSummaries(result.Entries),
			nextPage:  result.NextPage,
		}
	}
}

func journalSummaries(recordings []generated.Recording) []journalSummary {
	entries := make([]journalSummary, 0, len(recordings))
	for _, recording := range recordings {
		if recording.Type != "" && recording.Type != "Calendar::JournalEntry" {
			continue
		}
		date, starts := journalRecordingDate(recording.StartsAt)
		entries = append(entries, journalSummary{
			ID:      recording.Id,
			Date:    date,
			Starts:  starts,
			Preview: recording.Content,
		})
	}
	return entries
}

// HEY sends a journal recording's day as midnight UTC so its calendar date survives
// deserialization unchanged. Build the display time from that date rather than converting
// midnight to local time, which would move the entry onto yesterday west of UTC.
func journalRecordingDate(starts time.Time) (string, time.Time) {
	if starts.IsZero() {
		return "", time.Time{}
	}
	utc := starts.UTC()
	year, month, day := utc.Date()
	return utc.Format("2006-01-02"), time.Date(year, month, day, 12, 0, 0, 0, time.Local)
}

func (v *journalView) requestDate(date string, edit bool) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestDetail)
	return v.fetchJournalEntry(ctx, requestID, date, edit)
}

func (v *journalView) fetchJournalEntry(ctx context.Context, requestID uint64, date string, edit bool) tea.Cmd {
	return func() tea.Msg {
		recording, err := v.vc.sdk.Journal().Get(ctx, date)
		if err != nil {
			return journalDetailMsg{requestResult: newRequestResult(requestID, err), date: date, edit: edit}
		}
		if recording == nil {
			return journalDetailMsg{requestResult: newRequestResult(requestID, nil), date: date, edit: edit}
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
			date:          date,
			content:       editableContent,
			body:          htmlToMarkdown(renderedContent),
			images:        images,
			edit:          edit,
		}
	}
}

func (v *journalView) renderDetail(images [][]byte) tea.Cmd {
	content := markdown.Render(v.detailBody, max(v.vc.width-4, 40))
	if v.detailBody.IsEmpty() {
		content = "No entry for this day · press e to write"
	}
	heading := friendlyDateFromString(v.detailDate)
	content = v.vc.styles.title.Render(heading) + "\n\n" + content
	uploads := make([]tea.Cmd, 0, len(images))
	for _, imageData := range images {
		imageID := nextImageID()
		cols, rows := imageDimensions(imageData, v.vc.width-4)
		content += "\n\n" + renderImagePlaceholder(imageID, cols, rows)
		uploads = append(uploads, tea.Raw(kittyUploadAndPlace(imageData, imageID, cols, rows)))
	}
	v.detailView.SetContent(content)
	v.detailView.GotoTop()
	if len(uploads) == 0 {
		return nil
	}
	return tea.Batch(uploads...)
}

func friendlyDateFromString(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return terminal.SanitizeLine(date)
	}
	if date == todayJournalDate() {
		return "Today · " + parsed.Format("Monday, January 2, 2006")
	}
	return parsed.Format("Monday, January 2, 2006")
}

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

func (v *journalView) saveJournalEntry(remove bool) tea.Cmd {
	content := v.form.content()
	if remove {
		content = ""
	}
	date := v.form.date
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestMutation)
	return func() tea.Msg {
		_, err := v.vc.sdk.Journal().Update(ctx, date, content)
		return journalSavedMsg{requestResult: newRequestResult(requestID, err), date: date, removed: remove}
	}
}

func (v *journalView) removeJournalEntry(date string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestMutation)
	return func() tea.Msg {
		_, err := v.vc.sdk.Journal().Update(ctx, date, "")
		return journalSavedMsg{requestResult: newRequestResult(requestID, err), date: date, removed: true}
	}
}
