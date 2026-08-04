// Package google implements calgate.Authenticator and calgate.Gateway against
// the Google Calendar v3 API.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/jamesjohnsdev/calgate"
	"golang.org/x/oauth2"
	goauth "golang.org/x/oauth2/google"
)

const calBase = "https://www.googleapis.com/calendar/v3"

// DefaultScopes requests read-only calendar access plus offline refresh tokens.
var DefaultScopes = []string{
	"https://www.googleapis.com/auth/calendar.readonly",
}

// Config holds the Google OAuth2 app credentials.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// Scopes overrides DefaultScopes when set.
	Scopes []string
}

// Client implements calgate.Authenticator and builds Gateways.
type Client struct {
	oauth   *oauth2.Config
	calBase string
}

// New builds a Client from app credentials.
func New(cfg Config) *Client {
	return NewWithBase(cfg, calBase)
}

// NewWithBase builds a Client with an overridable API base URL (for testing).
func NewWithBase(cfg Config, base string) *Client {
	scopes := cfg.Scopes
	if scopes == nil {
		scopes = DefaultScopes
	}
	return &Client{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
			Endpoint:     goauth.Endpoint,
		},
		calBase: base,
	}
}

// AuthURL returns the Google consent URL. state must be validated on callback.
func (c *Client) AuthURL(state string) string {
	return c.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// Exchange trades an authorization code for a token (incl. refresh token).
func (c *Client) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	t, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("google: exchange: %w", err)
	}
	return t, nil
}

// TokenSource returns an auto-refreshing token source for an existing token.
func (c *Client) TokenSource(ctx context.Context, tok *oauth2.Token) oauth2.TokenSource {
	return c.oauth.TokenSource(ctx, tok)
}

// Gateway returns a read gateway for an authorized account.
func (c *Client) Gateway(ctx context.Context, ts oauth2.TokenSource) calgate.Gateway {
	return &gateway{http: oauth2.NewClient(ctx, ts), base: c.calBase}
}

type gateway struct {
	http *http.Client
	base string
}

func (g *gateway) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", calgate.ErrProvider, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return calgate.ErrUnauthorized
		case http.StatusNotFound:
			return calgate.ErrNotFound
		case http.StatusTooManyRequests:
			return calgate.ErrRateLimited
		default:
			return fmt.Errorf("%w: google %d: %s", calgate.ErrProvider, resp.StatusCode, body)
		}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (g *gateway) ListCalendars(ctx context.Context) ([]calgate.Calendar, error) {
	type page struct {
		Items         []gCalendar `json:"items"`
		NextPageToken string      `json:"nextPageToken"`
	}

	var out []calgate.Calendar
	pageToken := ""
	for {
		q := url.Values{}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		rawURL := g.base + "/users/me/calendarList"
		if len(q) > 0 {
			rawURL += "?" + q.Encode()
		}

		var p page
		if err := g.get(ctx, rawURL, &p); err != nil {
			return nil, err
		}
		for _, c := range p.Items {
			out = append(out, c.toCalendar())
		}
		if p.NextPageToken == "" {
			break
		}
		pageToken = p.NextPageToken
	}
	return out, nil
}

func (g *gateway) ListEvents(ctx context.Context, calendarID string, r calgate.TimeRange) ([]calgate.Event, error) {
	type page struct {
		Items         []gEvent `json:"items"`
		NextPageToken string   `json:"nextPageToken"`
	}

	var out []calgate.Event
	pageToken := ""
	hasRange := !r.Start.IsZero() || !r.End.IsZero()

	for {
		q := url.Values{}
		if hasRange {
			q.Set("singleEvents", "true")
			if !r.Start.IsZero() {
				q.Set("timeMin", r.Start.UTC().Format("2006-01-02T15:04:05Z"))
			}
			if !r.End.IsZero() {
				q.Set("timeMax", r.End.UTC().Format("2006-01-02T15:04:05Z"))
			}
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		rawURL := g.base + "/calendars/" + url.PathEscape(calendarID) + "/events"
		if len(q) > 0 {
			rawURL += "?" + q.Encode()
		}

		var p page
		if err := g.get(ctx, rawURL, &p); err != nil {
			return nil, err
		}
		for _, e := range p.Items {
			out = append(out, e.toEvent())
		}
		if p.NextPageToken == "" {
			break
		}
		pageToken = p.NextPageToken
	}
	return out, nil
}

func (g *gateway) GetEvent(ctx context.Context, calendarID, eventID string) (calgate.Event, error) {
	rawURL := g.base + "/calendars/" + url.PathEscape(calendarID) + "/events/" + url.PathEscape(eventID)
	var e gEvent
	if err := g.get(ctx, rawURL, &e); err != nil {
		return calgate.Event{}, err
	}
	return e.toEvent(), nil
}
