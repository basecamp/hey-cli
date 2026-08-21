package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/markdown"
)

// --- Journal messages ---

type journalRequestKind int

const (
	journalRequestNone journalRequestKind = iota
	journalRequestEntry
)

type journalDetailMsg struct {
	requestResult
	body   string
	images [][]byte
}

// --- Journal section view ---

type journalView struct {
	vc *viewContext

	dates     []string
	dateIndex int

	topicViewport viewport.Model
	topicContent  string
	inThread      bool
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
		v.topicContent = markdown.Render(msg.body, max(v.vc.width-4, 40))
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
	}

	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd, cmd != nil
	}

	return nil, false
}

func (v *journalView) View() string {
	return v.topicViewport.View()
}

func (v *journalView) HelpBindings() []helpBinding { return nil }

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
		return v.requestJournalEntry()
	}
	return nil
}

func (v *journalView) SubnavRight() tea.Cmd {
	if v.dateIndex < len(v.dates)-1 {
		v.dateIndex++
		return v.requestJournalEntry()
	}
	return nil
}

func (v *journalView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	// Journal always shows content in viewport
	var cmd tea.Cmd
	v.topicViewport, cmd = v.topicViewport.Update(msg)
	return cmd
}

func (v *journalView) InThread() bool { return v.inThread }
func (v *journalView) ExitThread()    {} // no-op: journal always shows content
func (v *journalView) Loading() bool  { return v.requests.loading }

// Restyle is a no-op: the journal caches plain text and Kitty placeholders, neither
// of which carries palette colors. The date list renders live.
func (v *journalView) Restyle() {}

func (v *journalView) Resize(width, height int) {
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
}

// --- Fetch command ---

func (v *journalView) requestJournalEntry() tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, journalRequestEntry)
	return v.fetchJournalEntry(ctx, requestID, v.dates[v.dateIndex])
}

// A day HEY has nothing for answers 204, and a day it cannot answer for reads the same
// way rather than as an error: the journal is a page of prose, and "(empty)" is what an
// empty one says.
func (v *journalView) fetchJournalEntry(ctx context.Context, requestID uint64, date string) tea.Cmd {
	return func() tea.Msg {
		content, err := v.vc.sdk.Journal().GetContent(ctx, date)
		if err != nil || content == "" {
			return journalDetailMsg{requestResult: newRequestResult(requestID, nil), body: "(empty)"}
		}

		var images [][]byte
		if v.vc.imageRenderer.protocol() == imageProtocolKitty && v.vc.imageFetcher != nil {
			for _, imageURL := range extractImageURLs(content) {
				data, fetchErr := v.vc.imageFetcher.Fetch(ctx, imageURL)
				if fetchErr == nil && len(data) > 0 {
					images = append(images, data)
				}
			}
		}

		return journalDetailMsg{requestResult: newRequestResult(requestID, nil), body: htmlToMarkdown(content), images: images}
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
