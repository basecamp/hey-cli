package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

type timeTrackRequest struct {
	method string
	path   string
	title  string
	query  string
}

// timeTrackServer stands in for HEY's time tracking: what is running, the categories, the
// pages of completed tracks, and every request made of it.
type timeTrackServer struct {
	mutex    sync.Mutex
	requests []timeTrackRequest
	// running is the track GetOngoing answers with, and nothing when nobody is tracking —
	// which HEY says with a 404.
	running     *generated.Recording
	startStatus int
	// pages are the tracked time index's pages, newest first. Every page but the last carries
	// a Link header naming the next, as geared_pagination does.
	pages [][]trackedTimeFixture
	// writes are the bodies the edits sent, so a test can say what a save carried.
	writes []string
}

// trackedTimeFixture is one completed track as the index serves it: a category as a plain
// title, and timestamps to the second.
type trackedTimeFixture struct {
	id       int64
	startsAt string
	endsAt   string
	category string
	notes    string
}

func (f trackedTimeFixture) json() string {
	return fmt.Sprintf(`{"id":%d,"type":"Calendar::TimeTrack","starts_at":%q,"ends_at":%q,"completed_at":%q,"category":%q,"notes":%q}`,
		f.id, f.startsAt, f.endsAt, f.endsAt, f.category, f.notes)
}

func (s *timeTrackServer) record(request timeTrackRequest) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.requests = append(s.requests, request)
}

func (s *timeTrackServer) recorded() []timeTrackRequest {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]timeTrackRequest(nil), s.requests...)
}

func (s *timeTrackServer) setRunning(track *generated.Recording) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.running = track
}

func (s *timeTrackServer) ongoing() *generated.Recording {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.running
}

// trackedTimePageJSON is one page as the index serves it: the tracks, and the calendar's whole
// category list alongside them.
func (s *timeTrackServer) trackedTimePageJSON(index int) string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var tracks []string
	if index >= 0 && index < len(s.pages) {
		for _, fixture := range s.pages[index] {
			tracks = append(tracks, fixture.json())
		}
	}
	return fmt.Sprintf(`{"time_tracks":[%s],"categories":[{"id":31,"title":"Client work"},{"id":32,"title":"Planning"}]}`,
		strings.Join(tracks, ","))
}

func (s *timeTrackServer) hasPageAfter(index int) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return index+1 < len(s.pages)
}

func (s *timeTrackServer) recordWrite(body string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.writes = append(s.writes, body)
}

func (s *timeTrackServer) written() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.writes...)
}

// isTimeTrackPath is one track's own route, which the categories live under a name of.
func isTimeTrackPath(path string) bool {
	return strings.HasPrefix(path, "/calendar/time_tracks/") && !strings.Contains(path, "categories")
}

func (s *timeTrackServer) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	request := timeTrackRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
	if r.Method != http.MethodGet && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		request.title = r.Form.Get("category[title]")
	}
	if r.Method == http.MethodPut {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		s.recordWrite(string(body))
	}
	s.record(request)

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/calendar/ongoing_time_track.json":
		track := s.ongoing()
		if track == nil {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"id":%d,"type":"Calendar::TimeTrack","starts_at":%q,"category":%q}`,
			track.Id, track.StartsAt.Format(time.RFC3339), track.Category)
	case r.Method == http.MethodPost && r.URL.Path == "/calendar/ongoing_time_track.json":
		if s.startStatus != 0 {
			w.WriteHeader(s.startStatus)
			_, _ = w.Write([]byte(`{"error":"Ongoing time track already in progress"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":91,"type":"Calendar::TimeTrack"}`))
	case r.Method == http.MethodGet && r.URL.Path == "/calendar/time_tracks.json":
		index := 0
		if page := r.URL.Query().Get("page"); page != "" {
			parsed, err := strconv.Atoi(page)
			if err != nil {
				t.Errorf("page cursor = %q", page)
			}
			index = parsed
		}
		if s.hasPageAfter(index) {
			w.Header().Set("Link", fmt.Sprintf("<%s?page=%d>; rel=\"next\"", r.URL.Path, index+1))
		}
		_, _ = w.Write([]byte(s.trackedTimePageJSON(index)))
	case r.Method == http.MethodPut && isTimeTrackPath(r.URL.Path):
		_, _ = w.Write([]byte(`{"id":91,"type":"Calendar::TimeTrack"}`))
	case r.Method == http.MethodDelete && isTimeTrackPath(r.URL.Path):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/calendar/time_tracks/categories.json":
		_, _ = w.Write([]byte(`[{"id":31,"title":"Client work"},{"id":32,"title":"Planning"}]`))
	case r.Method == http.MethodPost && r.URL.Path == "/calendar/time_tracks/categories":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPatch && r.URL.Path == "/calendar/time_tracks/categories/31":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && r.URL.Path == "/calendar/time_tracks/categories/31":
		w.WriteHeader(http.StatusNoContent)
	default:
		t.Logf("unhandled %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func timeTrackView(t *testing.T) (*calendarView, *timeTrackServer) {
	t.Helper()
	// One page of completed tracks, newest ended first, as the index serves them. The running
	// one is not among them — that is what the menu's stopwatch is for.
	tracked := &timeTrackServer{pages: [][]trackedTimeFixture{{
		{id: 501, startsAt: "2026-08-21T13:00:00Z", endsAt: "2026-08-21T14:15:00Z"},
		{id: 502, startsAt: "2026-08-20T09:00:00Z", endsAt: "2026-08-20T10:30:00Z", category: "Client work", notes: "Invoicing"},
		{id: 503, startsAt: "2024-11-24T12:25:00Z", endsAt: "2024-11-24T16:10:07Z", category: "Family", notes: "Birthday dinner"},
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracked.handle(t, w, r)
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	view := newCalendarView(vc)
	view.calendars = []Calendar{{ID: 7, Personal: true}}
	view.now = func() time.Time { return time.Date(2026, 8, 22, 16, 30, 0, 0, time.UTC) }
	view.Resize(80, 30)
	return view, tracked
}

// deliverTimeTrack runs a command and gives the view every message it answers with, down
// through the batches a write comes back in and on into whatever the view asks for next —
// a start is only finished once the track it started has been read back.
//
// The clock is marked as already ticking first: followClock would otherwise hand back a
// tick, and running one sleeps for a second.
func deliverTimeTrack(t *testing.T, view *calendarView, cmd tea.Cmd) {
	t.Helper()
	view.tickingClock = true

	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		cmd, pending = pending[0], pending[1:]
		if cmd == nil {
			continue
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			pending = append(pending, msg...)
		case notifyMsg:
		default:
			next, _ := view.Update(msg)
			pending = append(pending, next)
		}
	}
}

// openTimeTrackMenu is l and the read behind it, which is where every test here starts.
func openTimeTrackMenu(t *testing.T, view *calendarView) {
	t.Helper()
	cmd := view.HandleContentKey(keyPress("l"))
	if view.timeTrack == nil || !view.CapturingInput() {
		t.Fatal("l should open the time tracking menu")
	}
	deliverTimeTrack(t, view, cmd)
	if !view.ongoingKnown {
		t.Fatal("opening the menu should read what is being tracked")
	}
}

func TestCalendarTimeTrackMenuSaysWhatIsTracked(t *testing.T) {
	view, server := timeTrackView(t)
	openTimeTrackMenu(t, view)

	// Reading what is running never puts the spinner over the calendar: nobody asked for it.
	if view.Loading() || view.requests.loading {
		t.Error("the ongoing read claimed the reader's request lane")
	}
	menu := plainText(view.View())
	if !strings.Contains(menu, "Nothing is being tracked") || !strings.Contains(menu, "Start tracking") {
		t.Errorf("idle menu = %q", menu)
	}
	if _, running := view.OngoingTimeTrack(); running {
		t.Error("nothing should be running")
	}

	server.setRunning(&generated.Recording{Id: 91, StartsAt: time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC), Category: "Client work"})
	deliverTimeTrack(t, view, view.requestOngoingTrack())

	track, running := view.OngoingTimeTrack()
	if !running || track.ID != 91 || track.Category != "Client work" {
		t.Fatalf("ongoing track = %+v running:%v", track, running)
	}
	if elapsed := track.Elapsed(view.now()); elapsed != 90*time.Minute {
		t.Errorf("elapsed = %v", elapsed)
	}
	menu = plainText(view.View())
	if !strings.Contains(menu, "Tracking Client work") || !strings.Contains(menu, "1:30:00") {
		t.Errorf("running menu = %q", menu)
	}
	if !strings.Contains(menu, "Stop tracking") {
		t.Errorf("a running track should be stoppable from the menu: %q", menu)
	}

	view.HandleContentKey(keyPress("esc"))
	if view.timeTrack != nil || view.CapturingInput() {
		t.Error("esc should close the menu")
	}
}

// A running track keeps the clock ticking wherever the reader is, since its elapsed time is
// on screen and moving. Nothing else on the week does.
func TestCalendarClockFollowsARunningTrack(t *testing.T) {
	view, _ := timeTrackView(t)
	view.viewMode = viewWeek
	if view.followClock() != nil {
		t.Fatal("the week has nothing that moves on its own")
	}

	view.ongoing = &OngoingTrack{ID: 91, StartedAt: view.now().Add(-time.Minute)}
	if view.followClock() == nil {
		t.Error("a running track should keep the clock ticking")
	}
}

func TestCalendarTimeTrackStartAndStop(t *testing.T) {
	view, server := timeTrackView(t)
	openTimeTrackMenu(t, view)

	start := view.HandleContentKey(keyPress("enter"))
	if start == nil || !view.requests.loading {
		t.Fatal("enter on the first row should start tracking")
	}
	server.setRunning(&generated.Recording{Id: 91, StartsAt: time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)})
	deliverTimeTrack(t, view, start)

	if _, running := view.OngoingTimeTrack(); !running {
		t.Fatal("a started track should be read back")
	}
	if view.timeTrack.status != "Time tracking started" {
		t.Errorf("menu status = %q", view.timeTrack.status)
	}

	stop := view.HandleContentKey(keyPress("enter"))
	if stop == nil {
		t.Fatal("enter should stop the running track")
	}
	server.setRunning(nil)
	deliverTimeTrack(t, view, stop)
	if _, running := view.OngoingTimeTrack(); running {
		t.Error("a stopped track should be gone")
	}

	want := []timeTrackRequest{
		{method: http.MethodGet, path: "/calendar/ongoing_time_track.json"},
		{method: http.MethodPost, path: "/calendar/ongoing_time_track.json"},
		{method: http.MethodGet, path: "/calendar/ongoing_time_track.json"},
		{method: http.MethodPut, path: "/calendar/time_tracks/91.json"},
		{method: http.MethodGet, path: "/calendar/ongoing_time_track.json"},
	}
	assertTimeTrackRequests(t, server, want)
}

func TestCalendarTimeTrackStartConflictSaysWhatHappened(t *testing.T) {
	view, server := timeTrackView(t)
	server.startStatus = http.StatusConflict
	openTimeTrackMenu(t, view)

	deliverTimeTrack(t, view, view.HandleContentKey(keyPress("enter")))
	if view.timeTrack.status != "A time track is already running" || !view.timeTrack.isError {
		t.Errorf("conflict status = %q error:%v", view.timeTrack.status, view.timeTrack.isError)
	}
}

func TestCalendarTrackedTimeScreen(t *testing.T) {
	view, server := timeTrackView(t)
	openTrackedTime(t, view)

	screen := plainText(view.View())
	// The durations are computed from the two instants, which carry seconds — so the track
	// that ran 3:45:07 says so rather than being rounded down to the minute the export used.
	for _, want := range []string{"Client work", "1:30:00", "1:15:00", "Family", "3:45:07", "Invoicing"} {
		if !strings.Contains(screen, want) {
			t.Errorf("tracked time is missing %q: %q", want, screen)
		}
	}
	if day := view.trackedTime.tracks[2].StartsAt.Format("Jan 2 2006"); !strings.Contains(screen, day) {
		t.Errorf("tracked time is missing the day %q: %q", day, screen)
	}
	// One page, and nothing after it, so the screen can say this is everything.
	if !strings.Contains(screen, "Everything tracked · 3 tracks") {
		t.Errorf("tracked time summary = %q", screen)
	}
	// A track filed under nothing says so in the screen's own voice rather than repeating
	// HEY's word back as though it were somebody's category.
	if !strings.Contains(screen, "Uncategorized") {
		t.Errorf("a track filed under nothing should say so: %q", screen)
	}

	view.HandleContentKey(keyPress("esc"))
	if view.trackedTime != nil {
		t.Fatal("esc should leave the tracked time screen")
	}
	if view.timeTrack == nil {
		t.Error("leaving tracked time should come back to the menu it was opened from")
	}

	reads := 0
	for _, request := range server.recorded() {
		if request.path == "/calendar/time_tracks.json" {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("index reads = %d, want the one page that is all of it", reads)
	}
}

// A first page too short to fill the window keeps reading until it does, and an empty page ends
// the list whatever cursor came with it.
func TestTrackedTimeGrowsUntilTheWindowIsFull(t *testing.T) {
	view, server := timeTrackView(t)
	server.pages = [][]trackedTimeFixture{
		trackedTimeFixtures(1, 4),
		trackedTimeFixtures(5, 4),
		{},
	}
	openTrackedTime(t, view)

	if got := len(view.trackedTime.tracks); got != 8 {
		t.Errorf("tracks on screen = %d, want both pages read into a window with room for more", got)
	}
	if !view.trackedTime.complete || view.trackedTimeNextPage != "" {
		t.Errorf("an empty page should end the list: complete=%v cursor=%q",
			view.trackedTime.complete, view.trackedTimeNextPage)
	}
	if got := plainText(view.View()); !strings.Contains(got, "Everything tracked · 8 tracks") {
		t.Errorf("summary = %q", got)
	}

	cursors := []string{}
	for _, request := range server.recorded() {
		if request.path == "/calendar/time_tracks.json" {
			cursors = append(cursors, request.query)
		}
	}
	if len(cursors) != 3 || cursors[0] != "" || cursors[1] != "page=1" || cursors[2] != "page=2" {
		t.Errorf("index reads = %#v", cursors)
	}
}

// A list with more below it says so rather than claiming to be everything, and growing it never
// touches the request lane the reader is waiting on.
func TestTrackedTimeGrowsInItsOwnLane(t *testing.T) {
	view, server := timeTrackView(t)
	server.pages = [][]trackedTimeFixture{
		trackedTimeFixtures(1, 60),
		trackedTimeFixtures(61, 60),
	}
	openTrackedTime(t, view)

	if view.trackedTime.complete {
		t.Fatal("a page with another behind it is not everything")
	}
	if got := plainText(view.View()); !strings.Contains(got, "60 tracks so far · more below") {
		t.Errorf("summary = %q", got)
	}

	// The cursor coming within loadMoreThreshold of the bottom reads the page below, in a lane
	// of its own: no spinner, and the reader's own read is neither cancelled nor cancelling.
	view.trackedTime.cursor = len(view.trackedTime.tracks) - loadMoreThreshold
	more := view.loadMoreTrackedTime()
	if more == nil {
		t.Fatal("the cursor near the bottom should read the page below")
	}
	if view.requests.loading || view.Loading() {
		t.Error("growing the list claimed the reader's request lane")
	}

	waited := view.requestOngoingTrack()
	msg := more()
	if _, consumed := view.Update(msg); !consumed {
		t.Fatal("the page below should be consumed")
	}
	if got := len(view.trackedTime.tracks); got != 120 {
		t.Errorf("tracks after growing = %d", got)
	}
	deliverTimeTrack(t, view, waited)
	if !view.ongoingKnown {
		t.Error("the read the reader was waiting on was cancelled by the page below")
	}
}

// A page that does not arrive is said out loud: a list that quietly stops growing looks like a
// list that ended.
func TestTrackedTimeSaysWhatItCouldNotRead(t *testing.T) {
	view, _ := timeTrackView(t)
	openTrackedTime(t, view)

	view.trackedTimeMoreID++
	view.Update(trackedTimeAppendedMsg{requestID: view.trackedTimeMoreID, err: fmt.Errorf("nope")})
	if got := plainText(view.View()); !strings.Contains(got, "could not read further back") {
		t.Errorf("a failed page should be on screen: %q", got)
	}
}

func TestTrackedTimeCursorMovesAndTheScrollFollows(t *testing.T) {
	view, server := timeTrackView(t)
	server.pages = [][]trackedTimeFixture{trackedTimeFixtures(1, 40)}
	openTrackedTime(t, view)

	screen := view.trackedTime
	if screen.cursor != 0 || screen.contentVP.YOffset() != 0 {
		t.Fatalf("the list opens on its newest track: cursor=%d offset=%d", screen.cursor, screen.contentVP.YOffset())
	}
	if !strings.Contains(plainText(view.View()), "› ") {
		t.Error("the highlighted row is not marked")
	}

	for range 30 {
		view.HandleContentKey(keyPress("down"))
	}
	if screen.cursor != 30 {
		t.Errorf("cursor = %d, want the row the arrows walked to", screen.cursor)
	}
	if offset := screen.contentVP.YOffset(); offset == 0 || screen.cursor < offset || screen.cursor >= offset+screen.contentVP.Height() {
		t.Errorf("the scroll did not follow the cursor: cursor=%d offset=%d height=%d",
			screen.cursor, offset, screen.contentVP.Height())
	}

	for range 40 {
		view.HandleContentKey(keyPress("up"))
	}
	if screen.cursor != 0 || screen.contentVP.YOffset() != 0 {
		t.Errorf("the arrows should stop at the top: cursor=%d offset=%d", screen.cursor, screen.contentVP.YOffset())
	}
}

func TestTrackedTimeEditFormSendsOnlyWhatChanged(t *testing.T) {
	view, server := timeTrackView(t)
	openTrackedTime(t, view)

	view.HandleContentKey(keyPress("down"))
	view.HandleContentKey(keyPress("e"))
	form := view.trackedTimeForm
	if form == nil {
		t.Fatal("e should open the edit form on the highlighted row")
	}

	track := view.trackedTime.tracks[1]
	if form.trackID != track.ID {
		t.Errorf("the form is editing %d, want %d", form.trackID, track.ID)
	}
	if got := form.starts.date(); got != track.StartsAt.Format("2006-01-02") {
		t.Errorf("starts prefill = %q", got)
	}
	if got := form.starts.clock(); got != track.StartsAt.Format("15:04") {
		t.Errorf("starts clock prefill = %q", got)
	}
	if form.category.Value() != "Client work" || form.notes.Value() != "Invoicing" {
		t.Errorf("prefill = category:%q notes:%q", form.category.Value(), form.notes.Value())
	}
	// The categories arrived with the page, so the field offers them without a read of its own.
	if len(form.offered) != 2 || form.offered[1] != "Planning" {
		t.Errorf("offered categories = %#v", form.offered)
	}

	// Nothing touched is nothing to send: a save that says nothing is still a request.
	if _, submit := form.handleKey(keyPress("ctrl+s")); submit {
		t.Error("an untouched form should not save")
	}
	if form.status != "Nothing changed" || form.isError {
		t.Errorf("untouched status = %q error:%v", form.status, form.isError)
	}

	form.focus = trackFieldCategory
	form.chooseOffered(keyPress("down"))
	if form.category.Value() != "Planning" {
		t.Errorf("the arrows should offer the page's categories: %q", form.category.Value())
	}
	form.notes.SetValue("Invoicing and the quarterly report")

	save := view.handleTrackedTimeKey(keyPress("ctrl+s"))
	if save == nil {
		t.Fatal("ctrl+s should save")
	}
	deliverTimeTrack(t, view, save)
	if view.trackedTimeForm != nil {
		t.Error("a saved form should close")
	}

	written := server.written()
	if len(written) != 1 {
		t.Fatalf("writes = %#v", written)
	}
	var body struct {
		Track struct {
			StartsAt      *string `json:"starts_at"`
			EndsAt        *string `json:"ends_at"`
			Notes         string  `json:"notes"`
			CategoryTitle string  `json:"category_title"`
		} `json:"calendar_time_track"`
	}
	if err := json.Unmarshal([]byte(written[0]), &body); err != nil {
		t.Fatalf("unmarshal write: %v", err)
	}
	if body.Track.StartsAt != nil || body.Track.EndsAt != nil {
		t.Errorf("times nobody moved were sent: %s", written[0])
	}
	if body.Track.CategoryTitle != "Planning" || body.Track.Notes != "Invoicing and the quarterly report" {
		t.Errorf("write = %s", written[0])
	}
}

// The category field cannot un-file a track, so a blank one leaves it alone — and the form says
// as much rather than offering something HEY will ignore.
func TestTrackedTimeFormWillNotUnfileATrack(t *testing.T) {
	form := newTimeTrackForm(
		trackedTime{ID: 502, Category: "Client work", StartsAt: time.Now(), EndsAt: time.Now().Add(time.Hour)},
		[]generated.TimeTrackCategory{{Id: 31, Title: "Client work"}})

	form.category.SetValue("")
	if _, changed := form.payload(); changed {
		t.Error("a blank category should send nothing at all")
	}
	body := plainText(form.view())
	if !strings.Contains(body, "stays where it is") || !strings.Contains(body, "becomes one") {
		t.Errorf("the form does not say what the category field does: %q", body)
	}
}

// Emptying the notes cannot be sent, so the form refuses rather than saving something that
// looks like it worked.
func TestTrackedTimeFormRefusesToEmptyNotes(t *testing.T) {
	form := newTimeTrackForm(
		trackedTime{ID: 502, Notes: "Invoicing", StartsAt: time.Now(), EndsAt: time.Now().Add(time.Hour)},
		nil)

	form.notes.SetValue("")
	if _, submit := form.handleKey(keyPress("ctrl+s")); submit {
		t.Error("emptied notes should not be saved")
	}
	if !strings.Contains(form.status, "not emptied") || !form.isError {
		t.Errorf("status = %q error:%v", form.status, form.isError)
	}
}

func TestTrackedTimeDeleteAsksTwice(t *testing.T) {
	view, server := timeTrackView(t)
	openTrackedTime(t, view)

	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || !view.trackedTime.confirmingDelete {
		t.Fatal("the first x should ask rather than delete")
	}
	if got := plainText(view.View()); !strings.Contains(got, "Press x again to delete this track") {
		t.Errorf("the question is not on screen: %q", got)
	}
	// Any other key is the reader changing their mind.
	view.HandleContentKey(keyPress("down"))
	if view.trackedTime.confirmingDelete {
		t.Fatal("another key should take the question back")
	}

	view.HandleContentKey(keyPress("x"))
	remove := view.HandleContentKey(keyPress("x"))
	if remove == nil {
		t.Fatal("the second x should delete the track")
	}
	deliverTimeTrack(t, view, remove)

	deleted := false
	for _, request := range server.recorded() {
		deleted = deleted || (request.method == http.MethodDelete && strings.Contains(request.path, "/calendar/time_tracks/502"))
	}
	if !deleted {
		t.Errorf("the highlighted track was not deleted: %#v", server.recorded())
	}
}

// trackedTimeFixtures is a run of completed tracks, an hour each, a day apart.
func trackedTimeFixtures(from int64, count int) []trackedTimeFixture {
	day := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	fixtures := make([]trackedTimeFixture, 0, count)
	for i := range count {
		starts := day.AddDate(0, 0, -int(from)-i)
		fixtures = append(fixtures, trackedTimeFixture{
			id:       500 + from + int64(i),
			startsAt: starts.Format(time.RFC3339),
			endsAt:   starts.Add(time.Hour).Format(time.RFC3339),
		})
	}
	return fixtures
}

// openTrackedTime walks the menu to the tracked time and answers every read it opens with,
// which is where the tracked time tests start.
func openTrackedTime(t *testing.T, view *calendarView) {
	t.Helper()
	openTimeTrackMenu(t, view)
	view.HandleContentKey(keyPress("down"))
	deliverTimeTrack(t, view, view.HandleContentKey(keyPress("enter")))
	if view.trackedTime == nil {
		t.Fatal("the second row should open the tracked time screen")
	}
}

func TestCalendarTimeTrackCategoriesLoadAndClose(t *testing.T) {
	view, server := timeTrackView(t)
	openTimeTrackMenu(t, view)

	view.HandleContentKey(keyPress("down"))
	view.HandleContentKey(keyPress("down"))
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || view.timeTrackCategories == nil || !view.requests.loading {
		t.Fatal("the last row should open and load the categories")
	}
	// A modal is what the reader is looking at, so a read behind it does not put the
	// spinner over the calendar.
	if view.Loading() {
		t.Error("a read with the categories open claimed the spinner")
	}
	_, consumed := view.Update(cmd())
	if !consumed || view.requests.loading {
		t.Fatal("category response should finish loading")
	}
	if got := plainText(view.View()); !strings.Contains(got, "Client work") || !strings.Contains(got, "Planning") {
		t.Errorf("categories = %q", got)
	}

	view.HandleContentKey(keyPress("esc"))
	if view.timeTrackCategories != nil {
		t.Error("esc should close the categories")
	}
	if view.timeTrack == nil {
		t.Error("closing the categories should come back to the menu")
	}
	if got := (*server).recorded(); got[len(got)-1].path != "/calendar/time_tracks/categories.json" {
		t.Errorf("requests = %#v", got)
	}
}

func TestCalendarTimeTrackCategoriesBlockKeysWhileLoading(t *testing.T) {
	view, _ := timeTrackView(t)
	openTimeTrackMenu(t, view)
	load := openTimeTrackCategories(t, view)

	view.HandleContentKey(keyPress("a"))
	view.HandleContentKey(keyPress("esc"))
	if view.timeTrackCategories == nil || view.timeTrackCategories.mode != timeTrackCategoryBrowse {
		t.Fatal("the categories should ignore actions until their initial load finishes")
	}
	_, _ = view.Update(load())

	view.HandleContentKey(keyPress("a"))
	view.timeTrackCategories.input.SetValue("Research")
	create := view.HandleContentKey(keyPress("enter"))
	if create == nil || !view.requests.loading {
		t.Fatal("saving a category should enter a loading state")
	}
	view.HandleContentKey(keyPress("a"))
	view.HandleContentKey(keyPress("esc"))
	view.HandleContentKey(keyPress("x"))
	if view.timeTrackCategories == nil || view.timeTrackCategories.mode != timeTrackCategoryBrowse || view.timeTrackCategories.confirmingDelete {
		t.Fatal("the categories should ignore actions while a mutation is in flight")
	}
	finishTimeTrackCategoryMutation(t, view, create)
}

func TestCalendarTimeTrackCategoryMutations(t *testing.T) {
	view, server := timeTrackView(t)
	openTimeTrackMenu(t, view)
	_, _ = view.Update(openTimeTrackCategories(t, view)())

	view.HandleContentKey(keyPress("a"))
	if view.timeTrackCategories.mode != timeTrackCategoryCreate {
		t.Fatal("a should open category creation")
	}
	view.timeTrackCategories.input.SetValue("Research")
	create := view.HandleContentKey(keyPress("enter"))
	if create == nil {
		t.Fatal("enter should create the category")
	}
	finishTimeTrackCategoryMutation(t, view, create)

	view.HandleContentKey(keyPress("e"))
	if view.timeTrackCategories.mode != timeTrackCategoryRename {
		t.Fatal("e should open category rename")
	}
	if cmd := view.HandleContentKey(keyPress("enter")); cmd != nil || view.timeTrackCategories.mode != timeTrackCategoryBrowse {
		t.Fatal("an unedited rename should send nothing")
	}
	view.HandleContentKey(keyPress("e"))
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

	assertTimeTrackRequests(t, server, []timeTrackRequest{
		{method: http.MethodGet, path: "/calendar/ongoing_time_track.json"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
		{method: http.MethodPost, path: "/calendar/time_tracks/categories", title: "Research"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
		{method: http.MethodPatch, path: "/calendar/time_tracks/categories/31", title: "Customer work"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
		{method: http.MethodDelete, path: "/calendar/time_tracks/categories/31"},
		{method: http.MethodGet, path: "/calendar/time_tracks/categories.json"},
	})
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
	view := plainText(manager.draw("", 80, 20))
	if !strings.Contains(view, "uncategorized") {
		t.Errorf("delete confirmation = %q", view)
	}

	manager.confirmingDelete = false
	dangerous := "Saved \x1b]52;c;secret\a"
	manager.status = dangerous
	rendered := manager.draw("", 80, 20)
	if strings.Contains(rendered, dangerous) || strings.Contains(rendered, "\x1b]52") || strings.ContainsRune(rendered, '\a') {
		t.Errorf("category status retained terminal controls: %q", rendered)
	}

	manager.setCategories([]generated.TimeTrackCategory{{Id: 31, Title: dangerous}})
	if drawn := manager.draw("", 80, 20); strings.Contains(drawn, "\x1b]52") || strings.ContainsRune(drawn, '\a') {
		t.Errorf("category list retained terminal controls: %q", drawn)
	}
	manager.startRename()
	prefill := manager.input.Value()
	if strings.Contains(prefill, "\x1b]52") || strings.ContainsRune(prefill, '\a') {
		t.Errorf("rename input retained terminal controls: %q", prefill)
	}
}

// A category HEY serves is drawn through the sanitizer wherever it is shown, the running
// track's own included.
func TestOngoingTrackCategoryIsSanitized(t *testing.T) {
	track := ongoingTrackFrom(&generated.Recording{Id: 91, Category: "Client \x1b]52;c;secret\awork"})
	if strings.ContainsRune(track.Category, '\x1b') || strings.ContainsRune(track.Category, '\a') {
		t.Errorf("category = %q", track.Category)
	}
}

// Every stretch of time reads to the second, running or finished, and always with its hours —
// 0:45 alone is either three quarters of a minute or three quarters of an hour, and a format that
// grows on the hour would move the badge that has to fit beside the day's clock.
func TestTimeTrackDurationsRead(t *testing.T) {
	for _, want := range []struct {
		of   time.Duration
		want string
	}{
		{0, "0:00:00"},
		{45 * time.Second, "0:00:45"},
		{90 * time.Second, "0:01:30"},
		{time.Hour + 4*time.Minute + 9*time.Second, "1:04:09"},
		{95 * time.Minute, "1:35:00"},
		{25 * time.Hour, "25:00:00"},
		{-time.Minute, "0:00:00"},
	} {
		if got := formatElapsed(want.of); got != want.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", want.of, got, want.want)
		}
	}
}

// openTimeTrackCategories walks the menu to the categories and answers the read they open
// with, which every category test starts from.
func openTimeTrackCategories(t *testing.T, view *calendarView) tea.Cmd {
	t.Helper()
	view.HandleContentKey(keyPress("down"))
	view.HandleContentKey(keyPress("down"))
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || view.timeTrackCategories == nil {
		t.Fatal("the last row should open the categories")
	}
	return cmd
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

func assertTimeTrackRequests(t *testing.T, server *timeTrackServer, want []timeTrackRequest) {
	t.Helper()
	got := server.recorded()
	if len(got) != len(want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].method != want[i].method || got[i].path != want[i].path || got[i].title != want[i].title {
			t.Errorf("request %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}
