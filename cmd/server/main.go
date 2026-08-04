// Command server runs calgate as an HTTP microservice.
// Graph. Configure via environment variables:
//
//	CALGATE_CLIENT_ID      Azure AD app client ID           (required)
//	CALGATE_CLIENT_SECRET  Azure AD app client secret       (required)
//	CALGATE_REDIRECT_URL   OAuth redirect URL               (required)
//	CALGATE_TENANT         Azure AD tenant, default "common" (optional)
//	CALGATE_ADDR           Listen address, default ":8080"  (optional)
//
// Token storage is in-memory only. For production replace calgate.NewMemoryStore
// with a durable, encrypted implementation.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jamesjohnsdev/calgate"
	"github.com/jamesjohnsdev/calgate/outlook"
	"github.com/jamesjohnsdev/calgate/server"
)

func main() {
	clientID := mustEnv("CALGATE_CLIENT_ID")
	clientSecret := mustEnv("CALGATE_CLIENT_SECRET")
	redirectURL := mustEnv("CALGATE_REDIRECT_URL")

	tenant := os.Getenv("CALGATE_TENANT")
	addr := os.Getenv("CALGATE_ADDR")

	client := outlook.New(outlook.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Tenant:       tenant,
	})
	store := calgate.NewMemoryStore()

	srv := server.New(server.Config{
		Addr:          addr,
		Authenticator: client,
		Store:         store,
		NewGateway: func(ctx context.Context, userID string) (calgate.Gateway, error) {
			src, err := client.SourceFor(ctx, userID, store)
			if err != nil {
				return nil, err
			}
			return client.Gateway(ctx, src), nil
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if addr == "" {
		addr = ":8080"
	}
	log.Printf("calgate server listening on %s", addr)

	if err := srv.Run(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
