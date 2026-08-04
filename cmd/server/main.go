// Command server runs calgate as a stateless HTTP microservice.
// Configure via environment variables:
//
//	CALGATE_PROVIDER       Calendar provider: "outlook" (default) or "google"
//	CALGATE_CLIENT_ID      OAuth app client ID                      (required)
//	CALGATE_CLIENT_SECRET  OAuth app client secret                  (required)
//	CALGATE_REDIRECT_URL   OAuth redirect URL                       (required)
//	CALGATE_TENANT         Azure AD tenant, default "common"        (outlook only)
//	CALGATE_ADDR           Listen address, default ":8080"          (optional)
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
	"github.com/jamesjohnsdev/calgate/google"
	"github.com/jamesjohnsdev/calgate/outlook"
	"github.com/jamesjohnsdev/calgate/server"
)

func main() {
	provider := os.Getenv("CALGATE_PROVIDER")
	if provider == "" {
		provider = "outlook"
	}

	clientID := mustEnv("CALGATE_CLIENT_ID")
	clientSecret := mustEnv("CALGATE_CLIENT_SECRET")
	redirectURL := mustEnv("CALGATE_REDIRECT_URL")
	addr := os.Getenv("CALGATE_ADDR")

	var cfg server.Config
	cfg.Addr = addr

	switch provider {
	case "google":
		client := google.New(google.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
		})
		cfg.Authenticator = client
		cfg.NewTokenSource = client.TokenSource
		cfg.NewGateway = func(ctx context.Context, ts oauth2.TokenSource) calgate.Gateway {
			return client.Gateway(ctx, ts)
		}
	case "outlook":
		tenant := os.Getenv("CALGATE_TENANT")
		client := outlook.New(outlook.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Tenant:       tenant,
		})
		cfg.Authenticator = client
		cfg.NewTokenSource = client.TokenSource
		cfg.NewGateway = func(ctx context.Context, ts oauth2.TokenSource) calgate.Gateway {
			return client.Gateway(ctx, ts)
		}
	default:
		log.Fatalf("unknown CALGATE_PROVIDER %q; must be outlook or google", provider)
	}

	srv := server.New(cfg)

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
