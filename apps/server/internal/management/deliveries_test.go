package management_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/management"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func send(t *testing.T, h http.Handler, method, path, body, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestDeliveries_ListRetrySkip drives the three operator calls (orders
// design O4) against the real outbox tables.
func TestDeliveries_ListRetrySkip(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	at := time.Date(2026, time.November, 5, 10, 0, 0, 0, time.UTC)
	var eventID int64
	if err := pool.QueryRow(ctx, `INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
VALUES ('orders.sales_order.confirmed', 1, 'orders', 'orders.sales_order', '7', '{}', $1) RETURNING id`, at).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, row := range []struct{ handler, state string }{{"a.dead", "dead"}, {"a.failed", "failed"}, {"a.done", "done"}} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO platform.event_deliveries
(event_id, handler, aggregate, aggregate_id, state, attempts, last_error, next_attempt_at, created_at, updated_at)
VALUES ($1, $2, 'orders.sales_order', '7', $3, 8, 'boom', $4, $4, $4) RETURNING id`, eventID, row.handler, row.state, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[row.handler] = id
	}
	h := handler(management.Options{Deliveries: events.NewOperator(pool, func() time.Time { return at.Add(time.Hour) })})
	auth := "Bearer " + token

	res := send(t, h, http.MethodGet, "/management/events/deliveries?state=dead", "", auth)
	if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list: %d %s", res.Code, res.Body)
	}
	var body struct {
		Deliveries []struct {
			ID          int64   `json:"id"`
			EventID     int64   `json:"eventId"`
			Event       string  `json:"event"`
			Version     int     `json:"version"`
			Module      string  `json:"module"`
			Aggregate   string  `json:"aggregate"`
			AggregateID string  `json:"aggregateId"`
			Handler     string  `json:"handler"`
			State       string  `json:"state"`
			Attempts    int     `json:"attempts"`
			LastError   *string `json:"lastError"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Deliveries) != 1 {
		t.Fatalf("dead = %s, want one", res.Body)
	}
	if d := body.Deliveries[0]; d.ID != ids["a.dead"] || d.EventID != eventID || d.Event != "orders.sales_order.confirmed" ||
		d.Version != 1 || d.Module != "orders" || d.Aggregate != "orders.sales_order" || d.AggregateID != "7" || d.Handler != "a.dead" || d.State != "dead" ||
		d.Attempts != 8 || d.LastError == nil || *d.LastError != "boom" {
		t.Errorf("dead = %+v", d)
	}
	res = send(t, h, http.MethodGet, "/management/events/deliveries", "", auth)
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body.Deliveries) != 2 {
		t.Errorf("no state: %s, want dead and failed", res.Body)
	}
	if res := send(t, h, http.MethodGet, "/management/events/deliveries?state=done", "", auth); res.Code != http.StatusBadRequest {
		t.Errorf("state=done: %d, want 400", res.Code)
	}

	// Retry: pending, due now, attempts kept.
	if res := send(t, h, http.MethodPost, "/management/events/deliveries/"+itoa(ids["a.dead"])+"/retry", "", auth); res.Code != http.StatusNoContent {
		t.Fatalf("retry: %d %s", res.Code, res.Body)
	}
	var (
		state    string
		attempts int
		next     time.Time
	)
	if err := pool.QueryRow(ctx, `SELECT state, attempts, next_attempt_at FROM platform.event_deliveries WHERE id = $1`, ids["a.dead"]).
		Scan(&state, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || attempts != 8 || !next.Equal(at.Add(time.Hour)) {
		t.Errorf("after retry: %s, %d attempts, next %v", state, attempts, next)
	}
	if res := send(t, h, http.MethodPost, "/management/events/deliveries/"+itoa(ids["a.done"])+"/retry", "", auth); res.Code != http.StatusConflict {
		t.Errorf("retry of a done delivery: %d, want 409", res.Code)
	}
	if res := send(t, h, http.MethodPost, "/management/events/deliveries/999999/retry", "", auth); res.Code != http.StatusNotFound {
		t.Errorf("retry of no delivery: %d, want 404", res.Code)
	}
	if res := send(t, h, http.MethodPost, "/management/events/deliveries/x/retry", "", auth); res.Code != http.StatusNotFound {
		t.Errorf("retry of a malformed id: %d, want 404", res.Code)
	}

	// Skip: done with the note; a note is required.
	path := "/management/events/deliveries/" + itoa(ids["a.failed"]) + "/skip"
	for _, bad := range []string{"", "{}", `{"note":"  "}`, `{"note":"` + strings.Repeat("n", 501) + `"}`, "not json"} {
		if res := send(t, h, http.MethodPost, path, bad, auth); res.Code != http.StatusBadRequest {
			t.Errorf("skip with %.20q: %d, want 400", bad, res.Code)
		}
	}
	if res := send(t, h, http.MethodPost, path, `{"note":"handled by hand"}`, auth); res.Code != http.StatusNoContent {
		t.Fatalf("skip: %d %s", res.Code, res.Body)
	}
	var note *string
	if err := pool.QueryRow(ctx, `SELECT state, note FROM platform.event_deliveries WHERE id = $1`, ids["a.failed"]).Scan(&state, &note); err != nil {
		t.Fatal(err)
	}
	if state != "done" || note == nil || *note != "handled by hand" {
		t.Errorf("after skip: %s, %v", state, note)
	}
	if res := send(t, h, http.MethodPost, path, `{"note":"again"}`, auth); res.Code != http.StatusConflict {
		t.Errorf("a second skip: %d, want 409", res.Code)
	}
}

// TestDeliveries_RequireTheBearerToken: the outbox calls sit behind the
// listener's own authentication, as the status does.
func TestDeliveries_RequireTheBearerToken(t *testing.T) {
	t.Parallel()
	h := handler(management.Options{Deliveries: events.NewOperator(nil, nil)})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/management/events/deliveries"},
		{http.MethodPost, "/management/events/deliveries/1/retry"},
		{http.MethodPost, "/management/events/deliveries/1/skip"},
	} {
		if res := send(t, h, c.method, c.path, `{"note":"x"}`, "Bearer wrong"); res.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a wrong token: %d, want 401", c.method, c.path, res.Code)
		}
	}
}

// TestDeliveries_AbsentWithoutAnOutbox: no Deliveries, no routes.
func TestDeliveries_AbsentWithoutAnOutbox(t *testing.T) {
	t.Parallel()
	h := handler(management.Options{})
	if res := send(t, h, http.MethodGet, "/management/events/deliveries", "", "Bearer "+token); res.Code != http.StatusNotFound {
		t.Errorf("list without an outbox: %d, want 404", res.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
