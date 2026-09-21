// Package management serves the private management listener a control plane
// polls (MANAGEMENT_PORT, MANAGEMENT_TOKEN). It is deliberately not part of
// the public handler: it has no host filter, so that it can be reached by
// Service name or pod address, and it is never routed from the internet. The
// bearer token is its only protection; publish the port nowhere. An empty
// configured token disables the endpoint — every request is then rejected —
// rather than leaving it open.
//
// It imports no module: what it reports arrives as function values.
package management

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Installation is identity's part of the status.
type Installation struct {
	Bootstrap   string
	Users       int
	ActiveUsers int
}

// Options are the collaborators the handler is assembled from.
type Options struct {
	Logger  *slog.Logger
	Version string
	Token   string

	Installation  func(context.Context) (Installation, error)
	DatabaseBytes func(context.Context) (int64, error)
}

type status struct {
	Version   string `json:"version"`
	Bootstrap string `json:"bootstrap"`
	Usage     usage  `json:"usage"`
}

type usage struct {
	Users         int   `json:"users"`
	ActiveUsers   int   `json:"activeUsers"`
	DatabaseBytes int64 `json:"databaseBytes"`
}

// Handler serves GET /management/status behind the bearer token.
func Handler(o Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /management/status", func(w http.ResponseWriter, r *http.Request) {
		inst, err := o.Installation(r.Context())
		if err != nil {
			o.Logger.ErrorContext(r.Context(), "management status failed", "error", err)
			httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "The status is not available.")
			return
		}
		size, err := o.DatabaseBytes(r.Context())
		if err != nil {
			o.Logger.ErrorContext(r.Context(), "management status failed", "error", err)
			httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "The status is not available.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(status{
			Version:   o.Version,
			Bootstrap: inst.Bootstrap,
			Usage:     usage{Users: inst.Users, ActiveUsers: inst.ActiveUsers, DatabaseBytes: size},
		})
	})

	return httpx.Chain(mux,
		httpx.RequestID,
		httpx.RequestLog(o.Logger, ""),
		httpx.Recover(o.Logger),
		bearer(o.Token),
	)
}

// bearer admits only "Authorization: Bearer <token>" for a non-empty,
// configured token. Digests are compared in constant time, so neither the
// token's length nor its contents leak, and RequestLog, which logs only the
// method, path, status and duration, never the header, keeps the token out
// of the log line too.
//
// An empty configured token and an empty presented credential are each
// rejected outright, before any digest comparison: without this, a caller
// presenting "Bearer " (the prefix with no credential) against an empty
// configured token would hash to the same sha256("") on both sides and
// authenticate. Config validation requires MANAGEMENT_TOKEN to be at least
// 32 characters, so an empty token should never reach here in practice — but
// this listener has no host filter, the token is its only protection, and it
// must fail closed on its own rather than lean on that upstream check.
func bearer(token string) func(http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented, ok := bearerToken(r.Header.Get("Authorization"))
			digest := sha256.Sum256([]byte(presented))
			authenticated := ok && token != "" && presented != "" &&
				subtle.ConstantTimeCompare(digest[:], expected[:]) == 1
			if !authenticated {
				w.Header().Set("WWW-Authenticate", "Bearer")
				httpx.WriteProblem(w, r, http.StatusUnauthorized, "A valid bearer token is required.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken reads header the way identity/scim.go's scimBearer does: RFC
// 7235 makes the auth scheme case-insensitive, so "Bearer " is matched with
// strings.EqualFold rather than a literal prefix, and what follows it is
// trimmed.
func bearerToken(header string) (string, bool) {
	if len(header) < len("Bearer ") || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return "", false
	}
	return strings.TrimSpace(header[len("Bearer "):]), true
}

// DatabaseBytes reports the size of the connected database. pg_database_size
// needs only CONNECT, which the least-privilege runtime role has.
func DatabaseBytes(pool *pgxpool.Pool) func(context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		var n int64
		err := pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&n)
		return n, err
	}
}
