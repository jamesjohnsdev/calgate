package outlook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestGateway_ListCalendars_Paging(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me/calendars" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"value":[{"id":"c2","name":"Personal"}]}`))
			return
		}
		// First page points at page 2 via an absolute Graph-style nextLink;
		// relLink must strip the /v1.0 prefix and re-request against base.
		_, _ = w.Write([]byte(`{"value":[{"id":"c1","name":"Work","isDefaultCalendar":true}],` +
			`"@odata.nextLink":"` + srv.URL + `/v1.0/me/calendars?page=2"}`))
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
	if !cals[0].Primary {
		t.Error("c1 should be primary (isDefaultCalendar)")
	}
}

func TestGateway_ListEvents_UsesCalendarViewForRange(t *testing.T) {
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{"id":"e1","subject":"S"}]}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	r := calgate.TimeRange{
		Start: mustTime("2026-08-01T00:00:00Z"),
		End:   mustTime("2026-08-31T00:00:00Z"),
	}
	evts, err := gw.ListEvents(context.Background(), "cal-1", r)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evts) != 1 || evts[0].ID != "e1" {
		t.Fatalf("got %+v", evts)
	}
	if !strings.HasSuffix(sawPath, "/calendarView") {
		t.Errorf("path = %q, want calendarView for a bounded range", sawPath)
	}
}

func TestGateway_ListEvents_UsesEventsWithoutRange(t *testing.T) {
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	if _, err := gw.ListEvents(context.Background(), "cal-1", calgate.TimeRange{}); err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !strings.HasSuffix(sawPath, "/events") {
		t.Errorf("path = %q, want /events for an unbounded query", sawPath)
	}
}

func TestGateway_GetEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events/evt-9") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"evt-9","subject":"Hi"}`))
	}))
	defer srv.Close()

	gw := newTestGateway(srv)
	e, err := gw.GetEvent(context.Background(), "cal-1", "evt-9")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if e.ID != "evt-9" || e.Title != "Hi" {
		t.Fatalf("got %+v", e)
	}
}

func TestGateway_ErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, calgate.ErrUnauthorized},
		{http.StatusNotFound, calgate.ErrNotFound},
		{http.StatusTooManyRequests, calgate.ErrRateLimited},
		{http.StatusInternalServerError, calgate.ErrProvider},
		{http.StatusBadGateway, calgate.ErrProvider},
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

func TestClient_AuthURL(t *testing.T) {
	c := New(Config{
		ClientID:    "cid",
		RedirectURL: "https://app.example.com/cb",
		Tenant:      "common",
	})
	u := c.AuthURL("xyz-state")
	for _, want := range []string{
		"client_id=cid",
		"state=xyz-state",
		"offline_access",       // refresh-token scope must be present
		"access_type=offline",  // AccessTypeOffline option
		"redirect_uri=https%3A%2F%2Fapp.example.com%2Fcb",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("AuthURL missing %q\ngot: %s", want, u)
		}
	}
}

func TestRelLink(t *testing.T) {
	cases := map[string]string{
		"":                                            "",
		"https://graph.microsoft.com/v1.0/me/events":  "/me/events",
		"https://graph.microsoft.com/v1.0/me/events?$skiptoken=abc": "/me/events?$skiptoken=abc",
		"://bad":                                      "",
	}
	for in, want := range cases {
		if got := relLink(in); got != want {
			t.Errorf("relLink(%q) = %q, want %q", in, got, want)
		}
	}
}

// FuzzRelLink ensures nextLink parsing never panics on hostile input (it slices
// paths, which historically risked an out-of-range panic).
func FuzzRelLink(f *testing.F) {
	f.Add("https://graph.microsoft.com/v1.0/me/events?x=1")
	f.Add("/v1.0")
	f.Add("/x")
	f.Add("")
	f.Add("://")
	f.Fuzz(func(t *testing.T, s string) {
		_ = relLink(s)
	})
}
