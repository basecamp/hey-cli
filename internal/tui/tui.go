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
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// --- Shared messages ---

type errMsg struct{ err error } //nolint:errname // bubbletea convention

func (e errMsg) Error() string { return e.err.Error() }

type ctrlCResetMsg struct{}
type spinnerTickMsg struct{}

// TopicRequest identifies a thread to open in the TUI. AccountID selects a
// linked account when the request comes from another process.
type TopicRequest struct {
	TopicID   int64  `json:"topic_id"`
	AccountID int64  `json:"account_id,omitempty"`
	Title     string `json:"title,omitempty"`
}

// Options configures the TUI's initial destination.
type Options struct {
	OpenTopic TopicRequest
	Instance  string
}

// --- Model ---

type model struct {
	width          int
	height         int
	vc             *viewContext
	rootSDK        *hey.Client
	cancel         context.CancelFunc
	theme          Theme
	styles         styles
	help           helpBar
	saveHelpHidden func(bool) error

	// Navigation
	section    section
	focus      focusRow
	activeView sectionView

	// Section views (kept alive for state preservation)
	mailView     *mailView
	contactsView *contactsView
	calendarView *calendarView
	journalView  *journalView

	// The Screener, opened over the mail section with ctrl+s
	screenerView *screenerView

	// Live mail updates: the watcher, the stream it opened, and the context that keeps
	// the stream open. It outlives the view context, which a mail account switch throws
	// away — the changes stream is the same one whichever account is being read.
	watchMail         MailWatcher
	mailWatchEvents   <-chan MailWatchEvent
	mailWatchAttempt  uint64
	mailWatchFailures int
	mailWatchStatus   mailWatchStatus
	mailWatchReason   string
	watchCtx          context.Context
	stopWatching      context.CancelFunc

	// The Screener's stream, which can only be opened once HEY has served its name, and
	// the context that holds it open. Cancelling that context is how the stream behind an
	// old name is given up, so a new one does not stack on top of it.
	watchScreener      ScreenerWatcher
	screenerChanges    <-chan struct{}
	screenerStream     string
	stopScreenerWatch  context.CancelFunc
	screenerRefreshDue bool

	// Linked mail accounts
	mailAccounts            []mailAccountChoice
	mailAccountsLoaded      bool
	mailSourcesLoaded       bool
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

	// What just happened, in the top right corner until its clock runs out
	toast   notifyMsg
	toastID uint64

	// Loading & error
	loading      bool
	spinnerPhase float64
	err          error
	ctrlCOnce    bool
	pendingTopic *TopicRequest
}

func newModel() model {
	return newModelWithMailAccounts(nil, nil, "all", Watchers{})
}

func newModelWithMailAccounts(rootSDK, sdk *hey.Client, selected string, watchers Watchers) model {
	theme := ResolveTheme()
	applyTheme(theme)
	s := newStyles()
	ctx, cancel := context.WithCancel(context.Background())            //nolint:gosec // G118: cancel stored, called on ctrl+c
	watchCtx, stopWatching := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored, called on ctrl+c
	vc := newViewContext(ctx, rootSDK, sdk, s)
	mv := newMailView(vc)
	ov := newContactsView(vc)
	cv := newCalendarView(vc)
	jv := newJournalView(vc)
	sv := newScreenerView(vc)
	account := mailAccountChoice{label: "All Accounts"}
	if accountID, err := strconv.ParseInt(selected, 10, 64); err == nil && accountID > 0 {
		account = mailAccountChoice{id: accountID, label: fmt.Sprintf("Account %d", accountID)}
	}

	return model{
		vc:                  vc,
		rootSDK:             rootSDK,
		cancel:              cancel,
		theme:               theme,
		styles:              s,
		help:                newHelpBar(s),
		section:             sectionMail,
		focus:               rowContent,
		activeView:          mv,
		mailView:            mv,
		contactsView:        ov,
		calendarView:        cv,
		journalView:         jv,
		screenerView:        sv,
		watchMail:           watchers.Mail,
		watchScreener:       watchers.Screener,
		mailWatchAttempt:    1,
		watchCtx:            watchCtx,
		stopWatching:        stopWatching,
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
		loadCover:        config.Cover,
		saveCover:        config.SaveCover,
		loadLastCalendar: config.LastCalendarID,
		saveLastCalendar: config.SaveLastCalendarID,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.stampViewCmd(m.activeView.Init()),
		loadMailAccounts(m.vc.ctx, m.rootSDK, strconv.FormatInt(m.mailAccount.id, 10)),
		spinnerTick(),
		tea.RequestBackgroundColor,
		watchThemeCmd(omarchyWatchDir(userHomeDir())),
		startMailWatchCmd(m.watchCtx, m.watchMail, m.mailWatchAttempt),
	)
}

// restyle makes theme the active palette and refreshes every copy of the styles:
// the model's, the view context's, the help bar's, and whatever the sections cached.
func (m *model) restyle(theme Theme) {
	m.theme = theme
	applyTheme(theme)
	m.styles = newStyles()
	m.vc.styles = m.styles
	m.help.setStyles(m.styles)
	for _, view := range []sectionView{m.mailView, m.contactsView, m.calendarView, m.journalView} {
		if view != nil {
			view.Restyle()
		}
	}
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case viewGenerationMsg:
		if msg.generation != m.viewGeneration {
			return m, nil
		}
		return m.Update(msg.msg)

	case notifyMsg:
		return m, m.showToast(msg)

	case TopicRequest:
		return m.openTopic(msg)

	case toastExpiredMsg:
		if msg.id == m.toastID {
			m.toast = notifyMsg{}
		}
		return m, nil

	case mailAccountsLoadedMsg:
		if msg.err != nil {
			m.mailAccountDiscoveryErr = errorNotice("Could not load the mail accounts", msg.err)
			m.updateHelpBindings()
			return m, nil
		}
		m.mailAccountsLoaded = true
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
		if m.pendingTopic != nil {
			request := *m.pendingTopic
			return m.openTopic(request)
		}
		return m, nil

	case mailAccountSwitchedMsg:
		if msg.requestID != m.mailAccountRequestID {
			return m, nil
		}
		m.mailAccountSwitching = false
		if msg.err != nil {
			m.mailAccountErr = errorNotice("Could not switch account", msg.err)
			m.pendingTopic = nil
			m.updateHelpBindings()
			return m, nil
		}
		if m.pendingTopic != nil {
			m.section = sectionMail
		}
		updated, initCmd := m.applyMailAccount(msg.account, msg.client)
		next, ok := updated.(model)
		if !ok {
			return m, initCmd
		}
		m = next
		if m.pendingTopic == nil {
			return m, initCmd
		}
		request := *m.pendingTopic
		m.pendingTopic = nil
		opened, topicCmd := m.openTopic(request)
		return opened, tea.Batch(initCmd, topicCmd)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vc.width = msg.Width
		m.help.setWidth(msg.Width)
		contentH := m.contentHeight()
		m.vc.height = contentH
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

	case tea.BackgroundColorMsg:
		// The terminal knows its background; a theme file that states a mode knows better.
		if !m.theme.HasMode && m.theme.Dark != msg.IsDark() {
			theme := m.theme
			theme.Dark = msg.IsDark()
			m.restyle(theme)
		}
		return m, nil

	case themeChangedMsg:
		// omarchy-theme-set removes the theme directory before moving the new one in.
		// Mid-swap there is nothing to read yet; keep the current palette and wait
		// for the move.
		if theme := ResolveTheme(); theme.Source != "" || omarchyThemeDir(userHomeDir()) == "" {
			if !theme.HasMode {
				// Carry the last known mode for now and ask the terminal again:
				// the switch usually retints the terminal too, and the
				// BackgroundColorMsg reply corrects a mode-less theme.
				theme.Dark = m.theme.Dark
			}
			m.restyle(theme)
		}
		return m, tea.Batch(watchThemeCmd(omarchyWatchDir(userHomeDir())), tea.RequestBackgroundColor)

	case screenerClosedMsg:
		return m.closeScreener()

	case mailWatchStartedMsg:
		if msg.attempt != m.mailWatchAttempt {
			return m, nil
		}
		if msg.err != nil {
			cmd := m.mailWatchFailed(msg.err)
			m.updateHelpBindings()
			return m, cmd
		}
		catchUp := m.mailWatchStatus != mailWatchLive || m.mailWatchFailures > 0
		m.mailWatchEvents = msg.events
		m.mailWatchConnected()
		m.updateHelpBindings()
		wait := waitForMailWatchEventCmd(m.mailWatchEvents)
		if catchUp {
			return m, tea.Batch(m.stampViewCmd(m.mailView.boxChanged(AnyBoxChanged)), wait)
		}
		return m, wait

	case mailWatchEventMsg:
		if msg.closed {
			m.mailWatchEvents = nil
			cmd := m.mailWatchStopped()
			m.updateHelpBindings()
			return m, cmd
		}
		wait := waitForMailWatchEventCmd(m.mailWatchEvents)
		switch msg.event.Connection {
		case MailConnectionUnchanged:
			// The box event below is the ordinary live-update path.
		case MailConnectionDisconnected:
			m.mailWatchDisconnected(msg.event.WillReconnect)
			m.updateHelpBindings()
			return m, wait
		case MailConnectionReconnected:
			m.mailWatchConnected()
			m.updateHelpBindings()
			return m, tea.Batch(m.stampViewCmd(m.mailView.boxChanged(AnyBoxChanged)), wait)
		}
		// The stream is listened to for as long as the TUI runs, whichever section is
		// on screen: mail that arrived while the user was in the calendar is there when
		// they come back.
		return m, tea.Batch(m.stampViewCmd(m.mailView.boxChanged(msg.event.BoxID)), wait)

	case mailWatchRetryMsg:
		if msg.attempt != m.mailWatchAttempt || m.mailWatchEvents != nil {
			return m, nil
		}
		m.mailWatchAttempt++
		return m, startMailWatchCmd(m.watchCtx, m.watchMail, m.mailWatchAttempt)

	case mailRefreshDueMsg, postingsRefreshedMsg:
		cmd, _ := m.mailView.Update(msg)
		return m, m.stampViewCmd(cmd)

	case screenerWatchStartedMsg:
		if msg.stream != m.screenerStream {
			return m, nil
		}
		if msg.err != nil {
			m.dropScreenerWatch()
			m.mailView.screenerUpdatesUnavailable(msg.err)
			return m, nil
		}
		m.screenerChanges = msg.changes
		return m, waitForScreenerChangeCmd(m.screenerStream, m.screenerChanges)

	case screenerChangedMsg:
		if msg.stream != m.screenerStream {
			return m, nil
		}
		if msg.closed {
			// Letting the name go means the next read of the count opens the stream again.
			m.dropScreenerWatch()
			m.mailView.screenerUpdatesStopped()
			return m, nil
		}
		return m, tea.Batch(m.screenerChanged(), waitForScreenerChangeCmd(m.screenerStream, m.screenerChanges))

	case screenerRefreshDueMsg:
		return m, m.refreshScreener()

	case screenerPendingRefreshedMsg:
		cmd, _ := m.screenerView.Update(msg)
		return m, m.stampViewCmd(cmd)

	case mailSourcesLoadedMsg:
		// HEY serves The Screener's stream name with its count, and the sources read asks
		// for both, so this is where the watch is opened first.
		watch := m.startScreenerWatch(msg.screenerStream)
		var cmd tea.Cmd
		if m.activeView != m.mailView {
			cmd, _ = m.mailView.Update(msg)
			cmd = m.stampViewCmd(cmd)
		} else {
			cmd, _ = m.activeView.Update(msg)
			cmd = m.syncLoading(cmd)
			m.updateHelpBindings()
		}
		m.mailSourcesLoaded = true
		if m.pendingTopic != nil {
			request := *m.pendingTopic
			opened, topicCmd := m.openTopic(request)
			return opened, tea.Batch(cmd, watch, topicCmd)
		}
		return m, tea.Batch(cmd, watch)

	case screenerCountLoadedMsg:
		// The count comes with the stream name, so every re-read of the count — ctrl+r,
		// closing The Screener, the doorbell — is a chance to open a stream that closed.
		watch := m.startScreenerWatch(msg.screenerStream)
		if m.activeView != m.mailView {
			cmd, _ := m.mailView.Update(msg)
			return m, tea.Batch(m.stampViewCmd(cmd), watch)
		}
		cmd, _ := m.activeView.Update(msg)
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
		return m, tea.Batch(cmd, watch)

	case postingsLoadedMsg:
		if m.activeView != m.mailView {
			if !m.mailView.acceptsPostingsLoaded(msg) {
				return m, nil
			}
			if msg.err != nil {
				m.mailView.requests.finish(msg.requestID)
				m.mailView.notice = errorNotice("Could not load mail", msg.err)
				return m, nil
			}
			cmd, _ := m.mailView.Update(msg)
			return m, m.stampViewCmd(cmd)
		}

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
	// The Screener's stream is the account's, unlike the changes stream, and the new
	// account's sources read names its own. Until then there is nothing to follow.
	m.dropScreenerWatch()
	m.viewGeneration++
	m.viewGenerationToken.Store(m.viewGeneration)
	m.cancel()
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored, called on switch or quit
	m.cancel = cancel
	m.vc = newViewContext(ctx, m.rootSDK, client, m.styles)
	m.vc.width = m.width
	m.vc.height = m.contentHeight()
	m.mailView = newMailView(m.vc)
	m.contactsView = newContactsView(m.vc)
	m.calendarView = newCalendarView(m.vc)
	m.journalView = newJournalView(m.vc)
	m.screenerView = newScreenerView(m.vc)
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
	m.mailSourcesLoaded = false
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

const headerHeight = 6 // five drawn rows and the terminal's final safety row

// contentView is the content area on its own, which is what a modal draws itself over.
func (m model) contentView() string {
	switch {
	case m.err != nil:
		return errorView(m.err.Error(), m.width)
	case m.loading:
		return loadingView(m.width, m.contentHeight(), m.spinnerPhase)
	default:
		return m.activeView.View()
	}
}

func (m model) View() tea.View {
	var b strings.Builder

	b.WriteString(renderHeader(&m))
	b.WriteString("\n")
	if notice := m.mailWatchNotice(); notice != "" {
		b.WriteString(m.styles.title.Render(truncateStr(notice, max(m.width, 1))))
		b.WriteString("\n")
	}

	content := m.contentView()
	if m.mailAccountPicker {
		content = renderMailAccountPicker(&m, content)
	}
	// The toast goes on last, over the modals too: it is the answer to what the reader
	// just did, and a form open over the list does not make it less so.
	if toast := m.toastView(); toast != "" {
		x := max(m.width-lipgloss.Width(toast)-1, 0)
		content = overlayAt(content, toast, x, 0, m.width, m.contentHeight())
	}
	b.WriteString(content)

	helpView := m.help.view()
	if helpView != "" {
		contentLines := strings.Count(b.String(), "\n")
		helpH := strings.Count(helpView, "\n") + 1
		footerH := 1 + helpH
		padLines := m.height - contentLines - footerH - 1
		for range max(padLines, 0) {
			b.WriteString("\n")
		}

		b.WriteString(renderRule(m.width, ""))
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
	if m.canToggleHelp() && !m.help.hidden {
		bindings = append(bindings, helpBinding{"?", "toggle help"})
	}
	if m.canOpenMailAccountPicker() && !m.mailAccountPicker {
		if ic, ok := m.activeView.(inputCapturer); !ok || !ic.CapturingInput() {
			description := "mail account"
			if m.mailAccountDiscoveryErr != "" {
				description = "retry accounts"
			}
			bindings = append(bindings, helpBinding{"ctrl+a", description})
		}
	}
	m.help.setBindings(bindings)
	contentHeight := m.contentHeight()
	if contentHeight != m.vc.height {
		m.vc.height = contentHeight
		m.activeView.Resize(m.vc.width, contentHeight)
	}
}

// contentHeight gives the active view every row that is not navigation or a
// visible help footer. The footer carries two clear rows above its divider.
func (m model) contentHeight() int {
	footerHeight := 0
	if helpHeight := m.help.height(); helpHeight > 0 {
		footerHeight = helpHeight + 3
	}
	statusHeight := 0
	if m.mailWatchNotice() != "" {
		statusHeight = 1
	}
	return max(m.height-headerHeight-footerHeight-statusHeight, 1)
}

// --- Key handling ---

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.help.notice != "" {
		m.help.setNotice("")
		m.updateHelpBindings()
	}

	if key == "ctrl+c" {
		if m.ctrlCOnce {
			m.cancel()
			m.stopWatching()
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

	if key == "?" && m.canToggleHelp() {
		return m.toggleHelp()
	}

	// A view with an open text form gets every key (esc, tab, letters, ...).
	if ic, ok := m.activeView.(inputCapturer); ok && ic.CapturingInput() {
		cmd := m.activeView.HandleContentKey(msg)
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
		return m, cmd
	}

	if key == "ctrl+s" && m.canOpenScreener() {
		return m.openScreener()
	}

	if key == "ctrl+a" && m.canOpenMailAccountPicker() {
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
			m.updateHelpBindings()
			return m, m.syncLoading(cmd)
		}
	}

	switch m.focus {
	case rowSection:
		return m.handleSectionKeys(msg)
	case rowSubnav:
		cmd := m.handleSubnavKey(msg)
		m.updateHelpBindings()
		return m, m.syncLoading(cmd)
	case rowContent:
		cmd := m.activeView.HandleContentKey(msg)
		cmd = m.syncLoading(cmd)
		m.updateHelpBindings()
		return m, cmd
	}

	return m, nil
}

func (m model) canToggleHelp() bool {
	if m.mailAccountPicker {
		return false
	}
	if m.activeView == m.screenerView {
		return true
	}
	capturer, ok := m.activeView.(inputCapturer)
	return !ok || !capturer.CapturingInput()
}

func (m model) toggleHelp() (tea.Model, tea.Cmd) {
	hidden := !m.help.hidden
	m.help.setHidden(hidden)
	if m.saveHelpHidden != nil {
		if err := m.saveHelpHidden(hidden); err != nil {
			m.help.setNotice(errorNotice("Could not save the help preference", err))
		}
	}
	m.updateHelpBindings()
	return m, nil
}

func (m model) canSwitchMailAccounts() bool {
	return m.mailAccountDiscoveryErr != "" || len(m.mailAccounts) > 2 || (m.mailAccountUnavailable && len(m.mailAccounts) > 0)
}

func (m model) canOpenMailAccountPicker() bool {
	return m.canSwitchMailAccounts() && !m.loading && !m.activeView.InThread() && !m.hasPendingMutation()
}

func (m model) hasPendingMutation() bool {
	views := []sectionView{m.activeView}
	if m.mailView != nil && m.activeView != m.mailView {
		views = append(views, m.mailView)
	}
	if m.contactsView != nil && m.activeView != m.contactsView {
		views = append(views, m.contactsView)
	}
	for _, view := range views {
		if blocker, ok := view.(accountSwitchBlocker); ok && blocker.AccountSwitchBlocked() {
			return true
		}
	}
	return false
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

// canOpenScreener reports whether ctrl+s should open The Screener: from the mail list,
// with no thread, search or form in the way.
func (m model) canOpenScreener() bool {
	return m.section == sectionMail && m.activeView == m.mailView && !m.loading && m.err == nil &&
		!m.mailView.InThread() && !m.hasPendingMutation()
}

func (m model) openScreener() (tea.Model, tea.Cmd) {
	m.activeView = m.screenerView
	m.activeView.Resize(m.vc.width, m.vc.height)
	cmd := m.syncLoading(m.activeView.Init())
	m.updateHelpBindings()
	return m, cmd
}

func (m model) closeScreener() (tea.Model, tea.Cmd) {
	if m.activeView != m.screenerView {
		return m, nil
	}
	m.activeView = m.mailView
	m.activeView.Resize(m.vc.width, m.vc.height)
	cmd := m.syncLoading(m.mailView.refreshScreenerCount())
	m.updateHelpBindings()
	return m, cmd
}

// startScreenerWatch opens the Screener's stream the first time HEY names it, and again
// only if the name changes. Every read of the count carries the name, so without this the
// watch would be reopened on each one.
func (m *model) startScreenerWatch(signedStreamName string) tea.Cmd {
	if signedStreamName == "" || signedStreamName == m.screenerStream {
		return nil
	}
	m.dropScreenerWatch()
	watchCtx, stop := context.WithCancel(m.watchCtx) //nolint:gosec // G118: cancel stored, called on the next watch, an account switch or ctrl+c
	m.screenerStream = signedStreamName
	m.stopScreenerWatch = stop
	return startScreenerWatchCmd(watchCtx, m.watchCtx, m.watchScreener, signedStreamName)
}

// dropScreenerWatch gives up the stream the TUI was following. The subscription behind it
// belongs to the watch's context, so cancelling is what ends it: left open it goes on
// ringing the doorbell for a name nobody is showing, and holds a subscription, a channel
// and two goroutines for as long as the TUI runs.
func (m *model) dropScreenerWatch() {
	if m.stopScreenerWatch != nil {
		m.stopScreenerWatch()
	}
	m.stopScreenerWatch = nil
	m.screenerStream = ""
	m.screenerChanges = nil
}

// screenerChanged is the Screener's doorbell. Screening a queue in one go rings it once
// per sender, so the re-read is delayed and one is armed at a time.
func (m *model) screenerChanged() tea.Cmd {
	if m.screenerRefreshDue {
		return nil
	}
	m.screenerRefreshDue = true
	return refreshScreenerLaterCmd(liveRefreshDelay)
}

// refreshScreener reads the count again wherever the user is — it is the mail list's
// standing invitation — and reads the queue again as well when The Screener is what is
// on screen.
func (m *model) refreshScreener() tea.Cmd {
	m.screenerRefreshDue = false
	if m.activeView != m.screenerView {
		return m.stampViewCmd(m.mailView.refreshScreenerCount())
	}

	queue, held := m.screenerView.refreshPending()
	if held {
		m.screenerRefreshDue = true
		return refreshScreenerLaterCmd(liveRetryDelay)
	}
	return tea.Batch(m.stampViewCmd(m.mailView.refreshScreenerCount()), m.stampViewCmd(queue))
}

func (m model) switchSection(sec section) (tea.Model, tea.Cmd) {
	if sec == m.section || m.hasPendingMutation() {
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

func (m model) openTopic(request TopicRequest) (tea.Model, tea.Cmd) {
	if request.TopicID <= 0 {
		return m, nil
	}
	capturingInput := false
	if m.activeView != m.screenerView {
		capturer, ok := m.activeView.(inputCapturer)
		capturingInput = ok && capturer.CapturingInput()
	}
	if m.hasPendingMutation() || capturingInput {
		m.pendingTopic = nil
		return m, notify("Finish the current action before opening another thread")
	}
	if request.AccountID > 0 && m.mailAccount.id != request.AccountID {
		m.pendingTopic = &request
		if !m.mailAccountsLoaded {
			return m, nil
		}
		for _, account := range m.mailAccounts {
			if account.id != request.AccountID {
				continue
			}
			m.mailAccountRequestID++
			m.mailAccountSwitching = true
			return m, switchMailAccount(m.vc.ctx, m.rootSDK, account, m.mailAccountRequestID)
		}
		m.pendingTopic = nil
		m.mailAccountErr = fmt.Sprintf("Mail account %d is not available", request.AccountID)
		m.updateHelpBindings()
		return m, nil
	}
	if m.mailAccountSwitching {
		m.mailAccountRequestID++
		m.mailAccountSwitching = false
		m.updateHelpBindings()
	}
	if !m.mailSourcesLoaded {
		m.pendingTopic = &request
		return m, nil
	}
	m.pendingTopic = nil
	m.section = sectionMail
	m.focus = rowContent
	m.activeView = m.mailView
	m.activeView.Resize(m.vc.width, m.vc.height)
	m.updateHelpBindings()
	title := terminal.SanitizeLine(request.Title)
	return m, m.syncLoading(m.mailView.requestTopic(0, request.TopicID, 0, title))
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

// Run starts the TUI with the resolved mail account, the identity root client used for
// interactive account switching, the live watchers, and an optional initial thread.
func Run(rootSDK, sdk *hey.Client, selected string, watchers Watchers, options Options) error {
	calibrateWidths(os.Stdin, os.Stdout)
	m := newModelWithMailAccounts(rootSDK, sdk, selected, watchers)
	if options.OpenTopic.TopicID > 0 {
		request := options.OpenTopic
		m.pendingTopic = &request
	}
	m.help.setHidden(config.HelpHidden())
	m.saveHelpHidden = config.SaveHelpHidden
	p := tea.NewProgram(m)
	listener, err := startTopicListener(options.Instance, p.Send)
	if err != nil {
		return err
	}
	defer closeTopicListener(listener)
	_, err = p.Run()
	return err
}
