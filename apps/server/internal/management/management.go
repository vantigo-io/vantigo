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
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/events"
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
	// Deliveries is the operator's view of the domain-events outbox
	// (events.Operator, orders design O4). Nil serves none of its routes.
	Deliveries Deliveries
	// Live is the live hub's state (live design L7, L16): its listener and
	// the notification queue's usage. Nil — a worker, or LIVE_ENABLED=0 —
	// leaves it out of the status.
	Live func(context.Context) Live
}

// Live is the live hub's part of the status.
type Live struct {
	// Listener is "up" or "down".
	Listener string `json:"listener"`
	// QueueUsage is pg_notification_queue_usage(), 0 to 1, or -1 when it
	// could not be read.
	QueueUsage float64 `json:"queueUsage"`
	// Connections is the sockets open on this replica.
	Connections int `json:"connections"`
}

// Deliveries is what the listener asks of the outbox: the stuck deliveries,
// and a retry or a skip of one (events.Operator).
type Deliveries interface {
	List(ctx context.Context, state string) ([]events.DeliveryRecord, error)
	Retry(ctx context.Context, id int64) error
	Skip(ctx context.Context, id int64, note string) error
}

type status struct {
	Version   string `json:"version"`
	Bootstrap string `json:"bootstrap"`
	Usage     usage  `json:"usage"`
	Live      *Live  `json:"live,omitempty"`
}

type usage struct {
	Users         int   `json:"users"`
	ActiveUsers   int   `json:"activeUsers"`
	DatabaseBytes int64 `json:"databaseBytes"`
}

// Handler serves GET /management/status — with Options.Live, the live hub's
// state in it — and, with Options.Deliveries, the
// outbox's deliveries (GET /management/events/deliveries and the retry and
// skip of one), behind the bearer token.
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
		body := status{
			Version:   o.Version,
			Bootstrap: inst.Bootstrap,
			Usage:     usage{Users: inst.Users, ActiveUsers: inst.ActiveUsers, DatabaseBytes: size},
		}
		if o.Live != nil {
			live := o.Live(r.Context())
			body.Live = &live
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(body)
	})

	if o.Deliveries != nil {
		mountDeliveries(mux, o)
	}

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

// delivery is one outbox delivery as the listener answers it.
type delivery struct {
	ID            int64     `json:"id"`
	EventID       int64     `json:"eventId"`
	Event         string    `json:"event"`
	Version       int       `json:"version"`
	Module        string    `json:"module"`
	Aggregate     string    `json:"aggregate"`
	AggregateID   string    `json:"aggregateId"`
	Handler       string    `json:"handler"`
	State         string    `json:"state"`
	Attempts      int       `json:"attempts"`
	LastError     *string   `json:"lastError"`
	Note          *string   `json:"note"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	OccurredAt    time.Time `json:"occurredAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// skipNoteLimit is platform.event_deliveries.note's length.
const skipNoteLimit = 500

// mountDeliveries serves the operator's three outbox calls (orders design
// O4): the stuck deliveries, at most events.ListLimit, the latest changed
// first; a retry, which resets a failed or dead delivery to pending, due
// now, keeping its attempts; and a skip, which marks a delivery not yet done
// as done with the operator's note. A retry or skip of a delivery in another
// state is 409; of no delivery, 404.
func mountDeliveries(mux *http.ServeMux, o Options) {
	mux.HandleFunc("GET /management/events/deliveries", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != "" && state != "dead" && state != "failed" {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "state is dead or failed.")
			return
		}
		records, err := o.Deliveries.List(r.Context(), state)
		if err != nil {
			o.Logger.ErrorContext(r.Context(), "management deliveries failed", "error", err)
			httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "The deliveries are not available.")
			return
		}
		out := make([]delivery, 0, len(records))
		for _, d := range records {
			out = append(out, delivery(d))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Deliveries []delivery `json:"deliveries"`
		}{out})
	})
	mux.HandleFunc("POST /management/events/deliveries/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		id, ok := deliveryID(w, r)
		if !ok {
			return
		}
		writeDeliveryOutcome(w, r, o, o.Deliveries.Retry(r.Context(), id))
	})
	mux.HandleFunc("POST /management/events/deliveries/{id}/skip", func(w http.ResponseWriter, r *http.Request) {
		id, ok := deliveryID(w, r)
		if !ok {
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "The body is JSON with a note.")
			return
		}
		note := strings.TrimSpace(body.Note)
		if note == "" || utf8.RuneCountInString(note) > skipNoteLimit {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "A note of at most 500 characters is required.")
			return
		}
		writeDeliveryOutcome(w, r, o, o.Deliveries.Skip(r.Context(), id, note))
	})
}

// deliveryID reads the path's delivery id, answering 404 for one that is no
// positive integer.
func deliveryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "No such delivery.")
		return 0, false
	}
	return id, true
}

func writeDeliveryOutcome(w http.ResponseWriter, r *http.Request, o Options, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, events.ErrDeliveryNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "No such delivery.")
	case errors.Is(err, events.ErrDeliveryState):
		httpx.WriteProblem(w, r, http.StatusConflict, "The delivery is not in a state that allows this.")
	default:
		o.Logger.ErrorContext(r.Context(), "management delivery change failed", "error", err)
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "The delivery could not be changed.")
	}
}
