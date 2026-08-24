package smoke_test

import "testing"

func TestAccountsList(t *testing.T) {
	resp := heyJSON(t, "account", "list")
	accounts := dataAs[[]struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}](t, resp)
	if len(accounts) == 0 {
		t.Fatal("accounts list returned no filters")
	}
	if accounts[0].ID != "all" || accounts[0].Name != "All Accounts" {
		t.Fatalf("first account = %#v, want All Accounts", accounts[0])
	}
}

func TestAccountsUseAll(t *testing.T) {
	resp := heyJSON(t, "account", "use", "all")
	data := dataAs[map[string]string](t, resp)
	if data["account_id"] != "all" {
		t.Fatalf("account_id = %q, want all", data["account_id"])
	}

	_ = heyJSON(t, "--account", "all", "box", "list")
}

func TestAccountsRejectUnknownAccount(t *testing.T) {
	heyFail(t, "--account", "9223372036854775807", "box", "list")
}
