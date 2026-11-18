package main

// The demo integration's request log (API access design K23, plan step B.5):
// the day before the seed's clock of "Webshop sync" calling Customers,
// Products and Orders, an even pace with a busier afternoon, a few calls
// refused over the rate limit, one refused for an operation its roles do
// not allow, and one server error, so the Requests tab has something to
// show. The rows name the integration's newest key, or, on a hosted demo,
// where the seed makes none, a key id that names no key, which the screen
// shows as a deleted key. A rerun, or an integration that has a log already,
// adds nothing.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
)

// demoRequestKey stands for the key a hosted demo's log names when the
// seed made none.
var demoRequestKey = uuid.MustParse("6b2f4c1e-9a7d-4e3b-8c51-0d2a7f3e9b14")

type demoRequest struct {
	method, operation string
	status            int
	reason            string
	ms                int
}

// demoRequestPattern is one hour's calls, repeated through the day.
var demoRequestPattern = []demoRequest{
	{"GET", "getCustomers", 200, "", 18},
	{"GET", "getProducts", 200, "", 24},
	{"GET", "getProducts", 200, "", 21},
	{"POST", "createSalesOrder", 201, "", 64},
	{"GET", "getSalesOrder", 200, "", 15},
	{"PUT", "putCustomersById", 200, "", 33},
}

func seedDemoRequestLog(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, now time.Time) error {
	seeded := 0
	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var integration uuid.UUID
		err := tx.QueryRow(ctx, `SELECT user_id FROM identity.integrations WHERE lower(name) = lower($1) AND archived_at IS NULL`,
			demoIntegrationName).Scan(&integration)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the demo integration: %w", err)
		}
		var logged bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity.api_requests WHERE integration_id = $1)`, integration).Scan(&logged); err != nil {
			return fmt.Errorf("read the demo integration's request log: %w", err)
		}
		if logged {
			return nil
		}
		key := demoRequestKey
		if err := tx.QueryRow(ctx, `SELECT id FROM identity.api_keys WHERE integration_id = $1 ORDER BY created_at DESC LIMIT 1`, integration).Scan(&key); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read the demo integration's key: %w", err)
		}

		start := now.Add(-24 * time.Hour).Truncate(time.Hour)
		var rows [][]any
		add := func(at time.Time, r demoRequest) {
			var reason *string
			if r.reason != "" {
				reason = &r.reason
			}
			trace := fmt.Sprintf("%032x", len(rows)+1)
			rows = append(rows, []any{at, key, integration, r.method, r.operation, int16(r.status), reason, int32(r.ms), "203.0.113.24", trace})
		}
		for hour := range 24 {
			calls := 1
			if hour >= 12 && hour < 17 {
				calls = 3 // the afternoon's orders
			}
			for c := range calls {
				for i, r := range demoRequestPattern {
					add(start.Add(time.Duration(hour)*time.Hour+time.Duration(c*20+i)*time.Minute), r)
				}
			}
		}
		peak := start.Add(14*time.Hour + 59*time.Minute)
		for i := range 4 {
			add(peak.Add(time.Duration(i)*time.Second), demoRequest{"GET", "getProducts", 429, "rate_limited", 1})
		}
		add(start.Add(9*time.Hour+30*time.Minute), demoRequest{"GET", "listCarrierAccounts", 403, "forbidden", 3})
		add(start.Add(20*time.Hour+5*time.Minute), demoRequest{"POST", "createSalesOrder", 500, "", 112})

		n, err := tx.CopyFrom(ctx, pgx.Identifier{"identity", "api_requests"},
			[]string{"at", "key_id", "integration_id", "method", "operation_id", "status", "reason", "duration_ms", "client_ip", "trace_id"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("seed the demo integration's request log: %w", err)
		}
		seeded = int(n)
		return nil
	})
	if err != nil {
		return err
	}
	if seeded > 0 {
		logger.Info("integration demo request log seeded", "integration", demoIntegrationName, "requests", seeded)
	}
	return nil
}
