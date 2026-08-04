package calgate

import (
	"context"
	"errors"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

func TestMemoryStore_LoadMissing(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.Load(context.Background(), "nobody"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestMemoryStore_SaveLoadRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	want := &oauth2.Token{AccessToken: "a", RefreshToken: "r"}
	if err := s.Save(ctx, "u", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load(ctx, "u")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != "a" || got.RefreshToken != "r" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Save(ctx, "u", &oauth2.Token{AccessToken: "a"})
			_, _ = s.Load(ctx, "u")
			_ = i
		}()
	}
	wg.Wait()
}
