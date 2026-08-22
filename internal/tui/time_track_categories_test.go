package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

type timeTrackCategoryRequest struct {
	method string
	path   string
	title  string
}

func timeTrackCategoryView(t *testing.T) (*calendarView, *[]timeTrackCategoryRequest) {
	t.Helper()
	requests := &[]timeTrackCategoryRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := timeTrackCategoryRequest{method: r.Method, path: r.URL.Path}
		if r.Method != http.MethodGet {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse category form: %v", err)
			}
			request.title = r.Form.Get("category[title]")
		}
		*requests = append(*requests, request)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendar/time_tracks/categories.json":
			_, _ = w.Write([]byte(`[{"id":31,"title":"Client work"},{"id":32,"title":"Planning"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/calendar/time_tracks/categories":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPatch && r.URL.Path == "/calendar/time_tracks/categories/31":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/calendar/time_tracks/categories/31":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	view := newCalendarView(vc)
	view.Resize(80, 30)
	return view, requests
}

func finishTimeTrackCategoryMutation(t *testing.T, view *calendarView, cmd tea.Cmd) {
	t.Helper()
	msg := cmd()
	refresh, consumed := view.Update(msg)
	if !consumed || refresh == nil {
		t.Fatal("category mutation should be consumed and refresh categories")
	}
	_, consumed = view.Update(refresh())
	if !consumed {
		t.Fatal("category refresh should be consumed")
	}
}

func TestCalendarTimeTrackCategoriesLoadAndClose(t *testing.T) {
	view, requests := timeTrackCategoryView(t)
	cmd := view.HandleContentKey(keyPress("c"))
	if cmd == nil || !view.CapturingInput() || !view.requests.loading {
		t.Fatal("c should open and load the time track category manager")
	}
	// A modal is what the reader is looking at, so a read behind it does not put the
	// spinner over the calendar.
	if view.Loading() {
		t.Error("a read with the manager open claimed the spinner")
	}
	_, consumed := view.Update(cmd())
	if !consumed || view.requests.loading {
		t.Fatal("category response should finish loading")
	}
	if got := plainText(view.View()); !strings.Contains(got, "Client work") || !strings.Contains(got, "Planning") {
		t.Errorf("category manager view = %q", got)
	}
	if len(*requests) != 1 || (*requests)[0].path != "/calendar/time_tracks/categories.json" {
		t.Errorf("requests = %#v", *requests)
	}

	view.HandleContentKey(keyPress("q"))
	if view.CapturingInput() {
		t.Error("q should close the category manager")
	}
}

func TestCalendarTimeTrackCategoriesBlockKeysWhileLoading(t *testing.T) {
	view, _ := timeTrackCategoryView(t)
	load := view.HandleContentKey(keyPress("c"))
	view.HandleContentKey(keyPress("n"))
	view.HandleContentKey(keyPress("q"))
	if view.timeTrackCategories == nil || view.timeTrackCategories.mode != timeTrackCategoryBrowse {
		t.Fatal("the category manager should ignore actions until its initial load finishes")
	}
	_, _ = view.Update(load())

	view.HandleContentKey(keyPress("n"))
	view.timeTrackCategories.input.SetValue("Research")
	create := view.HandleContentKey(keyPress("enter"))
	if create == nil || !view.requests.loading {
		t.Fatal("saving a category should enter a loading state")
	}
	view.HandleContentKey(keyPress("n"))
	view.HandleContentKey(keyPress("q"))
	view.HandleContentKey(keyPress("x"))
	if view.timeTrackCategories == nil || view.timeTrackCategories.mode != timeTrackCategoryBrowse || view.timeTrackCategories.confirmingDelete {
		t.Fatal("the category manager should ignore actions while a mutation is in flight")
	}
	finishTimeTrackCategoryMutation(t, view, create)
}

func TestCalendarTimeTrackCategoryMutations(t *testing.T) {
	view, requests := timeTrackCategoryView(t)
	_, _ = view.Update(view.HandleContentKey(keyPress("c"))())

	view.HandleContentKey(keyPress("n"))
	if view.timeTrackCategories.mode != timeTrackCategoryCreate {
		t.Fatal("n should open category creation")
	}
	view.timeTrackCategories.input.SetValue("Research")
	create := view.HandleContentKey(keyPress("enter"))
	if create == nil {
		t.Fatal("enter should create the category")
	}
	finishTimeTrackCategoryMutation(t, view, create)

	view.HandleContentKey(keyPress("enter"))
	if view.timeTrackCategories.mode != timeTrackCategoryRename {
		t.Fatal("enter should open category rename")
	}
	if cmd := view.HandleContentKey(keyPress("enter")); cmd != nil || view.timeTrackCategories.mode != timeTrackCategoryBrowse {
		t.Fatal("an unedited rename should send nothing")
	}
	view.HandleContentKey(keyPress("enter"))
	view.timeTrackCategories.input.SetValue("Customer work")
	rename := view.HandleContentKey(keyPress("enter"))
	if rename == nil {
		t.Fatal("enter should rename the category")
	}
	finishTimeTrackCategoryMutation(t, view, rename)

	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || !view.timeTrackCategories.confirmingDelete {
		t.Fatal("first x should confirm category deletion")
	}
	remove := view.HandleContentKey(keyPress("x"))
	if remove == nil {
		t.Fatal("second x should delete the category")
	}
	finishTimeTrackCategoryMutation(t, view, remove)

	want := []timeTrackCategoryRequest{
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
		{method: http.MethodPost, path: "/calendar/time_tracks/categories", title: "Research"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
		{method: http.MethodPatch, path: "/calendar/time_tracks/categories/31", title: "Customer work"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
		{method: http.MethodDelete, path: "/calendar/time_tracks/categories/31"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
	}
	if len(*requests) != len(want) {
		t.Fatalf("requests = %#v, want %#v", *requests, want)
	}
	for i := range want {
		if (*requests)[i] != want[i] {
			t.Errorf("request %d = %#v, want %#v", i, (*requests)[i], want[i])
		}
	}
}

func TestTimeTrackCategoryManagerValidatesAndConfirms(t *testing.T) {
	manager := newTimeTrackCategoryManager()
	manager.startCreate()
	manager.input.SetValue("   ")
	if _, ok := manager.title(); ok || manager.status != "Enter a category title" {
		t.Errorf("empty category state = ok:%v status:%q", ok, manager.status)
	}

	manager.cancelEdit()
	manager.confirmingDelete = true
	view := plainText(manager.view(newStyles(), 80, 20))
	if !strings.Contains(view, "will become uncategorized") {
		t.Errorf("delete confirmation = %q", view)
	}

	manager.confirmingDelete = false
	dangerous := "Saved \x1b]52;c;secret\a"
	manager.status = dangerous
	rendered := manager.view(newStyles(), 80, 20)
	if strings.Contains(rendered, dangerous) || strings.Contains(rendered, "\x1b]52") || strings.ContainsRune(rendered, '\a') {
		t.Errorf("category status retained terminal controls: %q", rendered)
	}

	manager.setCategories([]generated.TimeTrackCategory{{Id: 31, Title: dangerous}})
	manager.startRename()
	prefill := manager.input.Value()
	if strings.Contains(prefill, "\x1b]52") || strings.ContainsRune(prefill, '\a') {
		t.Errorf("rename input retained terminal controls: %q", prefill)
	}
}
