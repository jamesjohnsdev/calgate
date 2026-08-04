// Command server runs calgate as a stateless HTTP microservice.
// Configure via environment variables:
//
//	CALGATE_CLIENT_ID      Azure AD app client ID            (required)
//	CALGATE_CLIENT_SECRET  Azure AD app client secret        (required)
//	CALGATE_REDIRECT_URL   OAuth redirect URL                (required)
//	CALGATE_TENANT         Azure AD tenant, default "common"  (optional)
//	CALGATE_ADDR           Listen address, default ":8080"   (optional)
//
// Tokens are caller-managed. POST /auth starts the flow; GET /auth/callback
// returns an encoded token the caller stores. Send it as X-Calgate-Token on
// every subsequent request. If the token is refreshed mid-request, the updated
// token is returned in the same header.
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

	"golang.org/x/oauth2"

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

	srv := server.New(server.Config{
		Addr:           addr,
		Authenticator:  client,
		NewTokenSource: client.TokenSource,
		NewGateway: func(ctx context.Context, ts oauth2.TokenSource) calgate.Gateway {
			return client.Gateway(ctx, ts)
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
