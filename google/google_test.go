package google

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jamesjohnsdev/calgate"
)

// newTestGateway points a gateway at an httptest server (no OAuth involved).
func newTestGateway(srv *httptest.Server) *gateway {
	return &gateway{http: srv.Client(), base: srv.URL}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// ── Client ────────────────────────────────────────────────────────────────────

func TestClient_AuthURL(t *testing.T) {
	c := New(Config{
		ClientID:    "cid",
		RedirectURL: "https://app.example.com/cb",
	})
	u := c.AuthURL("xyz-state")
	for _, want := range []string{
		"client_id=cid",
		"state=xyz-state",
		"access_type=offline",
		"redirect_uri=https%3A%2F%2Fapp.example.com%2Fcb",
		"accounts.google.com",
		"calendar.readonly",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("AuthURL missing %q\ngot: %s", want, u)
		}
	}
}

func TestClient_DefaultScopes(t *testing.T) {
	c := New(Config{ClientID: "x"})
	if len(c.oauth.Scopes) == 0 {
		t.Fatal("expected default scopes to be set")
	}
	found := false
	for _, s := range c.oauth.Scopes {
		if strings.Contains(s, "calendar.readonly") {
			found = true
		}
	}
	if !found {
		t.Errorf("calendar.readonly not in default scopes: %v", c.oauth.Scopes)
	}
}

// ── ListCalendars ─────────────────────────────────────────────────────────────

func TestGateway_ListCalendars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/calendarList") {
			t.Errorf("unexpected path %q, want .../calendarList", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"id":"primary","summary":"My Calendar","primary":true},
			{"id":"work","summary":"Work"}
		]}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	cals, err := gw.ListCalendars(context.Background())
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 2 {
		t.Fatalf("len = %d, want 2", len(cals))
	}
	if cals[0].ID != "primary" || cals[0].Name != "My Calendar" || !cals[0].Primary {
		t.Errorf("cal[0] wrong: %+v", cals[0])
	}
	if cals[1].ID != "work" || cals[1].Primary {
		t.Errorf("cal[1] wrong: %+v", cals[1])
	}
	if cals[0].Provider != calgate.ProviderGoogle {
		t.Errorf("provider = %q, want google", cals[0].Provider)
	}
}

func TestGateway_ListCalendars_Paging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/calendarList") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "tok2" {
			_, _ = w.Write([]byte(`{"items":[{"id":"c2","summary":"Personal"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"c1","summary":"Work"}],"nextPageToken":"tok2"}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	cals, err := gw.ListCalendars(context.Background())
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 2 || cals[0].ID != "c1" || cals[1].ID != "c2" {
		t.Fatalf("got %+v, want 2 calendars c1,c2", cals)
	}
}

func TestGateway_ListCalendars_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	cals, err := gw.ListCalendars(context.Background())
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 0 {
		t.Errorf("expected empty slice, got %+v", cals)
	}
}

// ── ListEvents ────────────────────────────────────────────────────────────────

func TestGateway_ListEvents_WithRange(t *testing.T) {
	var sawQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"e1","summary":"Meeting",
			"start":{"dateTime":"2026-08-04T09:00:00Z"},
			"end":{"dateTime":"2026-08-04T10:00:00Z"},
			"status":"confirmed"}]}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	r := calgate.TimeRange{
		Start: mustTime("2026-08-01T00:00:00Z"),
		End:   mustTime("2026-08-31T00:00:00Z"),
	}
	evts, err := gw.ListEvents(context.Background(), "primary", r)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evts) != 1 || evts[0].ID != "e1" {
		t.Fatalf("got %+v", evts)
	}
	if sawQuery.Get("singleEvents") != "true" {
		t.Error("singleEvents=true required when a time range is given")
	}
	if sawQuery.Get("timeMin") == "" || sawQuery.Get("timeMax") == "" {
		t.Error("timeMin and timeMax must be set for a bounded range")
	}
	if evts[0].Provider != calgate.ProviderGoogle {
		t.Errorf("provider = %q, want google", evts[0].Provider)
	}
}

func TestGateway_ListEvents_NoRange(t *testing.T) {
	var sawQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	if _, err := gw.ListEvents(context.Background(), "primary", calgate.TimeRange{}); err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if sawQuery.Get("singleEvents") != "" {
		t.Errorf("singleEvents should be absent for unbounded query, got %q", sawQuery.Get("singleEvents"))
	}
	if sawQuery.Get("timeMin") != "" || sawQuery.Get("timeMax") != "" {
		t.Error("timeMin/timeMax should be absent for unbounded query")
	}
}

func TestGateway_ListEvents_Paging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "p2" {
			_, _ = w.Write([]byte(`{"items":[{"id":"e2","start":{"dateTime":"2026-08-04T10:00:00Z"},"end":{"dateTime":"2026-08-04T11:00:00Z"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"e1","start":{"dateTime":"2026-08-04T09:00:00Z"},"end":{"dateTime":"2026-08-04T10:00:00Z"}}],"nextPageToken":"p2"}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	evts, err := gw.ListEvents(context.Background(), "primary", calgate.TimeRange{})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evts) != 2 || evts[0].ID != "e1" || evts[1].ID != "e2" {
		t.Fatalf("got %+v, want [e1, e2]", evts)
	}
}

func TestGateway_ListEvents_PathContainsCalendarID(t *testing.T) {
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	if _, err := gw.ListEvents(context.Background(), "cal-abc@example.com", calgate.TimeRange{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sawPath, "/calendars/") {
		t.Errorf("path %q missing /calendars/ prefix", sawPath)
	}
	if !strings.Contains(sawPath, "/events") {
		t.Errorf("path %q missing /events suffix", sawPath)
	}
}

// ── GetEvent ──────────────────────────────────────────────────────────────────

func TestGateway_GetEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events/evt-9") {
			t.Errorf("path = %q, want .../events/evt-9", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"evt-9","summary":"Sync",
			"start":{"dateTime":"2026-08-04T14:00:00Z"},
			"end":{"dateTime":"2026-08-04T14:30:00Z"},
			"status":"confirmed"
		}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	e, err := gw.GetEvent(context.Background(), "primary", "evt-9")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if e.ID != "evt-9" || e.Title != "Sync" {
		t.Errorf("got %+v", e)
	}
	if e.Provider != calgate.ProviderGoogle {
		t.Errorf("provider = %q, want google", e.Provider)
	}
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func TestGateway_ErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, calgate.ErrUnauthorized},
		{http.StatusNotFound, calgate.ErrNotFound},
		{http.StatusTooManyRequests, calgate.ErrRateLimited},
		{http.StatusInternalServerError, calgate.ErrProvider},
		{http.StatusServiceUnavailable, calgate.ErrProvider},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", c.status)
		}))
		gw := newTestGateway(srv)
		_, err := gw.ListCalendars(context.Background())
		if !errors.Is(err, c.want) {
			t.Errorf("status %d -> err %v, want %v", c.status, err, c.want)
		}
		srv.Close()
	}
}
