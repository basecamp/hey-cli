package smoke_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// This case changes the exclusively owned development identity's zone temporarily.
// Create through the web form: event add deliberately creates explicitly zoned events.
func TestEventZonelessOccurrenceTitleAcrossDST(t *testing.T) {
	ctx, cancel, allocCancel := newBrowserContext()
	t.Cleanup(allocCancel)
	t.Cleanup(cancel)
	// Allocate on the browser's lifetime context, not a single step's deadline.
	if err := chromedp.Run(ctx); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	run := func(actions ...chromedp.Action) {
		t.Helper()
		step, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		if err := chromedp.Run(step, actions...); err != nil {
			t.Fatal(err)
		}
	}
	// The emulated browser must not let HEY's automatic-zone beacon change the
	// identity before we inspect its preference and refuse automatic mode.
	run(network.Enable(),
		network.SetBlockedURLs([]string{baseURL + "/identity/time_zone?from_beacon=*"}),
		network.SetCookie("session_token", sessionCookie).WithDomain(parsed.Hostname()).WithPath("/").WithHTTPOnly(true),
		emulation.SetTimezoneOverride("America/Chicago"))

	type preference struct {
		Zone string `json:"zone"`
		Auto bool   `json:"auto"`
	}
	readPreference := func() preference {
		t.Helper()
		var value preference
		run(chromedp.Navigate(baseURL+"/identity/time_zone/edit"),
			chromedp.WaitReady(`#auto_time_zone_enabled`, chromedp.ByQuery),
			chromedp.Evaluate(`({zone: document.querySelector('[name="time_zone"]')?.value || '', auto: document.querySelector('#auto_time_zone_enabled').checked})`, &value))
		return value
	}
	original := readPreference()
	if original.Auto || original.Zone == "" {
		t.Fatal("requires an exclusively owned identity with automatic time zone disabled and a manual zone")
	}
	t.Logf("original preference: zone=%s auto=%t", original.Zone, original.Auto)
	setZone := func(zone string) {
		t.Helper()
		value, _ := json.Marshal(zone)
		run(chromedp.Navigate(baseURL+"/identity/time_zone/edit"),
			chromedp.WaitReady(`[name="time_zone"]`, chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(`(() => {
			 const select = document.querySelector('[name="time_zone"]');
			 select.value = %s;
			 if (!select.value) throw new Error('time zone option missing');
			 select.form.requestSubmit();
			})()`, value), nil),
			chromedp.Poll(`!location.pathname.endsWith('/time_zone/edit')`, nil))
	}
	// Register restoration before the first write, and read it back before browser
	// and OAuth client teardown even when the product assertion fails.
	t.Cleanup(func() {
		setZone(original.Zone)
		if got := readPreference(); got != original {
			t.Errorf("preference restoration = %+v, want %+v", got, original)
		} else {
			t.Logf("preference restored and read back: zone=%s auto=%t", got.Zone, got.Auto)
		}
	})
	setZone("Central Time (US & Canada)")
	if got := readPreference(); got.Zone != "Central Time (US & Canada)" || got.Auto {
		t.Fatalf("fixture preference = %+v", got)
	}

	const firstDay, selectedDay = "2026-10-21", "2026-11-04"
	title := "Weekly design planning " + uniqueID()
	// Register title-based discovery before submitting the browser form, so even
	// a failed redirect can recover the exact uniquely named series.
	var ids []string
	t.Cleanup(func() {
		data, err := smokeJSON(commandFor(t), "event", "list", "--starts-on", firstDay, "--ends-on", "2026-11-19", "--all")
		var rows []smokeEvent
		if err == nil {
			err = json.Unmarshal(data, &rows)
		}
		if err != nil {
			t.Errorf("discover owned series %q for cleanup: %v", title, err)
		}
		for _, row := range rows {
			if row.Title == title && row.ParentID == 0 {
				ids = append(ids, fmt.Sprint(row.ID))
			}
		}
		if len(ids) == 0 {
			if err == nil {
				t.Log("no owned series was created")
			}
			return
		}
		if err := cleanupEventsWith(commandFor(t), ids, title, []string{firstDay, selectedDay, "2026-11-18"}); err != nil {
			t.Error(err)
		} else {
			t.Log("owned series and realized days confirmed absent across all calendars")
		}
	})
	encodedTitle, _ := json.Marshal(title)
	run(chromedp.Navigate(baseURL + "/calendar/events/new?starts_at=2026-10-21T09:00:00&ends_at=2026-10-21T10:00:00"))
	run(chromedp.WaitReady(`[name="calendar_event[summary]"]`, chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf(`(() => {
		 const set = (name, value) => {
		   const input = document.querySelector('[name="'+name+'"]');
		   input.value = value;
		   input.dispatchEvent(new Event('input', {bubbles: true}));
		   input.dispatchEvent(new Event('change', {bubbles: true}));
		 };
		 set('calendar_event[summary]', %s);
		 const repeat = [...document.querySelectorAll('button')].find(b => b.textContent.trim() === 'Repeat');
		 repeat.click();
		 set('repeat_frequency', 'every_week');
		 set('calendar_recurrence_schedule[recurs_until_type]', 'count');
		 set('calendar_recurrence_schedule[recurs_count]', '5');
		 const zone = document.querySelector('[name="calendar_event[set_time_zone]"][type="checkbox"]');
		 if (zone.checked) zone.click();
		})()`, encodedTitle), nil))
	run(chromedp.Evaluate(`(() => {
		 document.addEventListener('turbo:submit-end', event => window.fixtureSubmitted = event.detail.success, {once: true});
		 const button = document.querySelector('input[type="submit"][value="Add this event"]');
		 button.form.requestSubmit(button);
		})()`, nil),
		chromedp.Poll(`window.fixtureSubmitted === true`, nil))

	type event struct {
		smokeEvent
		EndsAt    string `json:"ends_at"`
		StartZone string `json:"starts_at_time_zone"`
		EndZone   string `json:"ends_at_time_zone"`
		Recurring bool   `json:"recurring"`
	}
	rows := dataAs[[]event](t, heyJSON(t, "event", "list", "--starts-on", firstDay, "--ends-on", "2026-11-19", "--all"))
	var series event
	for _, row := range rows {
		if row.Title == title && row.ParentID == 0 {
			series = row
		}
	}
	if series.ID == 0 {
		t.Fatal("browser-created series not found")
	}
	ids = append(ids, fmt.Sprint(series.ID))
	t.Logf("owned series %d %q: start=%s end=%s zones=%q/%q recurring=%t", series.ID, title, series.StartsAt, series.EndsAt, series.StartZone, series.EndZone, series.Recurring)
	if !series.Recurring || series.StartZone != "" || series.EndZone != "" || series.StartsAt != "2026-10-21T14:00:00Z" || series.EndsAt != "2026-10-21T15:00:00Z" {
		t.Fatal("fixture is not the reported zoneless weekly 09:00–10:00 Chicago series")
	}

	browserTimes := func(wantTitle string) string {
		t.Helper()
		encoded, _ := json.Marshal(wantTitle)
		var times string
		run(chromedp.Navigate(baseURL+"/calendar/days/"+selectedDay),
			chromedp.WaitReady(`#day-periods`, chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(`(() => {
			 const titles = [...document.querySelectorAll('.event__title')].filter(e => e.textContent.trim() === %s);
			 if (titles.length !== 1) throw new Error('expected exactly one owned browser occurrence');
			 return titles[0].closest('a').querySelector('.event__label-time').textContent.replace(/\s+/g,' ').trim();
			})()`, encoded), &times))
		t.Logf("browser occurrence %q: %s", wantTitle, times)
		return times
	}
	before := browserTimes(title)
	if before != "9am - 10am" {
		t.Fatalf("browser fixture time = %q, want 9am - 10am", before)
	}
	occurrence := fmt.Sprintf("%d_%s", series.ID, selectedDay)
	virtuals := dataAs[[]smokeEvent](t, heyJSON(t, "event", "week", selectedDay))
	if row, ok := findSmokeOccurrence(virtuals, occurrence); !ok || row.RecordingID != 0 {
		t.Fatalf("expected virtual occurrence %s, got %+v", occurrence, row)
	}
	renamed := title + " (revised)"
	heyJSON(t, "event", "edit", fmt.Sprint(series.ID), "--occurrence", occurrence, "--apply-to", "current", "--title", renamed)
	stored := dataAs[[]event](t, heyJSON(t, "event", "list", "--starts-on", selectedDay, "--ends-on", "2026-11-05", "--all"))
	found := false
	for _, row := range stored {
		if row.OccurrenceID == occurrence {
			found = true
			t.Logf("stored occurrence %s id=%d parent=%d title=%q start=%s end=%s zones=%q/%q", occurrence, row.ID, row.ParentID, row.Title, row.StartsAt, row.EndsAt, row.StartZone, row.EndZone)
			if row.ID == 0 || row.ID == series.ID || row.ParentID != series.ID || row.Title != renamed || row.StartZone != "" || row.EndZone != "" || row.StartsAt != "2026-11-04T15:00:00Z" || row.EndsAt != "2026-11-04T16:00:00Z" {
				t.Error("title-only edit changed the occurrence's time or identity")
			}
		}
	}
	if !found {
		t.Error("all-calendar stored read did not find the realized occurrence")
	}
	if after := browserTimes(renamed); after != before {
		t.Errorf("title-only edit moved browser occurrence: before=%q after=%q", before, after)
	}
}
