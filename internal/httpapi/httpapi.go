// Package httpapi is the HTTP surface of theserver (D-020): the router, the
// health endpoint and the mount point of the device link. cmd/theserver only
// wires it to the store, the certificate and the lifecycle.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/version"
)

// A Pinger tells whether the database answers. *store.Store is one.
type Pinger interface {
	Ping(ctx context.Context) error
}

// New returns the router. deviceLink serves the device link at link.Path; it
// may be nil when the link is not wanted, as in tests of the health endpoint.
func New(db Pinger, deviceLink http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(db, logger))
	if deviceLink != nil {
		mux.Handle("GET "+link.Path, deviceLink)
	}
	return mux
}

// Health is the body of GET /healthz.
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	DB      string `json:"db"`
}

// healthz answers 200 while the database answers, and 503 when it does not,
// so that a monitor sees the difference.
func healthz(db Pinger, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := Health{Status: "ok", Version: version.Version, DB: "ok"}
		status := http.StatusOK
		if err := db.Ping(r.Context()); err != nil {
			logger.Error("health check could not reach the database", "error", err)
			body = Health{Status: "error", Version: version.Version, DB: "error"}
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}
}
