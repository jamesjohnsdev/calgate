// Package outlook implements calgate.Authenticator and calgate.Gateway against
// Microsoft Graph (Outlook / Office 365 calendars).
//
// It talks to Graph over plain HTTP using an oauth2 client — no generated SDK —
// to stay a light wrapper with full control of the unified mapping.
package outlook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jamesjohnsdev/calgate"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

const graphBase = "https://graph.microsoft.com/v1.0"

// DefaultScopes requests read-only calendar access plus a refresh token.
// offline_access is required for the refresh token that keeps sync alive.
var DefaultScopes = []string{
	"offline_access",
	"Calendars.Read",
}

// Config holds the Azure AD app-registration details.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// Tenant is the Azure AD tenant. Use "common" for multi-tenant + personal
	// accounts, "organizations" for work/school only, or a specific tenant ID.
	Tenant string
	// Scopes overrides DefaultScopes when set.
	Scopes []string
}

// Client implements calgate.Authenticator and builds Gateways for authorized
// accounts.
type Client struct {
	oauth *oauth2.Config
}

// New builds a Client from an app registration.
func New(cfg Config) *Client {
	tenant := cfg.Tenant
	if tenant == "" {
		tenant = "common"
	}
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
			Endpoint:     microsoft.AzureADEndpoint(tenant),
		},
	}
}

// AuthURL returns the Microsoft consent URL. state must be validated on callback.
func (c *Client) AuthURL(state string) string {
	return c.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// Exchange trades an authorization code for a token (incl. refresh token).
func (c *Client) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	t, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("outlook: exchange: %w", err)
	}
	return t, nil
}

// Gateway returns a read gateway for an account, given a token source that
// yields valid access tokens. Pair with calgate.PersistingSource to keep the
// stored refresh token fresh across refreshes.
func (c *Client) Gateway(ctx context.Context, ts oauth2.TokenSource) calgate.Gateway {
	return &gateway{http: oauth2.NewClient(ctx, ts), base: graphBase}
}

// SourceFor loads a user's stored token and wraps it in an auto-refreshing,
// self-persisting source. Convenience for the "already authorized" path.
func (c *Client) SourceFor(ctx context.Context, userID string, store calgate.TokenStore) (oauth2.TokenSource, error) {
	tok, err := store.Load(ctx, userID)
	if err != nil {
		return nil, err
	}
	base := c.oauth.TokenSource(ctx, tok)
	return calgate.PersistingSource(ctx, userID, base, tok, store), nil
}

type gateway struct {
	http *http.Client
	base string // Graph base URL; overridable in tests
}

func (g *gateway) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", calgate.ErrProvider, err)
	}
	defer resp.Body.Close()

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
			return fmt.Errorf("%w: graph %d: %s", calgate.ErrProvider, resp.StatusCode, body)
		}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (g *gateway) ListCalendars(ctx context.Context) ([]calgate.Calendar, error) {
	var out []calgate.Calendar
	path := "/me/calendars?$top=50"
	for path != "" {
		var page struct {
			Value    []graphCalendar `json:"value"`
			NextLink string          `json:"@odata.nextLink"`
		}
		if err := g.get(ctx, path, &page); err != nil {
			return nil, err
		}
		for _, c := range page.Value {
			out = append(out, c.toCalendar())
		}
		path = relLink(page.NextLink)
	}
	return out, nil
}

func (g *gateway) ListEvents(ctx context.Context, calendarID string, r calgate.TimeRange) ([]calgate.Event, error) {
	// calendarView expands recurrences and supports a time window.
	q := url.Values{}
	q.Set("$top", "50")
	if !r.Start.IsZero() {
		q.Set("startDateTime", r.Start.UTC().Format(time.RFC3339))
	}
	if !r.End.IsZero() {
		q.Set("endDateTime", r.End.UTC().Format(time.RFC3339))
	}
	endpoint := "events"
	// When a window is given, calendarView is the correct Graph endpoint.
	if q.Get("startDateTime") != "" && q.Get("endDateTime") != "" {
		endpoint = "calendarView"
	}
	path := fmt.Sprintf("/me/calendars/%s/%s?%s", url.PathEscape(calendarID), endpoint, q.Encode())

	var out []calgate.Event
	for path != "" {
		var page struct {
			Value    []graphEvent `json:"value"`
			NextLink string       `json:"@odata.nextLink"`
		}
		if err := g.get(ctx, path, &page); err != nil {
			return nil, err
		}
		for _, e := range page.Value {
			out = append(out, e.toEvent())
		}
		path = relLink(page.NextLink)
	}
	return out, nil
}

func (g *gateway) GetEvent(ctx context.Context, calendarID, eventID string) (calgate.Event, error) {
	var e graphEvent
	path := fmt.Sprintf("/me/calendars/%s/events/%s", url.PathEscape(calendarID), url.PathEscape(eventID))
	if err := g.get(ctx, path, &e); err != nil {
		return calgate.Event{}, err
	}
	return e.toEvent(), nil
}

// relLink turns an absolute Graph @odata.nextLink into a path we can re-request
// against the configured base (which already includes the /v1.0 prefix).
func relLink(next string) string {
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil {
		return ""
	}
	path := strings.TrimPrefix(u.Path, "/v1.0")
	if path == "" {
		return ""
	}
	if u.RawQuery != "" {
		return path + "?" + u.RawQuery
	}
	return path
}
