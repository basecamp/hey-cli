package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/config"
)

func TestCommandAccountScopePolicy(t *testing.T) {
	root := newRootCmd()
	for _, test := range []struct {
		args []string
		want bool
	}{
		{args: []string{"boxes"}, want: true},
		{args: []string{"calendars"}, want: true},
		{args: []string{"tui"}, want: true},
		{args: []string{"accounts", "list"}, want: false},
		{args: []string{"auth", "status"}, want: false},
		{args: []string{"config", "show"}, want: false},
		{args: []string{"omarchy", "poll"}, want: false},
	} {
		command, _, err := root.Find(test.args)
		if err != nil {
			t.Fatalf("Find(%v): %v", test.args, err)
		}
		if got := commandUsesAccountScope(command); got != test.want {
			t.Errorf("commandUsesAccountScope(%v) = %v, want %v", test.args, got, test.want)
		}
	}
}

func TestUnavailableAccountNotice(t *testing.T) {
	accounts := []accountListItem{{ID: "all"}, {ID: "1"}}
	if unavailableAccountNotice(accounts, "all") != "" {
		t.Fatal("All Accounts unexpectedly reported unavailable")
	}
	if unavailableAccountNotice(accounts, "1") == "" {
		t.Fatal("unavailable selected account notice is empty")
	}
	accounts[1].Active = true
	if unavailableAccountNotice(accounts, "1") != "" {
		t.Fatal("available selected account unexpectedly reported unavailable")
	}
}

func TestAccountOverrideNotice(t *testing.T) {
	if accountOverrideNotice(config.SourceGlobal) != "" {
		t.Fatal("global default unexpectedly reported an override")
	}
	if accountOverrideNotice(config.SourceEnv) == "" {
		t.Fatal("environment override notice is empty")
	}
	if accountOverrideNotice(config.SourceLocal) == "" {
		t.Fatal("local override notice is empty")
	}
}

func TestLinkedAccountListIncludesAccessibleAccounts(t *testing.T) {
	identity := &generated.Identity{
		Accounts: []generated.Account{
			{Id: 1, Name: "Personal", Purpose: "home", Status: "active"},
			{Id: 2, Name: "Canceled", Purpose: "home", Status: "canceled"},
			{Id: 3, Name: "Work", Purpose: "work", Status: "inactive"},
			{Id: 4, Name: "Old home", Purpose: "home", Status: "inactive"},
		},
		AllUsers: []generated.User{
			{Id: 11, AccountId: 1, Contact: generated.Contact{Id: 101, EmailAddress: "jane@example.com"}},
			{Id: 33, AccountId: 3, Contact: generated.Contact{Id: 303, EmailAddress: "jane@company.example"}},
		},
	}

	accounts := linkedAccountList(identity, "3")
	if len(accounts) != 3 {
		t.Fatalf("accounts = %#v, want All Accounts and two accessible accounts", accounts)
	}
	if accounts[0].ID != config.AllAccounts || accounts[0].Active {
		t.Errorf("All Accounts row = %#v", accounts[0])
	}
	if accounts[1].ID != "1" || accounts[1].Email != "jane@example.com" || accounts[1].Active {
		t.Errorf("personal row = %#v", accounts[1])
	}
	if accounts[2].ID != "3" || accounts[2].Email != "jane@company.example" || !accounts[2].Active {
		t.Errorf("work row = %#v", accounts[2])
	}
}

func TestAccountFlagScopesMailRequests(t *testing.T) {
	var boxesAccount string
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boxes.json" {
			boxesAccount = r.URL.Query().Get("filtered_account_id")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	})

	_, err := runAccountsCLI(t, server, "--account", "2", "boxes")
	if err != nil {
		t.Fatal(err)
	}
	if boxesAccount != "2" {
		t.Fatalf("boxes account = %q, want 2", boxesAccount)
	}
}

func TestPersistedAccountScopesMailRequests(t *testing.T) {
	var boxesAccount string
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boxes.json" {
			boxesAccount = r.URL.Query().Get("filtered_account_id")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	})
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "hey-cli"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"account_defaults": map[string]string{server.URL: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "hey-cli", "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := runAccountsCLIWithConfig(t, server, configDir, "boxes"); err != nil {
		t.Fatal(err)
	}
	if boxesAccount != "2" {
		t.Fatalf("boxes account = %q, want 2", boxesAccount)
	}
}

func TestSelectedAccountUsesMatchingSenderAndUser(t *testing.T) {
	var messageAccount, contactAccount, bundleAccount string
	var actingSenderID, actingUserID int64
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/messages.json":
			messageAccount = r.URL.Query().Get("filtered_account_id")
			var body struct {
				ActingSenderID int64 `json:"acting_sender_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			actingSenderID = body.ActingSenderID
			w.WriteHeader(http.StatusCreated)
		case "/contacts.json":
			contactAccount = r.URL.Query().Get("filtered_account_id")
			var body struct {
				ActingUserID int64 `json:"acting_user_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			actingUserID = body.ActingUserID
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":77,"name":"Jane Doe","email_address":"jane@example.org"}`))
		case "/contacts/77/bundle.json":
			bundleAccount = r.URL.Query().Get("filtered_account_id")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	})

	if _, err := runAccountsCLI(t, server,
		"--account", "2", "compose",
		"--to", "jane@example.org", "--subject", "Quarterly planning", "--message", "Agenda attached.",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := runAccountsCLI(t, server,
		"--account", "2", "contacts", "add",
		"--name", "Jane Doe", "--email", "jane@example.org",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := runAccountsCLI(t, server,
		"--account", "2", "contacts", "bundle", "77",
	); err != nil {
		t.Fatal(err)
	}
	if messageAccount != "2" || actingSenderID != 222 {
		t.Fatalf("message account/sender = %q/%d, want 2/222", messageAccount, actingSenderID)
	}
	if contactAccount != "2" || actingUserID != 22 {
		t.Fatalf("contact account/user = %q/%d, want 2/22", contactAccount, actingUserID)
	}
	if bundleAccount != "2" {
		t.Fatalf("bundle account = %q, want 2", bundleAccount)
	}
}

func TestThreadAccountIsRequiredForAccountSensitiveWrites(t *testing.T) {
	_, err := clientForResourceAccount(t.Context(), 0)
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "api" {
		t.Fatalf("error = %v, want api error", err)
	}
}

func TestUnknownAccountFlagFailsClosed(t *testing.T) {
	var boxesRequests int
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boxes.json" {
			boxesRequests++
		}
		http.NotFound(w, r)
	})

	_, err := runAccountsCLI(t, server, "--account", "99", "boxes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("error = %v, want not_found", err)
	}
	if boxesRequests != 0 {
		t.Fatalf("boxes requests = %d, want 0", boxesRequests)
	}
}

func TestConfigSetDoesNotPersistAccountOverride(t *testing.T) {
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "hey-cli"), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "hey-cli", "config.json")
	if err := os.WriteFile(configPath, []byte(`{"base_url":"https://old.hey.com","account_id":"1"}`), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := runAccountsCLIWithConfig(t, server, configDir,
		"--account", "2", "config", "set", "base_url", "https://new.hey.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		BaseURL         string            `json:"base_url"`
		AccountID       string            `json:"account_id"`
		AccountDefaults map[string]string `json:"account_defaults"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.BaseURL != "https://new.hey.com" || file.AccountID != "" || file.AccountDefaults["https://old.hey.com"] != "1" {
		t.Fatalf("saved config = %#v", file)
	}
}

func TestAccountsUseValidatesAndPersistsDefault(t *testing.T) {
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	tmp := t.TempDir()

	_, err := runAccountsCLIWithConfig(t, server, tmp, "accounts", "use", "2")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "hey-cli", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		AccountDefaults map[string]string `json:"account_defaults"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.AccountDefaults[server.URL] != "2" {
		t.Fatalf("saved account defaults = %#v", file.AccountDefaults)
	}
}

func TestBaseURLFlagDoesNotCarryGlobalAccountAcrossOrigins(t *testing.T) {
	var boxesAccounts []string
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/boxes.json" {
			http.NotFound(w, r)
			return
		}
		boxesAccounts = append(boxesAccounts, r.URL.Query().Get("filtered_account_id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	configDir := t.TempDir()
	dir := filepath.Join(configDir, "hey-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	global := `{"base_url":"https://app.hey.com","account_defaults":{"https://app.hey.com":"1"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(global), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := runAccountsCLIWithConfig(t, server, configDir, "boxes"); err != nil {
		t.Fatal(err)
	}
	if _, err := runAccountsCLIWithConfig(t, server, configDir, "--account", "2", "boxes"); err != nil {
		t.Fatal(err)
	}
	if len(boxesAccounts) != 2 || boxesAccounts[0] != "" || boxesAccounts[1] != "2" {
		t.Fatalf("boxes account filters = %#v, want [all, 2]", boxesAccounts)
	}
}

func TestAccountsUseRejectsUnknownAccountWithoutSaving(t *testing.T) {
	server := linkedAccountServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	configDir := t.TempDir()

	_, err := runAccountsCLIWithConfig(t, server, configDir, "accounts", "use", "99")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("error = %v, want not_found", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "hey-cli", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("config was saved for unavailable account: %v", err)
	}
}

func linkedAccountServer(t *testing.T, fallback http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/identity.json" {
			if got := r.URL.Query().Get("filtered_account_id"); got != "" {
				t.Errorf("identity validation account = %q, want unscoped", got)
			}
			_, _ = w.Write([]byte(`{
				"id":1,
				"accounts":[
					{"id":1,"name":"Personal","purpose":"home","status":"active"},
					{"id":2,"name":"Work","purpose":"work","status":"active"}
				],
				"all_users":[
					{"id":11,"account_id":1,"contact":{"id":101,"email_address":"jane@example.com"}},
					{"id":22,"account_id":2,"contact":{"id":202,"email_address":"jane@company.example"}}
				],
				"senders":[
					{"id":111,"account_id":1,"default":true},
					{"id":222,"account_id":2}
				]
			}`))
			return
		}
		fallback(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func runAccountsCLI(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	return runAccountsCLIWithConfig(t, server, t.TempDir(), args...)
}

func runAccountsCLIWithConfig(t *testing.T, server *httptest.Server, configDir string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	previousAccountFlag := accountFlag
	accountFlag = ""
	t.Cleanup(func() { accountFlag = previousAccountFlag })

	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(append([]string{"--json", "--base-url", server.URL}, args...))
	err := root.Execute()
	return output.String(), err
}
