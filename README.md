# calgate

Provider-agnostic calendar gateway in Go. Supports Outlook (Microsoft Graph) and Google Calendar behind a unified interface.

Fetches provider data and returns neutral `Calendar` / `Event` objects (plus the original payload in `Raw`). Not a sync engine.

## Use as a library

```go
import "github.com/jamesjohnsdev/calgate/google"
// or: "github.com/jamesjohnsdev/calgate/outlook"

client := google.New(google.Config{
    ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
    ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
    RedirectURL:  "https://you.example.com/oauth/callback",
})

// 1. Send user to consent URL (generate a random state for CSRF)
url := client.AuthURL(state)

// 2. On callback: exchange code for token
tok, _ := client.Exchange(ctx, code)

// 3. Build a gateway from a token source
ts := client.TokenSource(ctx, tok)
gw := client.Gateway(ctx, ts)

cals, _ := gw.ListCalendars(ctx)
evts, _ := gw.ListEvents(ctx, cals[0].ID, calgate.TimeRange{Start: from, End: to})
evt,  _ := gw.GetEvent(ctx, cals[0].ID, evts[0].ID)
```

For persistent token storage (refresh token write-back), inject a `calgate.TokenStore` and use `client.SourceFor(ctx, userID, store)` instead of `client.TokenSource`.

## Use as an HTTP service

Build and run `cmd/server`. Tokens are caller-managed: the service returns an encoded token on first auth; the caller sends it back as `X-Calgate-Token` on every request. If the token is refreshed mid-request, the updated token comes back in the same header.

```sh
CALGATE_PROVIDER=google \
CALGATE_CLIENT_ID=... \
CALGATE_CLIENT_SECRET=... \
CALGATE_REDIRECT_URL=https://you.example.com/callback \
CALGATE_ADDR=:8080 \
go run ./cmd/server
```

### Endpoints

| Method | Path                                 | Description                                    |
| ------ | ------------------------------------ | ---------------------------------------------- |
| `POST` | `/auth`                              | Returns `{"auth_url": "..."}` to redirect user |
| `GET`  | `/auth/callback?code=&state=`        | Exchanges code, returns `{"token": "..."}`     |
| `GET`  | `/calendars`                         | List calendars                                 |
| `GET`  | `/calendars/{id}/events?start=&end=` | List events (RFC3339 bounds, optional)         |
| `GET`  | `/calendars/{id}/events/{eventID}`   | Get single event                               |

All calendar/event endpoints require `X-Calgate-Token` header.

### Environment variables

| Variable                | Required | Default   | Notes                 |
| ----------------------- | -------- | --------- | --------------------- |
| `CALGATE_PROVIDER`      | no       | `outlook` | `outlook` or `google` |
| `CALGATE_CLIENT_ID`     | yes      |           |                       |
| `CALGATE_CLIENT_SECRET` | yes      |           |                       |
| `CALGATE_REDIRECT_URL`  | yes      |           |                       |
| `CALGATE_TENANT`        | no       | `common`  | Outlook only          |
| `CALGATE_ADDR`          | no       | `:8080`   |                       |

## Core types

| Type                 | Role                                                |
| -------------------- | --------------------------------------------------- |
| `Gateway`            | `ListCalendars`, `ListEvents`, `GetEvent`           |
| `Authenticator`      | `AuthURL`, `Exchange`                               |
| `TokenStore`         | Caller-provided token persistence (`Load` / `Save`) |
| `PersistingSource`   | Auto-refresh + write-back token source              |
| `Event` / `Calendar` | Provider-neutral data with `Raw` escape hatch       |

## Provider notes

**Outlook:** register an Azure AD app; grant `Calendars.Read` + `offline_access`. Refresh tokens rotate on every refresh; `PersistingSource` handles write-back.

**Google:** enable the Google Calendar API; set `RedirectURL` in the OAuth consent screen. `access_type=offline` is sent automatically so a refresh token is included.

## Status

- [x] Library core + unified types
- [x] Outlook read gateway
- [x] Google Calendar read gateway
- [x] Persisting OAuth token source
- [x] HTTP microservice (`server/`, `cmd/server`)
