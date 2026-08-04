// Package server wraps calgate as a lightweight HTTP microservice using chi
// and huma. It exposes the auth flow and read endpoints over a REST/JSON API
// and generates an OpenAPI spec automatically.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jamesjohnsdev/calgate"
)

// GatewayFactory builds a Gateway for the given user from their stored token.
// Returns calgate.ErrUnauthorized when the user has not yet authorized.
type GatewayFactory func(ctx context.Context, userID string) (calgate.Gateway, error)

// Config holds everything the server needs at construction time.
type Config struct {
	// Addr is the TCP address to listen on. Defaults to ":8080".
	Addr          string
	Authenticator calgate.Authenticator
	Store         calgate.TokenStore
	NewGateway    GatewayFactory
}

// Server is the HTTP microservice gateway over calgate.
type Server struct {
	cfg    Config
	router *chi.Mux
	api    huma.API

	mu      sync.Mutex
	pending map[string]string // state token → userID
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
		pending: map[string]string{},
	}
	s.register()
	return s
}

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

func (s *Server) register() {
	// ── Auth ─────────────────────────────────────────────────────────────────

	type beginAuthBody struct {
		UserID string `json:"user_id" doc:"Unique identifier for the account being authorized."`
	}
	type beginAuthOut struct {
		Body struct {
			AuthURL string `json:"auth_url" doc:"Open this URL in a browser to complete authorization."`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "begin-auth",
		Method:      http.MethodPost,
		Path:        "/auth",
		Summary:     "Start the OAuth authorization flow for a user",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *struct{ Body beginAuthBody }) (*beginAuthOut, error) {
		state, err := randomState()
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "failed to generate state token")
		}
		s.mu.Lock()
		s.pending[state] = in.Body.UserID
		s.mu.Unlock()

		out := &beginAuthOut{}
		out.Body.AuthURL = s.cfg.Authenticator.AuthURL(state)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID:   "auth-callback",
		Method:        http.MethodGet,
		Path:          "/auth/callback",
		Summary:       "OAuth redirect target — exchanges auth code for token",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		Code  string `query:"code" doc:"Authorization code from the provider."`
		State string `query:"state" doc:"State token echoed back by the provider."`
	}) (*struct{}, error) {
		s.mu.Lock()
		userID, ok := s.pending[in.State]
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
		if err := s.cfg.Store.Save(ctx, userID, tok); err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "failed to persist token")
		}
		return nil, nil
	})

	// ── Calendars ─────────────────────────────────────────────────────────────

	type listCalendarsOut struct {
		Body struct {
			Calendars []calgate.Calendar `json:"calendars"`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-calendars",
		Method:      http.MethodGet,
		Path:        "/users/{userID}/calendars",
		Summary:     "List calendars for a user",
		Tags:        []string{"calendars"},
	}, func(ctx context.Context, in *struct {
		UserID string `path:"userID"`
	}) (*listCalendarsOut, error) {
		gw, err := s.cfg.NewGateway(ctx, in.UserID)
		if err != nil {
			return nil, mapErr(err)
		}
		cals, err := gw.ListCalendars(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		out := &listCalendarsOut{}
		out.Body.Calendars = cals
		return out, nil
	})

	// ── Events ────────────────────────────────────────────────────────────────

	type listEventsOut struct {
		Body struct {
			Events []calgate.Event `json:"events"`
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-events",
		Method:      http.MethodGet,
		Path:        "/users/{userID}/calendars/{calendarID}/events",
		Summary:     "List events in a calendar",
		Tags:        []string{"events"},
	}, func(ctx context.Context, in *struct {
		UserID     string `path:"userID"`
		CalendarID string `path:"calendarID"`
		Start      string `query:"start" doc:"Inclusive window start in RFC 3339. Omit for unbounded."`
		End        string `query:"end" doc:"Exclusive window end in RFC 3339. Omit for unbounded."`
	}) (*listEventsOut, error) {
		tr, err := parseTimeRange(in.Start, in.End)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, err.Error())
		}
		gw, err := s.cfg.NewGateway(ctx, in.UserID)
		if err != nil {
			return nil, mapErr(err)
		}
		events, err := gw.ListEvents(ctx, in.CalendarID, tr)
		if err != nil {
			return nil, mapErr(err)
		}
		out := &listEventsOut{}
		out.Body.Events = events
		return out, nil
	})

	type getEventOut struct {
		Body calgate.Event
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "get-event",
		Method:      http.MethodGet,
		Path:        "/users/{userID}/calendars/{calendarID}/events/{eventID}",
		Summary:     "Get a single calendar event",
		Tags:        []string{"events"},
	}, func(ctx context.Context, in *struct {
		UserID     string `path:"userID"`
		CalendarID string `path:"calendarID"`
		EventID    string `path:"eventID"`
	}) (*getEventOut, error) {
		gw, err := s.cfg.NewGateway(ctx, in.UserID)
		if err != nil {
			return nil, mapErr(err)
		}
		event, err := gw.GetEvent(ctx, in.CalendarID, in.EventID)
		if err != nil {
			return nil, mapErr(err)
		}
		return &getEventOut{Body: event}, nil
	})
}

// mapErr converts calgate sentinel errors to huma HTTP errors.
func mapErr(err error) error {
	switch {
	case errors.Is(err, calgate.ErrUnauthorized):
		return huma.NewError(http.StatusUnauthorized, "not authorized; run POST /auth first")
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

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
