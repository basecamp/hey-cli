package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func TestLoadMailAccountsUsesAccessibleLinkedAccounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"id":1,
			"accounts":[
				{"id":1,"name":"Personal","purpose":"home","status":"active"},
				{"id":2,"name":"Canceled","purpose":"home","status":"canceled"},
				{"id":3,"name":"Work","purpose":"work","status":"inactive"}
			],
			"all_users":[
				{"id":11,"account_id":1,"contact":{"id":101,"email_address":"jane@example.com"}},
				{"id":33,"account_id":3,"contact":{"id":303,"email_address":"jane@company.example"}}
			]
		}`)
	}))
	t.Cleanup(server.Close)
	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "token"}, hey.WithMaxRetries(0))

	msg, ok := loadMailAccounts(t.Context(), client, "3")().(mailAccountsLoadedMsg)
	if !ok {
		t.Fatal("account loader returned the wrong message type")
	}
	if !msg.loaded || msg.selected != 2 || len(msg.accounts) != 3 {
		t.Fatalf("loaded accounts = %#v", msg)
	}
	if msg.accounts[0].label != "All Accounts" || msg.accounts[1].label != "jane@example.com" || msg.accounts[2].label != "jane@company.example" {
		t.Fatalf("account labels = %#v", msg.accounts)
	}
}

func TestUnavailableSelectedAccountFailsClosedAndAllowsRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":1,"accounts":[{"id":1,"status":"active"}]}`)
	}))
	t.Cleanup(server.Close)
	root := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "token"}, hey.WithMaxRetries(0))
	m := newModelWithMailAccounts(root, root, "99")

	loaded := loadMailAccounts(t.Context(), root, "99")().(mailAccountsLoadedMsg)
	if !loaded.selectedUnavailable {
		t.Fatal("unavailable selected account was accepted")
	}
	updated, _ := m.Update(loaded)
	m = updated.(model)
	if m.err == nil || !m.canSwitchMailAccounts() {
		t.Fatalf("unavailable account did not fail closed with recovery: err=%v switch=%v", m.err, m.canSwitchMailAccounts())
	}
}

func TestAccountDiscoveryFailureCanRetry(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":"try again"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":1,"accounts":[{"id":1,"status":"active"},{"id":2,"status":"active"}]}`)
	}))
	t.Cleanup(server.Close)
	root := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "token"}, hey.WithMaxRetries(0))
	m := newModelWithMailAccounts(root, root, "all")
	m.loading = false

	failed := loadMailAccounts(t.Context(), root, "all")().(mailAccountsLoadedMsg)
	updated, _ := m.Update(failed)
	m = updated.(model)
	if m.mailAccountDiscoveryErr == "" || !m.canSwitchMailAccounts() {
		t.Fatalf("discovery failure did not expose retry: %q", m.mailAccountDiscoveryErr)
	}
	fail.Store(false)
	updated, retry := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	m = updated.(model)
	if retry == nil {
		t.Fatal("ctrl+a did not retry account discovery")
	}
	loaded := retry().(mailAccountsLoadedMsg)
	updated, _ = m.Update(loaded)
	m = updated.(model)
	if m.mailAccountDiscoveryErr != "" || len(m.mailAccounts) != 3 {
		t.Fatalf("retry did not load accounts: error=%q accounts=%#v", m.mailAccountDiscoveryErr, m.mailAccounts)
	}
}

func TestAccountPickerRequiresMultipleLinkedAccounts(t *testing.T) {
	m := newModel()
	m.mailAccounts = []mailAccountChoice{{label: "All Accounts"}, {id: 1, label: "jane@example.com"}}
	if m.canSwitchMailAccounts() {
		t.Fatal("picker enabled for one linked account")
	}
	m.mailAccounts = append(m.mailAccounts, mailAccountChoice{id: 2, label: "jane@company.example"})
	if !m.canSwitchMailAccounts() {
		t.Fatal("picker disabled for multiple linked accounts")
	}
}

func TestCtrlAOpensAccountPicker(t *testing.T) {
	m := newModel()
	m.loading = false
	m.mailAccounts = []mailAccountChoice{
		{label: "All Accounts"},
		{id: 1, label: "jane@example.com"},
		{id: 2, label: "jane@company.example"},
	}
	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	m = updated.(model)
	if cmd != nil || !m.mailAccountPicker {
		t.Fatal("ctrl+a did not open the account picker")
	}
	if view := m.View().Content; !strings.Contains(view, "Select mail account") {
		t.Fatalf("picker view = %q", view)
	}
}

func TestAccountPickerWaitsForPendingMutation(t *testing.T) {
	m := newModel()
	m.loading = false
	m.mailAccounts = []mailAccountChoice{
		{label: "All Accounts"},
		{id: 1, label: "jane@example.com"},
		{id: 2, label: "jane@company.example"},
	}
	m.mailView.pendingMutations = 1

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	m = updated.(model)
	if cmd != nil || m.mailAccountPicker {
		t.Fatal("ctrl+a opened the account picker during a pending mutation")
	}
	updated, cmd = m.switchSection(sectionCalendar)
	m = updated.(model)
	if cmd != nil || m.section != sectionMail {
		t.Fatal("section changed before the pending mutation could report its result")
	}

	m.mailView.pendingMutations = 0
	updated, cmd = m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	m = updated.(model)
	if cmd != nil || !m.mailAccountPicker {
		t.Fatal("ctrl+a did not open the account picker after the mutation finished")
	}
}

func TestAccountSwitchRebuildsViewsAndCancelsOldGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity.json":
			_, _ = fmt.Fprint(w, `{"id":1,"accounts":[{"id":1,"status":"active"},{"id":2,"status":"active"}]}`)
		case "/boxes.json":
			_, _ = fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	root := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "token"}, hey.WithMaxRetries(0))
	m := newModelWithMailAccounts(root, root, "all")
	m.mailAccounts = []mailAccountChoice{
		{label: "All Accounts"},
		{id: 1, label: "jane@example.com"},
		{id: 2, label: "jane@company.example"},
	}
	m.mailAccountPicker = true
	m.mailAccountCursor = 2
	oldContext := m.vc.ctx
	oldMailView := m.mailView

	modelAfterKey, cmd := m.handleMailAccountKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = modelAfterKey.(model)
	if cmd == nil || !m.mailAccountSwitching {
		t.Fatal("account switch did not start")
	}
	switchMsg, ok := cmd().(mailAccountSwitchedMsg)
	if !ok || switchMsg.err != nil {
		t.Fatalf("switch result = %#v", switchMsg)
	}
	updatedModel, initCmd := m.Update(switchMsg)
	m = updatedModel.(model)
	if initCmd == nil {
		t.Fatal("switched account did not reload the active view")
	}
	if m.mailAccount.id != 2 || m.mailAccount.label != "jane@company.example" {
		t.Fatalf("selected account = %#v", m.mailAccount)
	}
	if m.viewGeneration != 1 || m.mailView == oldMailView || m.mailAccountPicker {
		t.Fatalf("switch did not rebuild state: generation=%d picker=%v", m.viewGeneration, m.mailAccountPicker)
	}
	select {
	case <-oldContext.Done():
	default:
		t.Fatal("old view context was not canceled")
	}
	if accountID, ok := m.vc.sdk.AccountID(); !ok || accountID != 2 {
		t.Fatalf("view SDK account = %d, %v", accountID, ok)
	}
}

func TestFailedAccountSwitchPreservesCurrentViews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":1,"accounts":[{"id":1,"status":"active"}]}`)
	}))
	t.Cleanup(server.Close)
	root := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "token"}, hey.WithMaxRetries(0))
	m := newModelWithMailAccounts(root, root, "all")
	m.mailAccounts = []mailAccountChoice{{label: "All Accounts"}, {id: 1, label: "jane@example.com"}, {id: 2, label: "missing@example.com"}}
	m.mailAccountPicker = true
	m.mailAccountCursor = 2
	oldContext := m.vc.ctx
	oldMailView := m.mailView

	updated, cmd := m.handleMailAccountKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(model)
	switchMsg := cmd().(mailAccountSwitchedMsg)
	updated, _ = m.Update(switchMsg)
	m = updated.(model)
	if m.mailView != oldMailView || !m.mailAccountPicker || m.mailAccountErr == "" {
		t.Fatalf("failed switch changed views: picker=%v error=%q", m.mailAccountPicker, m.mailAccountErr)
	}
	select {
	case <-oldContext.Done():
		t.Fatal("failed switch canceled the current view context")
	default:
	}
}

func TestGenerationGuardPreservesRawTerminalMessages(t *testing.T) {
	m := newModel()
	cmd := m.stampViewCmd(tea.Raw("image upload"))
	if _, ok := cmd().(tea.RawMsg); !ok {
		t.Fatal("generation guard hid tea.RawMsg from Bubble Tea")
	}

	stale := m.stampViewCmd(tea.Raw("stale image upload"))
	m.viewGeneration++
	m.viewGenerationToken.Store(m.viewGeneration)
	if msg := stale(); msg != nil {
		t.Fatalf("stale raw command returned %#v", msg)
	}
}

func TestStaleViewGenerationIsIgnoredAfterAccountSwitch(t *testing.T) {
	m := newModel()
	m.viewGeneration = 2
	updated, cmd := m.Update(viewGenerationMsg{
		generation: 1,
		msg:        boxesLoadedMsg{{ID: 99, Name: "Stale"}},
	})
	m = updated.(model)
	if cmd != nil || len(m.mailView.boxes) != 0 {
		t.Fatalf("stale message changed the active view: %#v", m.mailView.boxes)
	}
}

func TestSwitchMailAccountCanReturnToAllAccounts(t *testing.T) {
	root := hey.NewClient(&hey.Config{BaseURL: "http://localhost:3000"}, &hey.StaticTokenProvider{Token: "token"})
	msg, ok := switchMailAccount(context.Background(), root, mailAccountChoice{label: "All Accounts"}, 7)().(mailAccountSwitchedMsg)
	if !ok || msg.err != nil || msg.client != root || msg.requestID != 7 {
		t.Fatalf("All Accounts switch = %#v", msg)
	}
}
