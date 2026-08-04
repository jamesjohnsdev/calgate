package google

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jamesjohnsdev/calgate"
)

const sampleGoogleEvent = `{
  "id": "evt-1",
  "summary": "Study block",
  "description": "Revise chapter 4",
  "status": "confirmed",
  "start": {"dateTime": "2026-08-04T11:00:00Z"},
  "end":   {"dateTime": "2026-08-04T12:00:00Z"},
  "location": "Library",
  "organizer": {"displayName": "Jo", "email": "jo@example.com"},
  "attendees": [
    {"displayName": "Al", "email": "al@example.com", "responseStatus": "accepted"},
    {"displayName": "Bo", "email": "bo@example.com", "responseStatus": "needsAction"}
  ],
  "recurrence": ["RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE:20260810T000000Z"]
}`

func TestGEvent_ToEvent(t *testing.T) {
	var ge gEvent
	if err := json.Unmarshal([]byte(sampleGoogleEvent), &ge); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	e := ge.toEvent()

	if e.ID != "evt-1" || e.Title != "Study block" || e.Description != "Revise chapter 4" {
		t.Errorf("basic fields wrong: %+v", e)
	}
	if e.Provider != calgate.ProviderGoogle {
		t.Errorf("provider = %q, want google", e.Provider)
	}
	if e.Location != "Library" {
		t.Errorf("location = %q, want Library", e.Location)
	}
	if e.Organizer != "Jo" {
		t.Errorf("organizer = %q, want Jo (displayName preferred)", e.Organizer)
	}
	if e.Status != "confirmed" {
		t.Errorf("status = %q, want confirmed", e.Status)
	}
	if e.AllDay {
		t.Error("AllDay should be false for dateTime event")
	}
	wantStart := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	if !e.Start.Equal(wantStart) {
		t.Errorf("start = %v, want %v", e.Start, wantStart)
	}
	wantEnd := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if !e.End.Equal(wantEnd) {
		t.Errorf("end = %v, want %v", e.End, wantEnd)
	}
	if len(e.Attendees) != 2 {
		t.Fatalf("attendees len = %d, want 2", len(e.Attendees))
	}
	if e.Attendees[0].Name != "Al" || e.Attendees[0].Email != "al@example.com" || e.Attendees[0].Response != "accepted" {
		t.Errorf("attendee[0] wrong: %+v", e.Attendees[0])
	}
	if e.Attendees[1].Response != "none" {
		t.Errorf("attendee[1] response = %q, want none (needsAction maps to none)", e.Attendees[1].Response)
	}
	// Takes only the first rule.
	if e.Recurrence != "RRULE:FREQ=WEEKLY;BYDAY=MO" {
		t.Errorf("recurrence = %q, want first RRULE", e.Recurrence)
	}
	if len(e.Raw) == 0 {
		t.Error("Raw payload not captured")
	}
}

func TestGEvent_AllDay(t *testing.T) {
	var ge gEvent
	if err := json.Unmarshal([]byte(`{
		"id":"e2",
		"start":{"date":"2026-08-04"},
		"end":{"date":"2026-08-05"}
	}`), &ge); err != nil {
		t.Fatal(err)
	}
	e := ge.toEvent()
	if !e.AllDay {
		t.Error("AllDay should be true for date-only event")
	}
	wantStart := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	if !e.Start.Equal(wantStart) {
		t.Errorf("start = %v, want %v", e.Start, wantStart)
	}
}

func TestGEvent_StatusMapping(t *testing.T) {
	cases := map[string]string{
		`{"status":"confirmed"}`:  "confirmed",
		`{"status":"tentative"}`:  "tentative",
		`{"status":"cancelled"}`:  "cancelled",
		`{"status":""}`:           "",
	}
	for in, want := range cases {
		var ge gEvent
		if err := json.Unmarshal([]byte(in), &ge); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if got := ge.toEvent().Status; got != want {
			t.Errorf("input %s -> status %q, want %q", in, got, want)
		}
	}
}

func TestGEvent_AttendeeResponseMapping(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"accepted", "accepted"},
		{"declined", "declined"},
		{"tentative", "tentative"},
		{"needsAction", "none"},
		{"", "none"},
		{"unknown", "none"},
	}
	for _, tc := range cases {
		a := gAttendee{ResponseStatus: tc.in}
		if got := a.toAttendee().Response; got != tc.want {
			t.Errorf("responseStatus %q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGEvent_OrganizerFallsBackToEmail(t *testing.T) {
	var ge gEvent
	if err := json.Unmarshal([]byte(`{"organizer":{"email":"alice@example.com"}}`), &ge); err != nil {
		t.Fatal(err)
	}
	e := ge.toEvent()
	if e.Organizer != "alice@example.com" {
		t.Errorf("organizer = %q, want email fallback", e.Organizer)
	}
}

func TestGEvent_RecurrenceTakesFirst(t *testing.T) {
	var ge gEvent
	if err := json.Unmarshal([]byte(`{"recurrence":["RRULE:FREQ=DAILY","EXDATE:20260810"]}`), &ge); err != nil {
		t.Fatal(err)
	}
	if got := ge.toEvent().Recurrence; got != "RRULE:FREQ=DAILY" {
		t.Errorf("recurrence = %q, want first element", got)
	}
}

func TestGEvent_NoRecurrence(t *testing.T) {
	var ge gEvent
	if err := json.Unmarshal([]byte(`{}`), &ge); err != nil {
		t.Fatal(err)
	}
	if got := ge.toEvent().Recurrence; got != "" {
		t.Errorf("recurrence = %q, want empty for event with no recurrence", got)
	}
}

func TestGCalendar_ToCalendar(t *testing.T) {
	var gc gCalendar
	if err := json.Unmarshal([]byte(`{"id":"primary","summary":"My Calendar","primary":true}`), &gc); err != nil {
		t.Fatal(err)
	}
	c := gc.toCalendar()
	if c.ID != "primary" || c.Name != "My Calendar" || !c.Primary {
		t.Errorf("calendar wrong: %+v", c)
	}
	if c.Provider != calgate.ProviderGoogle {
		t.Errorf("provider = %q, want google", c.Provider)
	}
	if len(c.Raw) == 0 {
		t.Error("Raw payload not captured")
	}
}

func TestGCalendar_NonPrimary(t *testing.T) {
	var gc gCalendar
	if err := json.Unmarshal([]byte(`{"id":"c2","summary":"Work"}`), &gc); err != nil {
		t.Fatal(err)
	}
	if gc.toCalendar().Primary {
		t.Error("Primary should be false")
	}
}

func TestGDateTime_Parse(t *testing.T) {
	cases := []struct {
		dt     gDateTime
		want   time.Time
		allDay bool
	}{
		{
			dt:     gDateTime{DateTime: "2026-08-04T11:00:00Z"},
			want:   time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC),
			allDay: false,
		},
		{
			dt:     gDateTime{Date: "2026-08-04"},
			want:   time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
			allDay: true,
		},
		{
			dt:     gDateTime{},
			want:   time.Time{},
			allDay: false,
		},
		{
			dt:     gDateTime{DateTime: "not-a-date"},
			want:   time.Time{},
			allDay: false,
		},
		{
			dt:     gDateTime{Date: "bad-date"},
			want:   time.Time{},
			allDay: true,
		},
	}
	for _, tc := range cases {
		gotT, gotAllDay := tc.dt.parse()
		if gotAllDay != tc.allDay {
			t.Errorf("parse(%+v) allDay = %v, want %v", tc.dt, gotAllDay, tc.allDay)
		}
		if !gotT.Equal(tc.want) {
			t.Errorf("parse(%+v) time = %v, want %v", tc.dt, gotT, tc.want)
		}
	}
}

// ── Fuzz ──────────────────────────────────────────────────────────────────────

// FuzzGEventToEvent throws arbitrary JSON at the unmarshal+map path.
// Must never panic; provider must always be stamped on success.
func FuzzGEventToEvent(f *testing.F) {
	f.Add(sampleGoogleEvent)
	f.Add(`{}`)
	f.Add(`{"start":{"dateTime":"x"},"end":{"date":"y"}}`)
	f.Add(`{"attendees":[{"responseStatus":""}]}`)
	f.Add(`{"recurrence":[]}`)
	f.Add(`{"recurrence":null}`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, data string) {
		var ge gEvent
		if err := json.Unmarshal([]byte(data), &ge); err != nil {
			return
		}
		e := ge.toEvent()
		if e.Provider != calgate.ProviderGoogle {
			t.Fatalf("provider not stamped for input %q", data)
		}
	})
}

// FuzzGDateTimeParse ensures parsing never panics on arbitrary input.
func FuzzGDateTimeParse(f *testing.F) {
	f.Add("2026-08-04T11:00:00Z", "")
	f.Add("", "2026-08-04")
	f.Add("garbage", "")
	f.Add("", "not-a-date")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, dt, date string) {
		_, _ = gDateTime{DateTime: dt, Date: date}.parse()
	})
}
