package folders

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func TestList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/my/navigation.json" {
			t.Errorf("request = %s %s, want GET /my/navigation.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"title":"Etiquetas","icon":{"name":"folders"},"menu_items":[{"title":"All Labels","app_url":"/folders"},{"title":"Receipts","app_url":"https://app.hey.com/folders/12"},{"title":"Travel","app_url":"/folders/34?from=navigation"}]},{"title":"Collections","menu_items":[{"title":"Planning","app_url":"/collections/56"}]}]}`)
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	folders, err := List(context.Background(), client)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("folders = %+v, want two", folders)
	}
	if folders[0].Id != 12 || folders[0].Name != "Receipts" || folders[0].AppUrl != "https://app.hey.com/folders/12" {
		t.Errorf("first folder = %+v", folders[0])
	}
	if folders[1].Id != 34 || folders[1].Name != "Travel" {
		t.Errorf("second folder = %+v", folders[1])
	}
}

func TestFromNavigationHandlesEmptyAndMalformedItems(t *testing.T) {
	if folders := FromNavigation(nil); folders != nil {
		t.Errorf("nil navigation returned %+v", folders)
	}

	navigation := &generated.NavigationResponse{Items: []generated.NavigationItem{{
		Title: navigationTitle,
		MenuItems: []generated.NavigationItem{
			{Title: "All Labels", AppUrl: "/folders"},
			{Title: "Missing ID", AppUrl: "/folders/not-an-id"},
		},
	}}}
	if folders := FromNavigation(navigation); len(folders) != 0 {
		t.Errorf("malformed navigation returned %+v", folders)
	}
}

func TestListReportsNavigationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "navigation unavailable", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	if _, err := List(context.Background(), client); err == nil {
		t.Fatal("expected navigation failure")
	}
}
