# calgate

Light, provider-agnostic **cal**endar **gate**way in Go. Authorize an external
calendar account once, then read calendars and events as unified structs.
Outlook (Microsoft Graph) today; Google Calendar planned behind the same
interface.

It is a **gateway, not a sync engine**: it fetches provider data and hands back
neutral `Calendar` / `Event` objects (plus the original payload in `Raw`). What
you do with them — store, transform, sync — is up to you.

## Design

- **Library first.** Import `calgate` directly. A thin HTTP service (`server/`,
  `cmd/calgate-server/`) will wrap the same core later — the library never
  depends on it.
- **Auth once, read forever.** OAuth2 authorization-code flow yields a
  long-lived refresh token; a self-persisting token source refreshes access
  tokens silently and writes rotations back to your `TokenStore`.
- **Secrets stay out of the library.** You inject a `TokenStore` (encrypted
  Postgres / KeyVault). calgate holds no credentials at rest.

## Core types

| Type | Role |
|------|------|
| `Gateway` | Read: `ListCalendars`, `ListEvents`, `GetEvent` |
| `Authenticator` | One-time OAuth: `AuthURL`, `Exchange` |
| `TokenStore` | Caller-provided token persistence (`Load` / `Save`) |
| `PersistingSource` | Auto-refresh + write-back token source |
| `Event` / `Calendar` | Provider-neutral data, with `Raw` escape hatch |

## Flow

```go
client := outlook.New(outlook.Config{
    ClientID:     os.Getenv("MS_CLIENT_ID"),
    ClientSecret: os.Getenv("MS_CLIENT_SECRET"),
    RedirectURL:  "https://you.example.com/oauth/callback",
    Tenant:       "common",
})
store := calgate.NewMemoryStore() // swap for Postgres/KeyVault impl

// 1. one-time authorize — send user to the consent URL
url := client.AuthURL(state)

// 2. callback: exchange code, persist the token (incl. refresh token)
tok, _ := client.Exchange(ctx, code)
store.Save(ctx, userID, tok)

// 3. ongoing: build a self-refreshing gateway for the user
ts, _ := client.SourceFor(ctx, userID, store)
gw := client.Gateway(ctx, ts)
cals, _ := gw.ListCalendars(ctx)
evts, _ := gw.ListEvents(ctx, cals[0].ID, calgate.TimeRange{Start: from, End: to})
```

## Provider setup notes

- **Microsoft:** register an Azure AD app; add the `RedirectURL`; grant
  `Calendars.Read` + `offline_access`. Refresh tokens **rotate on every
  refresh** — `PersistingSource` handles the write-back; do not bypass it.
- **Google (future):** send `access_type=offline` + `prompt=consent` or a
  refresh token is only issued on first consent.

## Status

- [x] Library core + unified types
- [x] Outlook read gateway (raw Graph HTTP)
- [x] Persisting OAuth token source
- [ ] Google Calendar impl
- [ ] HTTP service wrapper
