package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"
)

func TestEventsEditZonelessOccurrenceUsesAccountWallClock(t *testing.T) {
	for _, tt := range []struct {
		name, zone, day, start, end, wantDay, wantStart, wantEnd string
		flags                                                    []string
		refused                                                  bool
	}{
		{name: "fall back", zone: "America/Chicago", day: "2026-11-04", start: "2026-10-21T14:00:00Z", end: "2026-10-21T15:00:00Z", wantDay: "2026-11-04", wantStart: "15:00:00", wantEnd: "16:00:00"},
		{name: "before fall back", zone: "America/Chicago", day: "2026-10-28", start: "2026-10-21T14:00:00Z", end: "2026-10-21T15:00:00Z", wantDay: "2026-10-28", wantStart: "14:00:00", wantEnd: "15:00:00"},
		{name: "spring forward", zone: "America/Chicago", day: "2026-03-11", start: "2026-03-04T15:00:00Z", end: "2026-03-04T16:00:00Z", wantDay: "2026-03-11", wantStart: "14:00:00", wantEnd: "15:00:00"},
		{name: "west of UTC date", zone: "America/Chicago", day: "2026-11-05", start: "2026-10-22T04:30:00Z", end: "2026-10-22T05:00:00Z", wantDay: "2026-11-05", wantStart: "05:30:00", wantEnd: "06:00:00"},
		{name: "east of UTC date", zone: "Europe/Berlin", day: "2026-11-03", start: "2026-10-20T22:30:00Z", end: "2026-10-20T23:00:00Z", wantDay: "2026-11-03", wantStart: "23:30:00", wantEnd: "00:00:00"},
		{name: "explicit destination zone still expands in source account zone", zone: "America/Chicago", day: "2026-11-04", start: "2026-10-21T14:00:00Z", end: "2026-10-21T15:00:00Z", wantDay: "2026-11-05", wantStart: "00:00:00", wantEnd: "01:00:00", flags: []string{"--time-zone", "Asia/Tokyo"}},
		{name: "missing source zone", day: "2026-11-04", start: "2026-10-21T14:00:00Z", end: "2026-10-21T15:00:00Z", refused: true},
		{name: "unknown source zone even with destination supplied", zone: "Unknown/Zone", day: "2026-11-04", start: "2026-10-21T14:00:00Z", end: "2026-10-21T15:00:00Z", flags: []string{"--time-zone", "Asia/Tokyo"}, refused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			series := fmt.Sprintf(`{"id":4821,"type":"Calendar::Event","title":"Weekly planning","recurring":true,"starts_at":%q,"ends_at":%q,"recurrence_schedule":{"kind":"every_week","preset":true},"calendar":{"id":9}}`, tt.start, tt.end)
			handler, writes := occurrenceServer(t, tt.day, `{"Calendar::Event":[`+series+`]}`, "", func(t *testing.T, form url.Values) {
				for field, want := range map[string]string{
					"calendar_event[starts_at]":      tt.wantDay,
					"calendar_event[starts_at_time]": tt.wantStart,
					"calendar_event[ends_at_time]":   tt.wantEnd,
				} {
					if got := form.Get(field); got != want {
						t.Errorf("%s = %q, want %q", field, got, want)
					}
				}
				if len(tt.flags) == 0 && form.Get("calendar_event[set_time_zone]") != "0" {
					t.Error("zoneless occurrence acquired an explicit zone")
				}
			})
			reads := 0
			withZone := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/identity.json" {
					reads++
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"id":1,"time_zone":%q}`, tt.zone)
					return
				}
				handler.ServeHTTP(w, r)
			})
			args := append([]string{"event", "edit", "4821", "--occurrence", "4821_" + tt.day, "--apply-to", "current", "--title", "Weekly planning (revised)"}, tt.flags...)
			_, err := runJSONCommand(t, withZone, args...)
			if tt.refused {
				if err == nil || !strings.Contains(err.Error(), "no time zone to expand") || writes.Load() != 0 {
					t.Fatalf("error=%v writes=%d, want source-zone refusal without a write", err, writes.Load())
				}
			} else if err != nil || writes.Load() != 1 {
				t.Fatalf("error=%v writes=%d, want one successful write", err, writes.Load())
			}
			if reads != 1 {
				t.Errorf("identity reads=%d, want one cached source-zone read", reads)
			}
		})
	}
}

func TestEventsRealizedZonelessOccurrenceKeepsItsOwnTime(t *testing.T) {
	const series = `{"id":4821,"type":"Calendar::Event","recurring":true,"starts_at":"2026-10-21T14:00:00Z","ends_at":"2026-10-21T15:00:00Z","recurrence_schedule":{"kind":"every_week","preset":true}}`
	const realized = `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-11-04","starts_at":"2026-11-04T17:00:00Z","ends_at":"2026-11-04T18:00:00Z"}`
	handler, writes := occurrenceServer(t, "2026-11-04", `{"Calendar::Event":[`+series+`,`+realized+`]}`, "", func(t *testing.T, form url.Values) {
		if got := form.Get("calendar_event[starts_at_time]"); got != "17:00:00" {
			t.Errorf("start=%q, want realized day's own time", got)
		}
	})
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity.json" {
			t.Error("realized current-only edit must not need an account-zone read")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		handler.ServeHTTP(w, r)
	}), "event", "edit", "4821", "--occurrence", "4821_2026-11-04", "--apply-to", "current", "--title", "Weekly planning (revised)")
	if err != nil || writes.Load() != 1 {
		t.Fatalf("error=%v writes=%d, want one successful write", err, writes.Load())
	}
}

func TestZonelessOccurrenceFutureBoundaryUsesAccountZone(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, "2026-10-21T14:00:00Z")
	date, _ := time.Parse(dateLayout, "2026-11-04")
	series := generated.Recording{Id: 4821, StartsAt: start, EndsAt: start.Add(time.Hour), RecurrenceSchedule: generated.RecurrenceSchedule{Preset: true}}
	for _, hour := range []int{14, 15, 16} {
		t.Run(fmt.Sprint(hour), func(t *testing.T) {
			at := date.Add(time.Duration(hour) * time.Hour)
			day := occurrenceDay{occurrence: hey.EventOccurrence{EventID: series.Id, Date: date}, series: series, realized: &generated.Recording{StartsAt: at}}
			for _, verb := range []string{"changed", "deleted"} {
				err := day.refuseAnUnsafeFutureBoundary(context.Background(), &accountZone{read: true, name: "America/Chicago"}, verb, "custom", "moved")
				if (err == nil) != (hour == 15) {
					t.Errorf("%s at %02d:00Z: %v; only 15:00Z is the unmoved day", verb, hour, err)
				}
			}
		})
	}
}
