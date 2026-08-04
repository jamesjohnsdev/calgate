// End-to-end integration tests for the calgate server using the real
// google.Gateway. A mock Google Calendar API server serves canned responses;
// the real google.Gateway maps them to calgate types; the calgate server
// exposes them over HTTP. This tests the full chain without hitting Google.
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/jamesjohnsdev/calgate/google"
	"github.com/jamesjohnsdev/calgate/server"
)

// mockGoogleAPI returns an httptest.Server that mimics the Google Calendar v3 API.
func mockGoogleAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/calendarList"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{
					map[string]any{"id": "primary", "summary": "My Calendar", "primary": true},
					map[string]any{"id": "work", "summary": "Work"},
				},
			})
		case strings.Contains(r.URL.Path, "/events/evt-1"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "evt-1",
				"summary": "Standup",
				"status":  "confirmed",
				"start":   map[string]any{"dateTime": "2026-08-04T09:00:00Z"},
				"end":     map[string]any{"dateTime": "2026-08-04T09:30:00Z"},
			})
		case strings.Contains(r.URL.Path, "/events"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{
					map[string]any{
						"id":      "evt-1",
						"summary": "Standup",
						"status":  "confirmed",
						"start":   map[string]any{"dateTime": "2026-08-04T09:00:00Z"},
						"end":     map[string]any{"dateTime": "2026-08-04T09:30:00Z"},
					},
					map[string]any{
						"id":      "evt-2",
						"summary": "All-hands",
						"status":  "confirmed",
						"start":   map[string]any{"date": "2026-08-05"},
						"end":     map[string]any{"date": "2026-08-06"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// newGoogleServerEnv wires a calgate server using the real google.Gateway
// pointed at the given mock Google Calendar API server.
func newGoogleServerEnv(t *testing.T, googleAPISrv *httptest.Server) (*httptest.Server, string) {
	t.Helper()

	// Use NewWithBase so the real google.Gateway calls the mock API instead of Google.
	googleClient := google.NewWithBase(google.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/callback",
	}, googleAPISrv.URL)

	srv := server.New(server.Config{
		// Mock authenticator: auth URL and Exchange work without hitting Google.
		Authenticator: &cAuth{
			baseURL: "https://accounts.google.com/mock/oauth",
			tok:     &oauth2.Token{AccessToken: "test-tok", Expiry: time.Now().Add(time.Hour)},
		},
		NewTokenSource: func(_ context.Context, tok *oauth2.Token) oauth2.TokenSource {
			return oauth2.StaticTokenSource(tok)
		},
		// Real google.Gateway: calls the mock Google Calendar API.
		NewGateway: googleClient.Gateway,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(googleAPISrv.Close)

	tok := mustAuth(t, ts.URL)
	return ts, tok
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGoogle_ListCalendars_EndToEnd(t *testing.T) {
	ts, tok := newGoogleServerEnv(t, mockGoogleAPI(t))

	resp := getWithToken(t, ts.URL+"/calendars", tok)
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Calendars []struct {
			ID      string `json:"ID"`
			Name    string `json:"Name"`
			Primary bool   `json:"Primary"`
		} `json:"calendars"`
	}
	decodeJSON(t, resp, &body)

	if len(body.Calendars) != 2 {
		t.Fatalf("calendars len = %d, want 2", len(body.Calendars))
	}
	if body.Calendars[0].ID != "primary" || body.Calendars[0].Name != "My Calendar" {
		t.Errorf("cal[0] wrong: %+v", body.Calendars[0])
	}
	if !body.Calendars[0].Primary {
		t.Error("cal[0].Primary should be true")
	}
	if body.Calendars[1].ID != "work" {
		t.Errorf("cal[1].ID = %q, want work", body.Calendars[1].ID)
	}
}

func TestGoogle_ListEvents_EndToEnd(t *testing.T) {
	ts, tok := newGoogleServerEnv(t, mockGoogleAPI(t))

	resp := getWithToken(t, ts.URL+"/calendars/primary/events", tok)
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Events []struct {
			ID     string `json:"ID"`
			Title  string `json:"Title"`
			Status string `json:"Status"`
			AllDay bool   `json:"AllDay"`
		} `json:"events"`
	}
	decodeJSON(t, resp, &body)

	if len(body.Events) != 2 {
		t.Fatalf("events len = %d, want 2", len(body.Events))
	}
	if body.Events[0].ID != "evt-1" || body.Events[0].Title != "Standup" {
		t.Errorf("event[0] wrong: %+v", body.Events[0])
	}
	if body.Events[0].Status != "confirmed" {
		t.Errorf("event[0].Status = %q, want confirmed", body.Events[0].Status)
	}
	// evt-2 is an all-day event (date-only start).
	if !body.Events[1].AllDay {
		t.Error("event[1] should be AllDay (date-only start)")
	}
}

func TestGoogle_GetEvent_EndToEnd(t *testing.T) {
	ts, tok := newGoogleServerEnv(t, mockGoogleAPI(t))

	resp := getWithToken(t, ts.URL+"/calendars/primary/events/evt-1", tok)
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		ID    string `json:"ID"`
		Title string `json:"Title"`
	}
	decodeJSON(t, resp, &body)

	if body.ID != "evt-1" || body.Title != "Standup" {
		t.Errorf("event wrong: %+v", body)
	}
}

func TestGoogle_NotFound_EndToEnd(t *testing.T) {
	googleAPISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	ts, tok := newGoogleServerEnv(t, googleAPISrv)

	resp := getWithToken(t, ts.URL+"/calendars/primary/events/missing", tok)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusNotFound)
}
