package calgate

import (
	"context"
	"sync"

	"golang.org/x/oauth2"
)

// PersistingSource wraps an auto-refreshing oauth2.TokenSource so that whenever
// the access token is refreshed, the new token is written back to the store.
//
// This is the mechanism behind "authorize once, sync forever": the base source
// silently exchanges the refresh token for new access tokens, and this wrapper
// captures the rotated token. Microsoft rotates the refresh token on every
// refresh. Without this, the stored refresh token goes stale and the account
// must re-authorize.
//
// The returned source is safe for concurrent use.
func PersistingSource(ctx context.Context, userID string, base oauth2.TokenSource, seed *oauth2.Token, store TokenStore) oauth2.TokenSource {
	ps := &persistingSource{
		ctx:    ctx,
		userID: userID,
		base:   base,
		store:  store,
		last:   seed,
	}
	// ReuseTokenSource caches the valid token and only calls ps.Token() when a
	// refresh is actually needed.
	return oauth2.ReuseTokenSource(seed, ps)
}

type persistingSource struct {
	ctx    context.Context
	userID string
	base   oauth2.TokenSource
	store  TokenStore

	mu   sync.Mutex
	last *oauth2.Token
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	t, err := p.base.Token()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Persist only when something actually changed (access or refresh token).
	if p.last == nil || t.AccessToken != p.last.AccessToken || t.RefreshToken != p.last.RefreshToken {
		if err := p.store.Save(p.ctx, p.userID, t); err != nil {
			return nil, err
		}
		p.last = t
	}
	return t, nil
}
