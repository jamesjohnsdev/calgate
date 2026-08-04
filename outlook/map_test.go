package outlook

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jamesjohnsdev/calgate"
)

const sampleEvent = `{
  "id": "evt-1",
  "subject": "Study block",
  "bodyPreview": "Revise chapter 4",
  "isAllDay": false,
  "isCancelled": false,
  "showAs": "busy",
  "start": {"dateTime": "2026-08-04T11:00:00.0000000", "timeZone": "UTC"},
  "end":   {"dateTime": "2026-08-04T12:00:00.0000000", "timeZone": "UTC"},
  "location": {"displayName": "Library"},
  "organizer": {"emailAddress": {"name": "Jo", "address": "jo@example.com"}},
  "attendees": [
    {"type": "required", "status": {"response": "accepted"}, "emailAddress": {"name": "Al", "address": "al@example.com"}},
    {"type": "optional", "status": {"response": "none"}, "emailAddress": {"name": "Bo", "address": "bo@example.com"}}
  ],
  "recurrence": {"pattern": {"type": "weekly"}}
}`

func TestGraphEvent_ToEvent(t *testing.T) {
	var ge graphEvent
	if err := json.Unmarshal([]byte(sampleEvent), &ge); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	e := ge.toEvent()

	if e.ID != "evt-1" || e.Title != "Study block" || e.Description != "Revise chapter 4" {
		t.Errorf("basic fields wrong: %+v", e)
	}
	if e.Provider != calgate.ProviderOutlook {
		t.Errorf("provider = %q", e.Provider)
	}
	if e.Location != "Library" || e.Organizer != "jo@example.com" {
		t.Errorf("location/organizer wrong: %q / %q", e.Location, e.Organizer)
	}
	if e.Status != "confirmed" {
		t.Errorf("status = %q, want confirmed", e.Status)
	}
	want := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	if !e.Start.Equal(want) {
		t.Errorf("start = %v, want %v", e.Start, want)
	}
	if len(e.Attendees) != 2 || e.Attendees[0].Response != "accepted" || e.Attendees[1].Response != "none" {
		t.Errorf("attendees wrong: %+v", e.Attendees)
	}
	if e.Recurrence == "" {
		t.Error("recurrence should be preserved as raw JSON")
	}
	if len(e.Raw) == 0 {
		t.Error("Raw payload not captured")
	}
}

func TestGraphEvent_StatusVariants(t *testing.T) {
	cases := map[string]string{
		`{"isCancelled": true}`:   "cancelled",
		`{"showAs": "tentative"}`: "tentative",
		`{"showAs": "busy"}`:      "confirmed",
		`{}`:                      "confirmed",
	}
	for in, want := range cases {
		var ge graphEvent
		if err := json.Unmarshal([]byte(in), &ge); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if got := ge.toEvent().Status; got != want {
			t.Errorf("%s -> status %q, want %q", in, got, want)
		}
	}
}

func TestGraphEvent_RecurrenceNullIsEmpty(t *testing.T) {
	var ge graphEvent
	if err := json.Unmarshal([]byte(`{"recurrence": null}`), &ge); err != nil {
		t.Fatal(err)
	}
	if got := ge.toEvent().Recurrence; got != "" {
		t.Errorf("recurrence = %q, want empty for null", got)
	}
}

func TestGraphCalendar_ToCalendar(t *testing.T) {
	var gc graphCalendar
	if err := json.Unmarshal([]byte(`{"id":"c1","name":"Work","isDefaultCalendar":true}`), &gc); err != nil {
		t.Fatal(err)
	}
	c := gc.toCalendar()
	if c.ID != "c1" || c.Name != "Work" || !c.Primary {
		t.Errorf("calendar wrong: %+v", c)
	}
	if c.Provider != calgate.ProviderOutlook || len(c.Raw) == 0 {
		t.Errorf("provider/raw wrong: %+v", c)
	}
}

func TestGraphDateTime_Parse(t *testing.T) {
	cases := []struct {
		dt, tz string
		want   time.Time
		zero   bool
	}{
		{"2026-08-04T11:00:00.0000000", "UTC", time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC), false},
		{"2026-08-04T11:00:00", "UTC", time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC), false},
		{"2026-08-04T11:00:00Z", "", time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC), false},
		{"", "UTC", time.Time{}, true},
		{"not-a-date", "UTC", time.Time{}, true},
	}
	for _, c := range cases {
		got := graphDateTime{DateTime: c.dt, TimeZone: c.tz}.parse()
		if c.zero {
			if !got.IsZero() {
				t.Errorf("parse(%q,%q) = %v, want zero", c.dt, c.tz, got)
			}
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parse(%q,%q) = %v, want %v", c.dt, c.tz, got, c.want)
		}
	}
}

func TestGraphDateTime_ParseWithTimezone(t *testing.T) {
	// A named zone should shift the wall-clock time to the right instant.
	got := graphDateTime{DateTime: "2026-08-04T11:00:00.0000000", TimeZone: "America/New_York"}.parse()
	// 11:00 EDT (UTC-4 in August) == 15:00 UTC.
	want := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("EDT parse = %v (UTC %v), want %v", got, got.UTC(), want)
	}
}

// --- Fuzz ---

// FuzzGraphEventToEvent throws arbitrary JSON at the unmarshal+map path. It must
// never panic, regardless of malformed/hostile provider payloads.
func FuzzGraphEventToEvent(f *testing.F) {
	f.Add(sampleEvent)
	f.Add(`{}`)
	f.Add(`{"start":{"dateTime":"x","timeZone":"y"}}`)
	f.Add(`{"attendees":[{"emailAddress":{}}]}`)
	f.Add(`{"recurrence":123}`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, data string) {
		var ge graphEvent
		if err := json.Unmarshal([]byte(data), &ge); err != nil {
			return // invalid JSON is fine; we only care that mapping can't panic
		}
		e := ge.toEvent()
		// Invariant: provider is always stamped.
		if e.Provider != calgate.ProviderOutlook {
			t.Fatalf("provider not stamped for input %q", data)
		}
	})
}

// FuzzGraphDateTimeParse ensures date parsing never panics on junk input.
func FuzzGraphDateTimeParse(f *testing.F) {
	f.Add("2026-08-04T11:00:00.0000000", "UTC")
	f.Add("", "")
	f.Add("garbage", "Not/A/Zone")

	f.Fuzz(func(t *testing.T, dt, tz string) {
		_ = graphDateTime{DateTime: dt, TimeZone: tz}.parse()
	})
}
