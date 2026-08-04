// Package calgate is a light, provider-agnostic gateway for reading calendar
// data from external providers (Outlook/Microsoft Graph now, Google later).
//
// It is a thin wrapper: it authorizes an account once, keeps access alive via
// refresh tokens, and returns calendar/event data as unified structs. It does
// not sync, reconcile, or persist calendar data itself — callers parse the
// unified objects (or the Raw payload) however they need.
//
// The package is transport-agnostic. It can be imported directly as a library,
// or wrapped by an HTTP service (see the future server/ package) without any
// change to the core.
package calgate

import (
	"context"
	"encoding/json"
	"time"

	"golang.org/x/oauth2"
)

// Provider identifies the upstream calendar provider a value came from.
type Provider string

const (
	ProviderOutlook Provider = "outlook"
	ProviderGoogle  Provider = "google"
)

// TimeRange bounds an event query. Zero values mean unbounded on that end.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Calendar is a provider-neutral calendar. Raw holds the original provider
// payload for fields this struct does not model.
type Calendar struct {
	ID       string
	Name     string
	Primary  bool
	Provider Provider
	Raw      json.RawMessage
}

// Event is a provider-neutral calendar event. It is a superset of the fields
// exposed by supported providers; provider-specific extras live in Raw.
type Event struct {
	ID          string
	Title       string
	Description string
	Start       time.Time
	End         time.Time
	AllDay      bool
	Location    string
	Organizer   string
	Attendees   []Attendee
	Recurrence  string // raw recurrence rule (RRULE-like); parse as needed
	Status      string // confirmed | tentative | cancelled
	Provider    Provider
	Raw         json.RawMessage
}

// Attendee is a provider-neutral event participant.
type Attendee struct {
	Name     string
	Email    string
	Response string // accepted | declined | tentative | none
}

// Gateway reads calendar data for a single authorized account. Implementations
// are provider-specific and constructed from an oauth2.TokenSource that yields
// valid access tokens (see TokenStore / persisting sources for staying alive).
type Gateway interface {
	ListCalendars(ctx context.Context) ([]Calendar, error)
	ListEvents(ctx context.Context, calendarID string, r TimeRange) ([]Event, error)
	GetEvent(ctx context.Context, calendarID, eventID string) (Event, error)
}

// Authenticator drives the one-time OAuth authorization-code flow. AuthURL is
// where the user consents; Exchange turns the returned code into a token that
// includes a long-lived refresh token. The HTTP redirect/callback endpoint is
// the caller's responsibility — this is a library, not a server.
type Authenticator interface {
	// AuthURL returns the provider consent URL. state must be a random,
	// per-request value the caller validates on callback (CSRF protection).
	AuthURL(state string) string
	// Exchange trades an authorization code for a token. One-time per account.
	Exchange(ctx context.Context, code string) (*oauth2.Token, error)
}

// TokenStore persists OAuth tokens between calls. Callers implement it against
// their own secret store (KeyVault, encrypted Postgres, etc.). Refresh tokens
// are long-lived credentials — encrypt at rest and never log them.
//
// Implementations must be safe for concurrent use: HTTP-service mode calls
// Load/Save from many goroutines.
type TokenStore interface {
	Load(ctx context.Context, userID string) (*oauth2.Token, error)
	Save(ctx context.Context, userID string, t *oauth2.Token) error
}
