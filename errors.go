package calgate

import "errors"

// Sentinel errors implementations should return so callers (and the future
// HTTP service) can map to transport codes without inspecting strings.
var (
	// ErrUnauthorized: token missing, expired beyond refresh, or revoked.
	// Maps to HTTP 401. Caller should re-run the authorize flow.
	ErrUnauthorized = errors.New("calgate: unauthorized")

	// ErrNotFound: calendar or event does not exist. Maps to HTTP 404.
	ErrNotFound = errors.New("calgate: not found")

	// ErrRateLimited: provider throttled the request. Maps to HTTP 429.
	ErrRateLimited = errors.New("calgate: rate limited")

	// ErrProvider: unexpected upstream/provider failure. Maps to HTTP 502.
	ErrProvider = errors.New("calgate: provider error")
)
