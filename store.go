package calgate

import (
	"context"
	"sync"

	"golang.org/x/oauth2"
)

// MemoryStore is a trivial in-memory TokenStore for tests and local runs.
// It is concurrency-safe but NOT durable — production callers implement
// TokenStore against encrypted Postgres / KeyVault instead.
type MemoryStore struct {
	mu     sync.RWMutex
	tokens map[string]*oauth2.Token
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tokens: map[string]*oauth2.Token{}}
}

func (m *MemoryStore) Load(_ context.Context, userID string) (*oauth2.Token, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tokens[userID]
	if !ok {
		return nil, ErrUnauthorized
	}
	return t, nil
}

func (m *MemoryStore) Save(_ context.Context, userID string, t *oauth2.Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[userID] = t
	return nil
}
