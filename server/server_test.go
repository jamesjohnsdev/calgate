package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/jamesjohnsdev/calgate"
)

// statusCoder matches huma's StatusError without importing huma.
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
			name: "both empty yields zero range",
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
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
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

// ── encodeToken / decodeToken ─────────────────────────────────────────────────

func TestEncodeDecodeToken_RoundTrip(t *testing.T) {
	want := &oauth2.Token{
		AccessToken:  "acc",
		RefreshToken: "ref",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Truncate(time.Second).Add(time.Hour),
	}
	enc, err := encodeToken(want)
	if err != nil {
		t.Fatalf("encodeToken: %v", err)
	}
	if enc == "" {
		t.Fatal("encoded token is empty")
	}
	got, err := decodeToken(enc)
	if err != nil {
		t.Fatalf("decodeToken: %v", err)
	}
	if got.AccessToken != want.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, want.RefreshToken)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Errorf("Expiry = %v, want %v", got.Expiry, want.Expiry)
	}
}

func TestDecodeToken_Invalid(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"not base64", "!!!"},
		{"valid base64 not json", "aGVsbG8="}, // "hello"
		{"truncated json", "ew"}, // base64url of "{" — incomplete JSON
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeToken(tc.in)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// ── tokenCapture ──────────────────────────────────────────────────────────────

func TestTokenCapture_Save(t *testing.T) {
	tc := &tokenCapture{}
	tok := &oauth2.Token{AccessToken: "new"}
	if err := tc.Save(context.Background(), "any", tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	tc.mu.Lock()
	got := tc.latest
	tc.mu.Unlock()
	if got != tok {
		t.Error("latest not set after Save")
	}
}

func TestTokenCapture_Load(t *testing.T) {
	tc := &tokenCapture{}
	_, err := tc.Load(context.Background(), "any")
	if !errors.Is(err, calgate.ErrUnauthorized) {
		t.Errorf("Load() error = %v, want ErrUnauthorized", err)
	}
}

func TestCapturedToken_NilWhenNoRefresh(t *testing.T) {
	tc := &tokenCapture{}
	if s := capturedToken(tc); s != "" {
		t.Errorf("capturedToken with no refresh = %q, want empty", s)
	}
}

func TestCapturedToken_SetWhenRefreshed(t *testing.T) {
	tc := &tokenCapture{}
	tok := &oauth2.Token{AccessToken: "refreshed", RefreshToken: "newref"}
	_ = tc.Save(context.Background(), "", tok)
	if s := capturedToken(tc); s == "" {
		t.Error("capturedToken should be non-empty after Save")
	}
}
