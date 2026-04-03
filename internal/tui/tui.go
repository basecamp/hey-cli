package tui

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// --- Shared messages ---

type errMsg struct{ err error } //nolint:errname // bubbletea convention

func (e errMsg) Error() string { return e.err.Error() }

type ctrlCResetMsg struct{}
type spinnerTickMsg struct{}

// --- Model ---

type model struct {
	width  int
	height int
	vc     *viewContext
	cancel context.CancelFunc
	styles styles
	help   helpBar

	// Navigation
	section    section
	focus      focusRow
	activeView sectionView

	// Section views (kept alive for state preservation)
	mailView     *mailView
	calendarView *calendarView
	journalView  *journalView

	// Loading & error
	loading      bool
	spinnerPhase float64
	err          error
	ctrlCOnce    bool
}

func newModel(sdk *hey.Client) model {
	s := newStyles()
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored, called on ctrl+c
	vc := &viewContext{sdk: sdk, ctx: ctx, styles: s}

	mv := newMailView(vc)
	cv := newCalendarView(vc)
	jv := newJournalView(vc)

	return model{
		vc:           vc,
		cancel:       cancel,
		styles:       s,
		help:         newHelpBar(s),
		section:      sectionMail,
		focus:        rowContent,
		activeView:   mv,
		mailView:     mv,
		calendarView: cv,
		journalView:  jv,
		loading:      true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.activeView.Init(), spinnerTick())
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vc.width = msg.Width
		contentH := msg.Height - headerHeight - m.help.height() - 3
		if contentH < 1 {
			contentH = 1
		}
		m.vc.height = contentH
		m.help.setWidth(msg.Width)
		m.activeView.Resize(msg.Width, contentH)
		m.updateHelpBindings()
		return m, nil

	case spinnerTickMsg:
		if m.loading {
			m.spinnerPhase += 0.15
			return m, spinnerTick()
		}
		return m, nil

	case ctrlCResetMsg:
		m.ctrlCOnce = false
		m.updateHelpBindings()
		return m, nil

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Delegate to active section view
	cmd, consumed := m.activeView.Update(msg)
	if consumed {
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
	}
	return m, cmd
}

// syncLoading synchronizes the main loading state with the active section view.
func (m *model) syncLoading(cmd tea.Cmd) tea.Cmd {
	nowLoading := m.activeView.Loading()
	if nowLoading && !m.loading {
		m.loading = true
		return tea.Batch(cmd, spinnerTick())
	}
	if !nowLoading && m.loading {
		m.loading = false
	}
	return cmd
}

// --- View ---

const headerHeight = 6

func (m model) View() tea.View {
	var b strings.Builder

	b.WriteString(renderHeader(&m))
	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(errorView(m.err.Error(), m.width))
	} else if m.loading {
		contentH := m.height - headerHeight - m.help.height() - 3
		if contentH < 1 {
			contentH = 1
		}
		b.WriteString(loadingView(m.width, contentH, m.spinnerPhase))
	} else {
		b.WriteString(m.activeView.View())
	}

	contentLines := strings.Count(b.String(), "\n")
	helpView := m.help.view()
	helpH := 0
	if helpView != "" {
		helpH = strings.Count(helpView, "\n") + 1
	}
	footerH := 1 + helpH
	padLines := m.height - contentLines - footerH - 1
	for range max(padLines, 0) {
		b.WriteString("\n")
	}

	b.WriteString(renderRule(m.width, ""))
	if helpView != "" {
		b.WriteString("\n" + helpView)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m *model) updateHelpBindings() {
	quitHint := helpBinding{"ctrl+c ctrl+c", "quit"}
	if m.ctrlCOnce {
		quitHint = helpBinding{"ctrl+c", "press again to quit"}
	}

	var bindings []helpBinding

	if m.activeView.InThread() {
		bindings = []helpBinding{
			{"↑↓", "scroll"},
			{"esc/q", "back"},
			quitHint,
		}
	} else {
		switch m.focus {
		case rowSection:
			bindings = []helpBinding{
				{"←→", "section"},
				{"tab", "next row"},
				{"shift+M/C/J", "jump"},
				quitHint,
			}
		case rowSubnav:
			bindings = []helpBinding{
				{"←→", "switch"},
				{"tab", "next row"},
				{"shift+tab", "prev row"},
				quitHint,
			}
		case rowContent:
			bindings = []helpBinding{
				{"↑↓", "navigate"},
				{"enter", "open"},
				{"tab", "next row"},
				{"shift+tab", "prev row"},
				quitHint,
			}
			bindings = append(bindings, m.activeView.HelpBindings()...)
		}
	}
	m.help.setBindings(bindings)
}

// --- Key handling ---

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		if m.ctrlCOnce {
			m.cancel()
			return m, tea.Quit
		}
		m.ctrlCOnce = true
		m.updateHelpBindings()
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ctrlCResetMsg{} })
	}
	if m.ctrlCOnce {
		m.ctrlCOnce = false
		m.updateHelpBindings()
	}

	if msg.Key().Code == tea.KeyEscape || key == "q" {
		if m.activeView.InThread() {
			m.activeView.ExitThread()
			m.updateHelpBindings()
			m.activeView.Resize(m.vc.width, m.vc.height)
			return m, nil
		}
		return m, nil
	}

	if msg.Key().Code == tea.KeyTab {
		if msg.Key().Mod == tea.ModShift {
			m.focus = (m.focus + 2) % 3
		} else {
			m.focus = (m.focus + 1) % 3
		}
		m.updateHelpBindings()
		return m, nil
	}

	if sec := sectionForShortcut(key); sec >= 0 {
		return m.switchSection(sec)
	}

	// Mail box shortcuts (global when in mail section)
	if m.section == sectionMail {
		if cmd := m.mailView.handleBoxShortcut(key); cmd != nil {
			return m, m.syncLoading(cmd)
		}
	}

	switch m.focus {
	case rowSection:
		return m.handleSectionKeys(msg)
	case rowSubnav:
		cmd := m.handleSubnavKey(msg)
		return m, m.syncLoading(cmd)
	case rowContent:
		cmd := m.activeView.HandleContentKey(msg)
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
		return m, cmd
	}

	return m, nil
}

func (m model) switchSection(sec section) (tea.Model, tea.Cmd) {
	if sec == m.section {
		return m, nil
	}
	m.section = sec
	switch sec {
	case sectionMail:
		m.activeView = m.mailView
	case sectionCalendar:
		m.activeView = m.calendarView
	case sectionJournal:
		m.activeView = m.journalView
	}
	cmd := m.activeView.Init()
	cmd = m.syncLoading(cmd)
	m.updateHelpBindings()
	return m, cmd
}

func (m model) handleSectionKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Key().Code {
	case tea.KeyLeft:
		if m.section > 0 {
			return m.switchSection(m.section - 1)
		}
	case tea.KeyRight:
		if m.section < sectionJournal {
			return m.switchSection(m.section + 1)
		}
	}
	return m, nil
}

func (m model) handleSubnavKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyLeft:
		return m.activeView.SubnavLeft()
	case tea.KeyRight:
		return m.activeView.SubnavRight()
	}
	return nil
}

// --- Shared utilities ---

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format("2006-01-02T15:04:05Z")
}

var imageHTTPClient = &http.Client{Timeout: 10 * time.Second}

func fetchImageData(imgURL string) []byte {
	req, err := http.NewRequestWithContext(context.Background(), "GET", imgURL, nil)
	if err != nil {
		return nil
	}
	resp, err := imageHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	return data
}

// Run starts the TUI program.
func Run(sdk *hey.Client) error {
	p := tea.NewProgram(newModel(sdk))
	_, err := p.Run()
	return err
}
