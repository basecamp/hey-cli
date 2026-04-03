package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// --- Journal messages ---

type journalDetailMsg struct {
	title  string
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
	loading       bool
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
	v.loading = true
	return v.fetchJournalEntry(v.dates[v.dateIndex])
}

func (v *journalView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case journalDetailMsg:
		// Guard against stale async responses from a previously selected date.
		if v.dateIndex >= 0 && v.dateIndex < len(v.dates) && msg.title != v.dates[v.dateIndex] {
			return nil, true
		}
		v.loading = false
		v.inThread = true
		body := msg.body
		v.topicContent = body
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
		v.loading = true
		return v.fetchJournalEntry(v.dates[v.dateIndex])
	}
	return nil
}

func (v *journalView) SubnavRight() tea.Cmd {
	if v.dateIndex < len(v.dates)-1 {
		v.dateIndex++
		v.loading = true
		return v.fetchJournalEntry(v.dates[v.dateIndex])
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
func (v *journalView) Loading() bool  { return v.loading }

func (v *journalView) Resize(width, height int) {
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
}

// --- Fetch command ---

func (v *journalView) fetchJournalEntry(date string) tea.Cmd {
	return func() tea.Msg {
		content, err := v.vc.sdk.Journal().GetContent(v.vc.ctx, date)
		if err != nil || content == "" {
			return journalDetailMsg{title: date, body: "(empty)"}
		}

		body := htmlToText(content)

		var images [][]byte
		for _, imgURL := range extractImageURLs(content) {
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

		return journalDetailMsg{title: date, body: body, images: images}
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
