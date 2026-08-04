// Package server_test contains black-box HTTP contract tests. Each test spins
// up a real httptest.Server and exercises the API surface end-to-end through
// actual HTTP requests, validating status codes, Content-Type, and response
// body and header shapes.
//
// Tokens are caller-managed in this model: mustAuth drives the full auth flow
// and returns the encoded token that subsequent test requests pass via the
// X-Calgate-Token header.
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type staticTS struct{ tok *oauth2.Token }

func (s *staticTS) Token() (*oauth2.Token, error) { return s.tok, nil }

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

// ── Test environment ──────────────────────────────────────────────────────────

type testEnv struct {
	ts   *httptest.Server
	auth *cAuth
	gw   *cGW
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{
		auth: &cAuth{
			baseURL: "https://mock.test/oauth",
			tok:     &oauth2.Token{AccessToken: "tok", RefreshToken: "ref", Expiry: time.Now().Add(time.Hour)},
		},
		gw: &cGW{},
	}
	srv := server.New(server.Config{
		Authenticator: env.auth,
		NewTokenSource: func(_ context.Context, tok *oauth2.Token) oauth2.TokenSource {
			return &staticTS{tok}
		},
		NewGateway: func(_ context.Context, _ oauth2.TokenSource) calgate.Gateway {
			return env.gw
		},
	})
	env.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(env.ts.Close)
	return env
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// mustAuth drives POST /auth → GET /auth/callback and returns the encoded
// token string to use in X-Calgate-Token on subsequent requests.
func mustAuth(t *testing.T, baseURL string) string {
	t.Helper()

	resp, err := http.Post(baseURL+"/auth", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("POST /auth status = %d: %s", resp.StatusCode, b)
	}
	var ar struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode POST /auth body: %v", err)
	}
	_ = resp.Body.Close()

	u, err := url.Parse(ar.AuthURL)
	if err != nil {
		t.Fatalf("parse auth_url %q: %v", ar.AuthURL, err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in auth_url %q", ar.AuthURL)
	}

	cbURL := baseURL + "/auth/callback?code=testcode&state=" + url.QueryEscape(state)
	resp2, err := http.Get(cbURL)
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		_ = resp2.Body.Close()
		t.Fatalf("GET /auth/callback status = %d: %s", resp2.StatusCode, b)
	}
	var cr struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&cr); err != nil {
		_ = resp2.Body.Close()
		t.Fatalf("decode /auth/callback body: %v", err)
	}
	_ = resp2.Body.Close()
	if cr.Token == "" {
		t.Fatal("callback response has empty token")
	}
	return cr.Token
}

func getWithToken(t *testing.T, rawURL, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("X-Calgate-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	return resp
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status = %d, want %d — body: %s", resp.StatusCode, want, b)
	}
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// ── Auth tests ────────────────────────────────────────────────────────────────

func TestAuthBegin(t *testing.T) {
	env := newTestEnv(t)

	resp, err := http.Post(env.ts.URL+"/auth", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("auth_url not a valid URL: %v", err)
	}
	if u.Query().Get("state") == "" {
		t.Error("auth_url has no state param")
	}
}

func TestAuthCallback_FullFlow(t *testing.T) {
	env := newTestEnv(t)
	tok := mustAuth(t, env.ts.URL)
	if tok == "" {
		t.Fatal("empty token from auth flow")
	}
}

func TestAuthCallback_ReturnsToken(t *testing.T) {
	env := newTestEnv(t)

	resp, _ := http.Post(env.ts.URL+"/auth", "application/json", bytes.NewReader([]byte("{}")))
	var ar struct{ AuthURL string `json:"auth_url"` }
	decodeJSON(t, resp, &ar)

	u, _ := url.Parse(ar.AuthURL)
	state := u.Query().Get("state")

	resp2 := getWithToken(t, env.ts.URL+"/auth/callback?code=x&state="+url.QueryEscape(state), "")
	assertStatus(t, resp2, http.StatusOK)

	var cr struct{ Token string `json:"token"` }
	decodeJSON(t, resp2, &cr)
	if cr.Token == "" {
		t.Error("callback response missing token field")
	}
}

func TestAuthCallback_UnknownState(t *testing.T) {
	env := newTestEnv(t)
	resp := getWithToken(t, env.ts.URL+"/auth/callback?code=x&state=doesnotexist", "")
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusBadRequest)
}

func TestAuthCallback_StateConsumedOnUse(t *testing.T) {
	env := newTestEnv(t)

	resp, _ := http.Post(env.ts.URL+"/auth", "application/json", bytes.NewReader([]byte("{}")))
	var ar struct{ AuthURL string `json:"auth_url"` }
	decodeJSON(t, resp, &ar)

	u, _ := url.Parse(ar.AuthURL)
	state := u.Query().Get("state")
	cbURL := env.ts.URL + "/auth/callback?code=x&state=" + url.QueryEscape(state)

	// First use: should succeed.
	r1 := getWithToken(t, cbURL, "")
	assertStatus(t, r1, http.StatusOK)
	_, _ = io.Copy(io.Discard, r1.Body)
	_ = r1.Body.Close()

	// Second use of same state: must fail.
	r2 := getWithToken(t, cbURL, "")
	defer func() { _ = r2.Body.Close() }()
	assertStatus(t, r2, http.StatusBadRequest)
}

// ── Calendar tests ────────────────────────────────────────────────────────────

func TestListCalendars(t *testing.T) {
	env := newTestEnv(t)
	env.gw.calendars = []calgate.Calendar{
		{ID: "cal-1", Name: "Personal", Provider: calgate.ProviderOutlook},
		{ID: "cal-2", Name: "Work", Provider: calgate.ProviderOutlook},
	}
	tok := mustAuth(t, env.ts.URL)

	resp := getWithToken(t, env.ts.URL+"/calendars", tok)
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

func TestListCalendars_MissingToken(t *testing.T) {
	env := newTestEnv(t)
	resp := getWithToken(t, env.ts.URL+"/calendars", "")
	defer func() { _ = resp.Body.Close() }()
	// huma returns 400 for missing required header, or our 401 — either is correct
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected non-200 for missing token")
	}
}

func TestListCalendars_InvalidToken(t *testing.T) {
	env := newTestEnv(t)
	resp := getWithToken(t, env.ts.URL+"/calendars", "this-is-not-a-valid-token")
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestListCalendars_ProviderError(t *testing.T) {
	env := newTestEnv(t)
	env.gw.err = calgate.ErrProvider
	tok := mustAuth(t, env.ts.URL)

	resp := getWithToken(t, env.ts.URL+"/calendars", tok)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusBadGateway)
}

// ── Event tests ───────────────────────────────────────────────────────────────

func TestListEvents(t *testing.T) {
	env := newTestEnv(t)
	env.gw.events = []calgate.Event{
		{ID: "ev-1", Title: "Standup", Provider: calgate.ProviderOutlook},
	}
	tok := mustAuth(t, env.ts.URL)

	resp := getWithToken(t, env.ts.URL+"/calendars/cal-1/events", tok)
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
	tok := mustAuth(t, env.ts.URL)

	start := time.Now().UTC().Format(time.RFC3339)
	end := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	u := env.ts.URL + "/calendars/cal-1/events?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

	resp := getWithToken(t, u, tok)
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
	tok := mustAuth(t, env.ts.URL)
	resp := getWithToken(t, env.ts.URL+"/calendars/cal-1/events?start=not-a-date", tok)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestListEvents_InvalidEnd(t *testing.T) {
	env := newTestEnv(t)
	tok := mustAuth(t, env.ts.URL)
	resp := getWithToken(t, env.ts.URL+"/calendars/cal-1/events?end=garbage", tok)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestGetEvent(t *testing.T) {
	env := newTestEnv(t)
	env.gw.event = calgate.Event{ID: "ev-1", Title: "Standup", Provider: calgate.ProviderOutlook}
	tok := mustAuth(t, env.ts.URL)

	resp := getWithToken(t, env.ts.URL+"/calendars/cal-1/events/ev-1", tok)
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
	tok := mustAuth(t, env.ts.URL)
	resp := getWithToken(t, env.ts.URL+"/calendars/cal-1/events/missing", tok)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusNotFound)
}

func TestGetEvent_RateLimited(t *testing.T) {
	env := newTestEnv(t)
	env.gw.err = calgate.ErrRateLimited
	tok := mustAuth(t, env.ts.URL)
	resp := getWithToken(t, env.ts.URL+"/calendars/cal-1/events/ev-1", tok)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusTooManyRequests)
}

// ── Response shape contract ───────────────────────────────────────────────────

func TestResponseContentType(t *testing.T) {
	env := newTestEnv(t)
	env.gw.calendars = []calgate.Calendar{{ID: "c1", Name: "Cal"}}
	tok := mustAuth(t, env.ts.URL)

	resp := getWithToken(t, env.ts.URL+"/calendars", tok)
	assertStatus(t, resp, http.StatusOK)
	ct := resp.Header.Get("Content-Type")
	_ = resp.Body.Close()
	if ct == "" {
		t.Error("missing Content-Type header")
	}
}

func TestOpenAPISpec(t *testing.T) {
	env := newTestEnv(t)

	resp, err := http.Get(env.ts.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
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
		"/calendars",
		"/calendars/{calendarID}/events",
		"/calendars/{calendarID}/events/{eventID}",
	}
	for _, p := range required {
		if paths[p] == nil {
			t.Errorf("path %q missing from OpenAPI spec", p)
		}
	}
}
