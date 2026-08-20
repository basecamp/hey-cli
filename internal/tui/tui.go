package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
)

// --- Shared messages ---

type errMsg struct{ err error } //nolint:errname // bubbletea convention

func (e errMsg) Error() string { return e.err.Error() }

type ctrlCResetMsg struct{}
type spinnerTickMsg struct{}

// --- Model ---

type model struct {
	width   int
	height  int
	vc      *viewContext
	rootSDK *hey.Client
	cancel  context.CancelFunc
	styles  styles
	help    helpBar

	// Navigation
	section    section
	focus      focusRow
	activeView sectionView

	// Section views (kept alive for state preservation)
	mailView     *mailView
	contactsView *contactsView
	calendarView *calendarView
	journalView  *journalView

	// Linked mail accounts
	mailAccounts            []mailAccountChoice
	mailAccount             mailAccountChoice
	mailAccountCursor       int
	mailAccountPicker       bool
	mailAccountSwitching    bool
	mailAccountUnavailable  bool
	mailAccountDiscoveryErr string
	mailAccountErr          string
	mailAccountRequestID    uint64
	viewGeneration          uint64
	viewGenerationToken     *atomic.Uint64

	// Loading & error
	loading      bool
	spinnerPhase float64
	err          error
	ctrlCOnce    bool
}

func newModel() model {
	return newModelWithMailAccounts(nil, nil, "all")
}

func newModelWithMailAccounts(rootSDK, sdk *hey.Client, selected string) model {
	s := newStyles()
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored, called on ctrl+c
	vc := newViewContext(ctx, rootSDK, sdk, s)
	mv := newMailView(vc)
	ov := newContactsView(vc)
	cv := newCalendarView(vc)
	jv := newJournalView(vc)
	account := mailAccountChoice{label: "All Accounts"}
	if accountID, err := strconv.ParseInt(selected, 10, 64); err == nil && accountID > 0 {
		account = mailAccountChoice{id: accountID, label: fmt.Sprintf("Account %d", accountID)}
	}

	return model{
		vc:                  vc,
		rootSDK:             rootSDK,
		cancel:              cancel,
		styles:              s,
		help:                newHelpBar(s),
		section:             sectionMail,
		focus:               rowContent,
		activeView:          mv,
		mailView:            mv,
		contactsView:        ov,
		calendarView:        cv,
		journalView:         jv,
		mailAccounts:        []mailAccountChoice{{label: "All Accounts"}},
		mailAccount:         account,
		viewGenerationToken: &atomic.Uint64{},
		loading:             true,
	}
}

func newViewContext(ctx context.Context, rootSDK, sdk *hey.Client, styles styles) *viewContext {
	return &viewContext{
		rootSDK:       rootSDK,
		sdk:           sdk,
		ctx:           ctx,
		styles:        styles,
		imageRenderer: environmentImageRenderer(),
		imageFetcher:  newTrustedImageFetcher(sdk),
		saveAttachment: func(ctx context.Context, destination, sourceURL string, force bool) (int64, error) {
			return attachmentfiles.Save(ctx, sdk, destination, sourceURL, force)
		},
		openAttachment: openExternalFile,
		newAttachmentTempDir: func() (string, error) {
			return os.MkdirTemp("", "hey-cli-attachment-*")
		},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.stampViewCmd(m.activeView.Init()),
		loadMailAccounts(m.vc.ctx, m.rootSDK, strconv.FormatInt(m.mailAccount.id, 10)),
		spinnerTick(),
	)
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case viewGenerationMsg:
		if msg.generation != m.viewGeneration {
			return m, nil
		}
		return m.Update(msg.msg)

	case mailAccountsLoadedMsg:
		if msg.err != nil {
			m.mailAccountDiscoveryErr = msg.err.Error()
			m.updateHelpBindings()
			return m, nil
		}
		m.mailAccountDiscoveryErr = ""
		m.mailAccounts = msg.accounts
		m.mailAccountUnavailable = msg.selectedUnavailable
		if msg.selectedUnavailable {
			m.loading = false
			m.mailAccountErr = fmt.Sprintf("Selected mail account %d is no longer available", m.mailAccount.id)
			m.err = errors.New(m.mailAccountErr)
		} else if msg.loaded && msg.selected >= 0 && msg.selected < len(msg.accounts) {
			m.mailAccount = msg.accounts[msg.selected]
			m.mailAccountCursor = msg.selected
		}
		m.updateHelpBindings()
		return m, nil

	case mailAccountSwitchedMsg:
		if msg.requestID != m.mailAccountRequestID {
			return m, nil
		}
		m.mailAccountSwitching = false
		if msg.err != nil {
			m.mailAccountErr = msg.err.Error()
			m.updateHelpBindings()
			return m, nil
		}
		return m.applyMailAccount(msg.account, msg.client)

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

	// Delegate to the active section view.
	cmd, consumed := m.activeView.Update(msg)
	if consumed {
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
		return m, cmd
	}
	return m, m.stampViewCmd(cmd)
}

// syncLoading synchronizes the main loading state with the active section view.
func (m *model) syncLoading(cmd tea.Cmd) tea.Cmd {
	cmd = m.stampViewCmd(cmd)
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

func (m model) stampViewCmd(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	generation := m.viewGeneration
	token := m.viewGenerationToken
	return func() tea.Msg {
		if token != nil && token.Load() != generation {
			return nil
		}
		msg := cmd()
		if msg == nil || (token != nil && token.Load() != generation) {
			return nil
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			stamped := make(tea.BatchMsg, len(batch))
			for index, batchCmd := range batch {
				stamped[index] = m.stampViewCmd(batchCmd)
			}
			return stamped
		}
		if _, ok := msg.(tea.RawMsg); ok {
			return msg
		}
		return viewGenerationMsg{generation: generation, msg: msg}
	}
}

func (m model) applyMailAccount(account mailAccountChoice, client *hey.Client) (tea.Model, tea.Cmd) {
	m.viewGeneration++
	m.viewGenerationToken.Store(m.viewGeneration)
	m.cancel()
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored, called on switch or quit
	m.cancel = cancel
	m.vc = newViewContext(ctx, m.rootSDK, client, m.styles)
	m.vc.width = m.width
	m.vc.height = max(m.height-headerHeight-m.help.height()-3, 1)
	m.mailView = newMailView(m.vc)
	m.contactsView = newContactsView(m.vc)
	m.calendarView = newCalendarView(m.vc)
	m.journalView = newJournalView(m.vc)
	switch m.section {
	case sectionMail:
		m.activeView = m.mailView
	case sectionContacts:
		m.activeView = m.contactsView
	case sectionCalendar:
		m.activeView = m.calendarView
	case sectionJournal:
		m.activeView = m.journalView
	}
	m.mailAccount = account
	m.mailAccountPicker = false
	m.mailAccountUnavailable = false
	m.mailAccountErr = ""
	m.err = nil
	m.focus = rowContent
	m.loading = false
	m.activeView.Resize(m.vc.width, m.vc.height)
	cmd := m.syncLoading(m.activeView.Init())
	m.updateHelpBindings()
	return m, cmd
}

// --- View ---

const headerHeight = 6

func (m model) View() tea.View {
	var b strings.Builder

	b.WriteString(renderHeader(&m))
	b.WriteString("\n")

	if m.mailAccountPicker {
		b.WriteString(renderMailAccountPicker(&m))
	} else if m.err != nil {
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

	if m.mailAccountPicker {
		bindings = []helpBinding{
			{"↑↓", "account"},
			{"enter", "switch"},
			{"esc/q", "cancel"},
			quitHint,
		}
	} else if ic, ok := m.activeView.(inputCapturer); ok && ic.CapturingInput() {
		bindings = append(m.activeView.HelpBindings(), quitHint)
	} else if m.activeView.InThread() {
		extra := m.activeView.HelpBindings()
		bindings = make([]helpBinding, 0, 3+len(extra))
		bindings = append(bindings,
			helpBinding{"↑↓", "scroll"},
			helpBinding{"esc/q", "back"},
		)
		bindings = append(bindings, extra...)
		bindings = append(bindings, quitHint)
	} else {
		switch m.focus {
		case rowSection:
			bindings = []helpBinding{
				{"←→", "section"},
				{"tab", "next row"},
				{"shift+M/O/C/J", "jump"},
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
			extra := m.activeView.HelpBindings()
			bindings = make([]helpBinding, 0, 5+len(extra))
			bindings = append(bindings,
				helpBinding{"↑↓", "navigate"},
				helpBinding{"enter", "open"},
				helpBinding{"tab", "next row"},
				helpBinding{"shift+tab", "prev row"},
				quitHint,
			)
			bindings = append(bindings, extra...)
		}
	}
	if m.canSwitchMailAccounts() && !m.mailAccountPicker && !m.loading && !m.activeView.InThread() {
		if ic, ok := m.activeView.(inputCapturer); !ok || !ic.CapturingInput() {
			description := "mail account"
			if m.mailAccountDiscoveryErr != "" {
				description = "retry accounts"
			}
			bindings = append(bindings, helpBinding{"ctrl+a", description})
		}
	}
	m.help.setBindings(bindings)
	contentHeight := m.height - headerHeight - m.help.height() - 3
	if contentHeight < 1 {
		contentHeight = 1
	}
	if contentHeight != m.vc.height {
		m.vc.height = contentHeight
		m.activeView.Resize(m.vc.width, contentHeight)
	}
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

	if m.mailAccountPicker {
		return m.handleMailAccountKey(msg)
	}

	// A view with an open text form gets every key (esc, tab, letters, ...).
	if ic, ok := m.activeView.(inputCapturer); ok && ic.CapturingInput() {
		cmd := m.activeView.HandleContentKey(msg)
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
		return m, cmd
	}

	if key == "ctrl+a" && m.canSwitchMailAccounts() && !m.loading && !m.activeView.InThread() {
		if m.mailAccountDiscoveryErr != "" {
			m.mailAccountDiscoveryErr = ""
			m.updateHelpBindings()
			return m, loadMailAccounts(m.vc.ctx, m.rootSDK, strconv.FormatInt(m.mailAccount.id, 10))
		}
		m.mailAccountPicker = true
		m.mailAccountErr = ""
		for index, account := range m.mailAccounts {
			if account.id == m.mailAccount.id {
				m.mailAccountCursor = index
				break
			}
		}
		m.updateHelpBindings()
		return m, nil
	}

	if msg.Key().Code == tea.KeyEscape || key == "q" {
		if m.activeView.InThread() {
			if exiter, ok := m.activeView.(detailExiter); ok {
				exiter.ExitDetail(key)
			} else {
				m.activeView.ExitThread()
			}
			m.updateHelpBindings()
			m.activeView.Resize(m.vc.width, m.vc.height)
			return m, m.syncLoading(nil)
		}
		if canceler, ok := m.activeView.(pendingDetailCanceler); ok && canceler.CancelPendingDetail() {
			m.updateHelpBindings()
			return m, m.syncLoading(nil)
		}
		return m, nil
	}

	if m.loading {
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

func (m model) canSwitchMailAccounts() bool {
	return m.mailAccountDiscoveryErr != "" || len(m.mailAccounts) > 2 || (m.mailAccountUnavailable && len(m.mailAccounts) > 0)
}

func (m model) handleMailAccountKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if msg.Key().Code == tea.KeyEscape || key == "q" {
		if !m.mailAccountSwitching {
			m.mailAccountPicker = false
			m.mailAccountErr = ""
			m.updateHelpBindings()
		}
		return m, nil
	}
	if m.mailAccountSwitching {
		return m, nil
	}
	switch msg.Key().Code {
	case tea.KeyUp:
		if m.mailAccountCursor > 0 {
			m.mailAccountCursor--
		}
	case tea.KeyDown:
		if m.mailAccountCursor < len(m.mailAccounts)-1 {
			m.mailAccountCursor++
		}
	case tea.KeyEnter:
		account := m.mailAccounts[m.mailAccountCursor]
		if account.id == m.mailAccount.id {
			m.mailAccountPicker = false
			m.mailAccountErr = ""
			m.updateHelpBindings()
			return m, nil
		}
		m.mailAccountRequestID++
		m.mailAccountSwitching = true
		m.mailAccountErr = ""
		m.updateHelpBindings()
		return m, switchMailAccount(m.vc.ctx, m.rootSDK, account, m.mailAccountRequestID)
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
	case sectionContacts:
		m.activeView = m.contactsView
	case sectionCalendar:
		m.activeView = m.calendarView
	case sectionJournal:
		m.activeView = m.journalView
	}
	m.activeView.Resize(m.vc.width, m.vc.height)
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

// Run starts the TUI with the resolved mail account and the identity root client used
// for interactive account switching.
func Run(rootSDK, sdk *hey.Client, selected string) error {
	p := tea.NewProgram(newModelWithMailAccounts(rootSDK, sdk, selected))
	_, err := p.Run()
	return err
}
