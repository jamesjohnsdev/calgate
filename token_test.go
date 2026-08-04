package calgate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fixedSource returns the same token every call; safe for concurrent use.
type fixedSource struct {
	tok *oauth2.Token
	err error
	n   atomic.Int64
}

func (f *fixedSource) Token() (*oauth2.Token, error) {
	f.n.Add(1)
	return f.tok, f.err
}

// countingStore wraps a MemoryStore and counts Save calls.
type countingStore struct {
	*MemoryStore
	saves atomic.Int64
}

func (c *countingStore) Save(ctx context.Context, userID string, t *oauth2.Token) error {
	c.saves.Add(1)
	return c.MemoryStore.Save(ctx, userID, t)
}

func newCountingStore() *countingStore { return &countingStore{MemoryStore: NewMemoryStore()} }

func TestPersistingSource_SavesOnChange(t *testing.T) {
	store := newCountingStore()
	seed := &oauth2.Token{AccessToken: "old", RefreshToken: "r1"}
	base := &fixedSource{tok: &oauth2.Token{AccessToken: "new", RefreshToken: "r2"}}

	ps := &persistingSource{ctx: context.Background(), userID: "u", base: base, store: store, last: seed}

	got, err := ps.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.AccessToken != "new" {
		t.Fatalf("access = %q, want new", got.AccessToken)
	}
	if store.saves.Load() != 1 {
		t.Fatalf("saves = %d, want 1", store.saves.Load())
	}
	saved, _ := store.Load(context.Background(), "u")
	if saved.RefreshToken != "r2" {
		t.Fatalf("persisted refresh = %q, want r2 (rotation not written back)", saved.RefreshToken)
	}
}

func TestPersistingSource_NoSaveWhenUnchanged(t *testing.T) {
	store := newCountingStore()
	tok := &oauth2.Token{AccessToken: "same", RefreshToken: "r1"}
	base := &fixedSource{tok: tok}

	ps := &persistingSource{ctx: context.Background(), userID: "u", base: base, store: store, last: tok}

	if _, err := ps.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if store.saves.Load() != 0 {
		t.Fatalf("saves = %d, want 0 (nothing changed)", store.saves.Load())
	}
}

func TestPersistingSource_PropagatesError(t *testing.T) {
	store := newCountingStore()
	sentinel := errors.New("refresh failed")
	base := &fixedSource{err: sentinel}

	ps := &persistingSource{ctx: context.Background(), userID: "u", base: base, store: store, last: nil}

	if _, err := ps.Token(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if store.saves.Load() != 0 {
		t.Fatalf("saves = %d, want 0 on error", store.saves.Load())
	}
}

func TestPersistingSource_ConcurrentSingleSave(t *testing.T) {
	store := newCountingStore()
	seed := &oauth2.Token{AccessToken: "old"}
	base := &fixedSource{tok: &oauth2.Token{AccessToken: "new"}}
	ps := &persistingSource{ctx: context.Background(), userID: "u", base: base, store: store, last: seed}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ps.Token(); err != nil {
				t.Errorf("Token: %v", err)
			}
		}()
	}
	wg.Wait()

	// First goroutine to see the change saves; the rest observe last=="new".
	if store.saves.Load() != 1 {
		t.Fatalf("saves = %d, want exactly 1 under concurrency", store.saves.Load())
	}
}

func TestPersistingSource_EndToEndRefresh(t *testing.T) {
	// A seed that's already expired forces ReuseTokenSource to call through to
	// our persisting wrapper, exercising the full public path.
	store := newCountingStore()
	seed := &oauth2.Token{AccessToken: "old", Expiry: time.Now().Add(-time.Hour)}
	base := &fixedSource{tok: &oauth2.Token{
		AccessToken: "refreshed",
		Expiry:      time.Now().Add(time.Hour),
	}}

	src := PersistingSource(context.Background(), "u", base, seed, store)
	got, err := src.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.AccessToken != "refreshed" {
		t.Fatalf("access = %q, want refreshed", got.AccessToken)
	}
	if store.saves.Load() < 1 {
		t.Fatalf("expected a persisted refresh")
	}
}
