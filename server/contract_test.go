// Package server_test contains black-box HTTP contract tests. Each test spins
// up a real httptest.Server and exercises the API surface end-to-end through
// actual HTTP requests, validating status codes, Content-Type, and response
// body shapes.
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/jamesjohnsdev/calgate"
	"github.com/jamesjohnsdev/calgate/server"
)

// ── Contract mocks ────────────────────────────────────────────────────────────

type cAuth struct {
	baseURL string
	tok     *oauth2.Token
	err     error
}

func (a *cAuth) AuthURL(state string) string { return a.baseURL + "?state=" + state }
func (a *cAuth) Exchange(_ context.Context, _ string) (*oauth2.Token, error) {
	return a.tok, a.err
}

type cStore struct {
	mu     sync.RWMutex
	tokens map[string]*oauth2.Token
}

func newCStore() *cStore { return &cStore{tokens: map[string]*oauth2.Token{}} }

func (s *cStore) Load(_ context.Context, userID string) (*oauth2.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokens[userID]
	if !ok {
		return nil, calgate.ErrUnauthorized
	}
	return t, nil
}

func (s *cStore) Save(_ context.Context, userID string, t *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[userID] = t
	return nil
}

type cGW struct {
	calendars []calgate.Calendar
	events    []calgate.Event
	event     calgate.Event
	err       error
}

func (g *cGW) ListCalendars(_ context.Context) ([]calgate.Calendar, error) {
	return g.calendars, g.err
}
func (g *cGW) ListEvents(_ context.Context, _ string, _ calgate.TimeRange) ([]calgate.Event, error) {
	return g.events, g.err
}
func (g *cGW) GetEvent(_ context.Context, _, _ string) (calgate.Event, error) {
	return g.event, g.err
}

// ── Test server helpers ───────────────────────────────────────────────────────

type testEnv struct {
	ts    *httptest.Server
	auth  *cAuth
	store *cStore
	gw    *cGW
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{
		auth: &cAuth{
			baseURL: "https://mock.test/oauth",
			tok:     &oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)},
		},
		store: newCStore(),
		gw:    &cGW{},
	}
	srv := server.New(server.Config{
		Authenticator: env.auth,
		Store:         env.store,
		NewGateway: func(ctx context.Context, userID string) (calgate.Gateway, error) {
			if _, err := env.store.Load(ctx, userID); err != nil {
				return nil, err
			}
			return env.gw, nil
		},
	})
	env.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(env.ts.Close)
	return env
}

// mustAuth drives the full auth flow for userID via HTTP and fatals on any failure.
func mustAuth(t *testing.T, baseURL, userID string) {
	t.Helper()

	// Step 1: begin auth.
	body, _ := json.Marshal(map[string]string{"user_id": userID})
	resp, err := http.Post(baseURL+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("POST /auth status = %d: %s", resp.StatusCode, b)
	}
	var ar struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		resp.Body.Close()
		t.Fatalf("decode auth response: %v", err)
	}
	resp.Body.Close()

	// Step 2: extract state from consent URL.
	u, err := url.Parse(ar.AuthURL)
	if err != nil {
		t.Fatalf("parse auth_url %q: %v", ar.AuthURL, err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in auth_url %q", ar.AuthURL)
	}

	// Step 3: simulate provider redirect with code.
	cbURL := baseURL + "/auth/callback?code=testcode&state=" + state
	resp2, err := http.Get(cbURL)
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("GET /auth/callback status = %d: %s", resp2.StatusCode, b)
	}
}

// get sends a GET and returns the response; caller is responsible for closing Body.
func get(t *testing.T, rawURL string) *http.Response {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	return resp
}

// postJSON sends a POST with a JSON body and returns the response.
func postJSON(t *testing.T, rawURL string, v any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(v)
	resp, err := http.Post(rawURL, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	return resp
}

// assertStatus fatals if resp.StatusCode != want.
func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want %d — body: %s", resp.StatusCode, want, b)
	}
}

// decodeJSON decodes JSON from resp.Body into v and closes the body.
func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// ── Auth tests ────────────────────────────────────────────────────────────────

func TestAuthBegin(t *testing.T) {
	env := newTestEnv(t)

	resp := postJSON(t, env.ts.URL+"/auth", map[string]string{"user_id": "alice"})
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		AuthURL string `json:"auth_url"`
	}
	decodeJSON(t, resp, &body)

	if body.AuthURL == "" {
		t.Fatal("auth_url is empty")
	}
	u, err := url.Parse(body.AuthURL)
	if err != nil {
		t.Fatalf("auth_url is not a valid URL: %v", err)
	}
	if u.Query().Get("state") == "" {
		t.Error("auth_url has no state param")
	}
}

func TestAuthCallback_ValidState(t *testing.T) {
	env := newTestEnv(t)
	// mustAuth covers the full round-trip and fatals if anything goes wrong.
	mustAuth(t, env.ts.URL, "alice")
}

func TestAuthCallback_UnknownState(t *testing.T) {
	env := newTestEnv(t)

	resp := get(t, env.ts.URL+"/auth/callback?code=x&state=doesnotexist")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusBadRequest)
}

func TestAuthCallback_MissingParams(t *testing.T) {
	env := newTestEnv(t)

	// No code or state at all — huma returns 422 for required query params.
	resp := get(t, env.ts.URL+"/auth/callback")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected non-200 for missing required params")
	}
}

// ── Calendar tests ────────────────────────────────────────────────────────────

func TestListCalendars(t *testing.T) {
	env := newTestEnv(t)
	env.gw.calendars = []calgate.Calendar{
		{ID: "cal-1", Name: "Personal", Provider: calgate.ProviderOutlook},
		{ID: "cal-2", Name: "Work", Provider: calgate.ProviderOutlook},
	}
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars")
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Calendars []map[string]any `json:"calendars"`
	}
	decodeJSON(t, resp, &body)

	if len(body.Calendars) != 2 {
		t.Fatalf("calendars len = %d, want 2", len(body.Calendars))
	}
	if body.Calendars[0]["ID"] != "cal-1" {
		t.Errorf("first calendar ID = %v, want cal-1", body.Calendars[0]["ID"])
	}
}

func TestListCalendars_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	// "bob" never authorized — expect 401.
	resp := get(t, env.ts.URL+"/users/bob/calendars")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestListCalendars_ProviderError(t *testing.T) {
	env := newTestEnv(t)
	env.gw.err = calgate.ErrProvider
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusBadGateway)
}

// ── Event tests ───────────────────────────────────────────────────────────────

func TestListEvents(t *testing.T) {
	env := newTestEnv(t)
	env.gw.events = []calgate.Event{
		{ID: "ev-1", Title: "Standup", Provider: calgate.ProviderOutlook},
	}
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars/cal-1/events")
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	decodeJSON(t, resp, &body)

	if len(body.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(body.Events))
	}
	if body.Events[0]["ID"] != "ev-1" {
		t.Errorf("event ID = %v, want ev-1", body.Events[0]["ID"])
	}
}

func TestListEvents_WithTimeRange(t *testing.T) {
	env := newTestEnv(t)
	env.gw.events = []calgate.Event{{ID: "ev-1", Title: "Meeting"}}
	mustAuth(t, env.ts.URL, "alice")

	start := time.Now().UTC().Format(time.RFC3339)
	end := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	u := env.ts.URL + "/users/alice/calendars/cal-1/events?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

	resp := get(t, u)
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	decodeJSON(t, resp, &body)
	if len(body.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(body.Events))
	}
}

func TestListEvents_InvalidStart(t *testing.T) {
	env := newTestEnv(t)
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars/cal-1/events?start=not-a-date")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestListEvents_InvalidEnd(t *testing.T) {
	env := newTestEnv(t)
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars/cal-1/events?end=garbage")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestGetEvent(t *testing.T) {
	env := newTestEnv(t)
	env.gw.event = calgate.Event{ID: "ev-1", Title: "Standup", Provider: calgate.ProviderOutlook}
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars/cal-1/events/ev-1")
	assertStatus(t, resp, http.StatusOK)

	var body map[string]any
	decodeJSON(t, resp, &body)

	if body["ID"] != "ev-1" {
		t.Errorf("event ID = %v, want ev-1", body["ID"])
	}
	if body["Title"] != "Standup" {
		t.Errorf("event Title = %v, want Standup", body["Title"])
	}
}

func TestGetEvent_NotFound(t *testing.T) {
	env := newTestEnv(t)
	env.gw.err = calgate.ErrNotFound
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars/cal-1/events/missing")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusNotFound)
}

func TestGetEvent_RateLimited(t *testing.T) {
	env := newTestEnv(t)
	env.gw.err = calgate.ErrRateLimited
	mustAuth(t, env.ts.URL, "alice")

	resp := get(t, env.ts.URL+"/users/alice/calendars/cal-1/events/ev-1")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusTooManyRequests)
}

// ── Response shape contract ───────────────────────────────────────────────────

// TestResponseContentType verifies all JSON endpoints advertise application/json.
func TestResponseContentType(t *testing.T) {
	env := newTestEnv(t)
	env.gw.calendars = []calgate.Calendar{{ID: "c1", Name: "Cal"}}
	mustAuth(t, env.ts.URL, "alice")

	endpoints := []string{
		"/users/alice/calendars",
	}
	for _, ep := range endpoints {
		resp := get(t, env.ts.URL+ep)
		assertStatus(t, resp, http.StatusOK)
		ct := resp.Header.Get("Content-Type")
		resp.Body.Close()
		if ct == "" {
			t.Errorf("%s: missing Content-Type header", ep)
		}
	}
}

// TestOpenAPISpec verifies huma serves the generated spec.
func TestOpenAPISpec(t *testing.T) {
	env := newTestEnv(t)

	resp := get(t, env.ts.URL+"/openapi.json")
	assertStatus(t, resp, http.StatusOK)

	var spec map[string]any
	decodeJSON(t, resp, &spec)

	if spec["openapi"] == nil {
		t.Error("openapi field missing from spec")
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths field missing or not an object")
	}
	required := []string{
		"/auth",
		"/auth/callback",
		"/users/{userID}/calendars",
		"/users/{userID}/calendars/{calendarID}/events",
		"/users/{userID}/calendars/{calendarID}/events/{eventID}",
	}
	for _, p := range required {
		if paths[p] == nil {
			t.Errorf("path %q missing from OpenAPI spec", p)
		}
	}
}
