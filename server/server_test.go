package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/jamesjohnsdev/calgate"
)

// ── Shared mocks ──────────────────────────────────────────────────────────────

type mockAuth struct {
	baseURL string
	tok     *oauth2.Token
	err     error
}

func (m *mockAuth) AuthURL(state string) string     { return m.baseURL + "?state=" + state }
func (m *mockAuth) Exchange(_ context.Context, _ string) (*oauth2.Token, error) {
	return m.tok, m.err
}

type mockStore struct {
	mu      sync.RWMutex
	tokens  map[string]*oauth2.Token
	saveErr error
}

func newMockStore() *mockStore { return &mockStore{tokens: map[string]*oauth2.Token{}} }

func (m *mockStore) Load(_ context.Context, userID string) (*oauth2.Token, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tokens[userID]
	if !ok {
		return nil, calgate.ErrUnauthorized
	}
	return t, nil
}

func (m *mockStore) Save(_ context.Context, userID string, t *oauth2.Token) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[userID] = t
	return nil
}

type mockGW struct {
	calendars []calgate.Calendar
	events    []calgate.Event
	event     calgate.Event
	err       error
}

func (m *mockGW) ListCalendars(_ context.Context) ([]calgate.Calendar, error) {
	return m.calendars, m.err
}
func (m *mockGW) ListEvents(_ context.Context, _ string, _ calgate.TimeRange) ([]calgate.Event, error) {
	return m.events, m.err
}
func (m *mockGW) GetEvent(_ context.Context, _, _ string) (calgate.Event, error) {
	return m.event, m.err
}

// statusCoder matches huma's StatusError interface without importing huma.
type statusCoder interface{ GetStatus() int }

// ── mapErr ────────────────────────────────────────────────────────────────────

func TestMapErr(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{calgate.ErrUnauthorized, 401},
		{calgate.ErrNotFound, 404},
		{calgate.ErrRateLimited, 429},
		{calgate.ErrProvider, 502},
		{fmt.Errorf("wrapped: %w", calgate.ErrNotFound), 404},
		{errors.New("unrecognised"), 500},
	}
	for _, tc := range cases {
		got := mapErr(tc.err)
		sc, ok := got.(statusCoder)
		if !ok {
			t.Fatalf("mapErr(%v) returned %T, want statusCoder", tc.err, got)
		}
		if sc.GetStatus() != tc.want {
			t.Errorf("mapErr(%v) = %d, want %d", tc.err, sc.GetStatus(), tc.want)
		}
	}
}

// ── parseTimeRange ────────────────────────────────────────────────────────────

func TestParseTimeRange(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rfc := now.Format(time.RFC3339)

	cases := []struct {
		name    string
		start   string
		end     string
		wantErr bool
		check   func(t *testing.T, tr calgate.TimeRange)
	}{
		{
			name:  "both empty yields zero range",
			check: func(t *testing.T, tr calgate.TimeRange) {
				if !tr.Start.IsZero() || !tr.End.IsZero() {
					t.Error("expected zero TimeRange")
				}
			},
		},
		{
			name:  "valid start only",
			start: rfc,
			check: func(t *testing.T, tr calgate.TimeRange) {
				if !tr.Start.Equal(now) {
					t.Errorf("start = %v, want %v", tr.Start, now)
				}
				if !tr.End.IsZero() {
					t.Error("end should be zero")
				}
			},
		},
		{
			name: "valid end only",
			end:  rfc,
			check: func(t *testing.T, tr calgate.TimeRange) {
				if !tr.End.Equal(now) {
					t.Errorf("end = %v, want %v", tr.End, now)
				}
				if !tr.Start.IsZero() {
					t.Error("start should be zero")
				}
			},
		},
		{
			name:  "both valid",
			start: rfc,
			end:   rfc,
			check: func(t *testing.T, tr calgate.TimeRange) {
				if !tr.Start.Equal(now) || !tr.End.Equal(now) {
					t.Error("unexpected time values")
				}
			},
		},
		{name: "invalid start", start: "not-a-date", wantErr: true},
		{name: "invalid end", end: "not-a-date", wantErr: true},
		{name: "invalid month", start: "2025-13-01T00:00:00Z", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := parseTimeRange(tc.start, tc.end)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, tr)
			}
		})
	}
}

// ── randomState ───────────────────────────────────────────────────────────────

func TestRandomState(t *testing.T) {
	s, err := randomState()
	if err != nil {
		t.Fatalf("randomState() error: %v", err)
	}
	if len(s) != 32 {
		t.Errorf("len = %d, want 32 (16 bytes hex-encoded)", len(s))
	}
	const hexChars = "0123456789abcdef"
	for _, c := range s {
		if !strings.ContainsRune(hexChars, c) {
			t.Errorf("non-hex character %q in state", c)
			break
		}
	}

	s2, err := randomState()
	if err != nil {
		t.Fatal(err)
	}
	if s == s2 {
		t.Error("two successive states must not collide")
	}
}
