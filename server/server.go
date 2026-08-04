// Package server wraps calgate as a lightweight stateless HTTP microservice
// using chi and huma. Tokens are caller-managed: the auth callback returns an
// encoded token that the caller stores and sends with every subsequent request
// via the X-Calgate-Token header. If the provider refreshes the token during a
// request, the updated token is echoed in the same response header so the
// caller can update their stored copy.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/oauth2"

	"github.com/jamesjohnsdev/calgate"
)

// Config holds everything the server needs at construction time.
type Config struct {
	// Addr is the TCP address to listen on. Defaults to ":8080".
	Addr string

	Authenticator calgate.Authenticator

	// NewTokenSource wraps a raw token in an auto-refreshing oauth2.TokenSource.
	// Needed so the server can transparently refresh expired access tokens during
	// a request without requiring the caller to re-authorize.
	NewTokenSource func(ctx context.Context, tok *oauth2.Token) oauth2.TokenSource

	// NewGateway creates a provider Gateway backed by the given TokenSource.
	NewGateway func(ctx context.Context, ts oauth2.TokenSource) calgate.Gateway
}

// Server is the stateless HTTP microservice gateway over calgate.
type Server struct {
	cfg    Config
	router *chi.Mux
	api    huma.API

	mu      sync.Mutex
	pending map[string]struct{} // valid CSRF state tokens; cleared on callback
}

// New builds and wires the server. Call Run to start listening.
func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	api := humachi.New(r, huma.DefaultConfig("calgate", "0.1.0"))

	s := &Server{
		cfg:     cfg,
		router:  r,
		api:     api,
		pending: map[string]struct{}{},
	}
	s.register()
	return s
}

// Handler returns the underlying http.Handler, useful for embedding or testing.
func (s *Server) Handler() http.Handler { return s.router }

// Run starts the HTTP server and blocks until ctx is cancelled or a fatal
// error occurs. Returns http.ErrServerClosed on clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    s.cfg.Addr,
		Handler: s.router,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	return srv.ListenAndServe()
}

// tokenCapture is a per-request calgate.TokenStore whose only job is to catch
// any token that PersistingSource saves after a refresh. Load is never called
// by PersistingSource (it uses the seed directly); only Save is used.
type tokenCapture struct {
	mu     sync.Mutex
	latest *oauth2.Token
}

func (tc *tokenCapture) Load(_ context.Context, _ string) (*oauth2.Token, error) {
	return nil, calgate.ErrUnauthorized
}

func (tc *tokenCapture) Save(_ context.Context, _ string, t *oauth2.Token) error {
	tc.mu.Lock()
	tc.latest = t
	tc.mu.Unlock()
	return nil
}

// buildGateway decodes the token from the request header, wraps it in a
// PersistingSource so any mid-request refresh is captured, and returns the
// Gateway and the capture. Callers check capture after the request to know
// whether to echo an updated token to the caller.
func (s *Server) buildGateway(ctx context.Context, encoded string) (calgate.Gateway, *tokenCapture, error) {
	tok, err := decodeToken(encoded)
	if err != nil {
		return nil, nil, huma.NewError(http.StatusUnauthorized, "invalid or missing token; run POST /auth")
	}
	capture := &tokenCapture{}
	base := s.cfg.NewTokenSource(ctx, tok)
	src := calgate.PersistingSource(ctx, "req", base, tok, capture)
	return s.cfg.NewGateway(ctx, src), capture, nil
}

// capturedToken returns the base64-encoded refreshed token if PersistingSource
// saved a new one, or an empty string (no header to set) if not.
func capturedToken(c *tokenCapture) string {
	c.mu.Lock()
	tok := c.latest
	c.mu.Unlock()
	if tok == nil {
		return ""
	}
	enc, err := encodeToken(tok)
	if err != nil {
		return ""
	}
	return enc
}

func (s *Server) register() {
	// ── Auth ─────────────────────────────────────────────────────────────────

	type beginAuthOut struct {
		Body struct {
			AuthURL string `json:"auth_url" doc:"Open in a browser to authorize the account."`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "begin-auth",
		Method:      http.MethodPost,
		Path:        "/auth",
		Summary:     "Start the OAuth authorization flow",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, _ *struct{}) (*beginAuthOut, error) {
		state, err := randomState()
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "failed to generate state")
		}
		s.mu.Lock()
		s.pending[state] = struct{}{}
		s.mu.Unlock()

		out := &beginAuthOut{}
		out.Body.AuthURL = s.cfg.Authenticator.AuthURL(state)
		return out, nil
	})

	type callbackOut struct {
		Body struct {
			Token string `json:"token" doc:"Base64-encoded OAuth token. Store and send as X-Calgate-Token on subsequent requests. Treat as opaque — refresh token is embedded."`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "auth-callback",
		Method:      http.MethodGet,
		Path:        "/auth/callback",
		Summary:     "OAuth redirect target — exchanges code and returns encoded token",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *struct {
		Code  string `query:"code"`
		State string `query:"state"`
	}) (*callbackOut, error) {
		s.mu.Lock()
		_, ok := s.pending[in.State]
		if ok {
			delete(s.pending, in.State)
		}
		s.mu.Unlock()

		if !ok {
			return nil, huma.NewError(http.StatusBadRequest, "unknown or expired state; restart the auth flow")
		}
		tok, err := s.cfg.Authenticator.Exchange(ctx, in.Code)
		if err != nil {
			return nil, huma.NewError(http.StatusBadGateway, fmt.Sprintf("token exchange failed: %v", err))
		}
		enc, err := encodeToken(tok)
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "failed to encode token")
		}
		out := &callbackOut{}
		out.Body.Token = enc
		return out, nil
	})

	// ── Calendars ─────────────────────────────────────────────────────────────

	type listCalendarsOut struct {
		UpdatedToken string `header:"X-Calgate-Token" doc:"Present only when the token was refreshed mid-request. Store this value and use it for subsequent requests."`
		Body         struct {
			Calendars []calgate.Calendar `json:"calendars"`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-calendars",
		Method:      http.MethodGet,
		Path:        "/calendars",
		Summary:     "List calendars for the authenticated account",
		Tags:        []string{"calendars"},
	}, func(ctx context.Context, in *struct {
		Token string `header:"X-Calgate-Token" required:"true"`
	}) (*listCalendarsOut, error) {
		gw, capture, err := s.buildGateway(ctx, in.Token)
		if err != nil {
			return nil, err
		}
		cals, err := gw.ListCalendars(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		out := &listCalendarsOut{}
		out.Body.Calendars = cals
		out.UpdatedToken = capturedToken(capture)
		return out, nil
	})

	// ── Events ────────────────────────────────────────────────────────────────

	type listEventsOut struct {
		UpdatedToken string `header:"X-Calgate-Token"`
		Body         struct {
			Events []calgate.Event `json:"events"`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-events",
		Method:      http.MethodGet,
		Path:        "/calendars/{calendarID}/events",
		Summary:     "List events in a calendar",
		Tags:        []string{"events"},
	}, func(ctx context.Context, in *struct {
		Token      string `header:"X-Calgate-Token" required:"true"`
		CalendarID string `path:"calendarID"`
		Start      string `query:"start" doc:"Inclusive window start (RFC 3339). Omit for unbounded."`
		End        string `query:"end" doc:"Exclusive window end (RFC 3339). Omit for unbounded."`
	}) (*listEventsOut, error) {
		tr, err := parseTimeRange(in.Start, in.End)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, err.Error())
		}
		gw, capture, err := s.buildGateway(ctx, in.Token)
		if err != nil {
			return nil, err
		}
		events, err := gw.ListEvents(ctx, in.CalendarID, tr)
		if err != nil {
			return nil, mapErr(err)
		}
		out := &listEventsOut{}
		out.Body.Events = events
		out.UpdatedToken = capturedToken(capture)
		return out, nil
	})

	type getEventOut struct {
		UpdatedToken string `header:"X-Calgate-Token"`
		Body         calgate.Event
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "get-event",
		Method:      http.MethodGet,
		Path:        "/calendars/{calendarID}/events/{eventID}",
		Summary:     "Get a single calendar event",
		Tags:        []string{"events"},
	}, func(ctx context.Context, in *struct {
		Token      string `header:"X-Calgate-Token" required:"true"`
		CalendarID string `path:"calendarID"`
		EventID    string `path:"eventID"`
	}) (*getEventOut, error) {
		gw, capture, err := s.buildGateway(ctx, in.Token)
		if err != nil {
			return nil, err
		}
		event, err := gw.GetEvent(ctx, in.CalendarID, in.EventID)
		if err != nil {
			return nil, mapErr(err)
		}
		out := &getEventOut{Body: event}
		out.UpdatedToken = capturedToken(capture)
		return out, nil
	})
}

// mapErr converts calgate sentinel errors to huma HTTP errors.
func mapErr(err error) error {
	switch {
	case errors.Is(err, calgate.ErrUnauthorized):
		return huma.NewError(http.StatusUnauthorized, "not authorized; run POST /auth")
	case errors.Is(err, calgate.ErrNotFound):
		return huma.NewError(http.StatusNotFound, "not found")
	case errors.Is(err, calgate.ErrRateLimited):
		return huma.NewError(http.StatusTooManyRequests, "provider rate limit; retry after a moment")
	case errors.Is(err, calgate.ErrProvider):
		return huma.NewError(http.StatusBadGateway, "upstream provider error")
	default:
		return huma.NewError(http.StatusInternalServerError, "internal error")
	}
}

func parseTimeRange(start, end string) (calgate.TimeRange, error) {
	var tr calgate.TimeRange
	if start != "" {
		t, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return tr, fmt.Errorf("invalid start time: must be RFC 3339")
		}
		tr.Start = t
	}
	if end != "" {
		t, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return tr, fmt.Errorf("invalid end time: must be RFC 3339")
		}
		tr.End = t
	}
	return tr, nil
}

// encodeToken JSON-marshals tok and base64url-encodes the result.
// The encoded form is safe for use as a header value and is treated as opaque
// by callers — they store and return it verbatim.
func encodeToken(tok *oauth2.Token) (string, error) {
	b, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeToken reverses encodeToken.
func decodeToken(s string) (*oauth2.Token, error) {
	if s == "" {
		return nil, fmt.Errorf("empty token")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return &tok, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
