package energy_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestTariffAssignment_RacesADeleteAndAnArchive pins energy tariffs plan
// reading 5 under concurrency: every write that assigns a tariff — a supply
// period's create, switch and terms, a metering point's create and replace —
// judges the tariff inside its own transaction under FOR SHARE, so a delete or
// an archive of the tariff in flight (both take FOR UPDATE) is waited for and
// seen. A racing delete answers the field's 400 "does not exist", not a 500 on
// the foreign key; a racing archive answers "is archived", not a 201 that
// assigns an archived tariff anew.
func TestTariffAssignment_RacesADeleteAndAnArchive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, tariffPermissions...)
	c := h.SignIn(t, allEnergyPermissions...)
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	type path struct {
		name   string
		kind   string // "power" or "grid"
		field  string
		assign func(t *testing.T, tariffID int32) func() *modtest.Response
	}
	periods := func(t *testing.T) string {
		return fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", createMeteringPoint(t, c).Id)
	}
	paths := []path{
		{"supply period create", "power", "tariffId", func(t *testing.T, id int32) func() *modtest.Response {
			p := periods(t)
			return func() *modtest.Response {
				return c.Do(http.MethodPost, p, map[string]any{"customerId": 1001, "start": start, "tariffId": id})
			}
		}},
		{"supply period switch", "power", "tariffId", func(t *testing.T, id int32) func() *modtest.Response {
			p := periods(t)
			return func() *modtest.Response {
				return c.Do(http.MethodPost, p+"/switch", map[string]any{"customerId": 1001, "switchAt": start, "tariffId": id})
			}
		}},
		{"supply period terms", "power", "tariffId", func(t *testing.T, id int32) func() *modtest.Response {
			p := periods(t)
			r := c.Do(http.MethodPost, p, map[string]any{"customerId": 1001, "start": start})
			if r.Status != http.StatusCreated {
				t.Fatalf("create period: status %d body %s", r.Status, r.Body)
			}
			var period supplyPeriodJSON
			r.JSON(&period)
			return func() *modtest.Response {
				return c.Do(http.MethodPut, fmt.Sprintf("%s/%d", p, period.Id), map[string]any{"tariffId": id})
			}
		}},
		{"metering point create", "grid", "gridTariffId", func(t *testing.T, id int32) func() *modtest.Response {
			body := withField(newMeteringPointBody(gsrn(t), "Race meter"), "gridTariffId", id)
			return func() *modtest.Response { return c.Do(http.MethodPost, "/api/v1/energy/metering-points", body) }
		}},
		{"metering point replace", "grid", "gridTariffId", func(t *testing.T, id int32) func() *modtest.Response {
			point := createMeteringPoint(t, c)
			body := withField(updateBody(point.Gsrn), "gridTariffId", id)
			return func() *modtest.Response {
				return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), body)
			}
		}},
	}
	races := []struct {
		name  string
		write string // the competing write, run after the tariff is locked FOR UPDATE
		msg   string // %d is the tariff's id
	}{
		{"a delete", `DELETE FROM energy.tariffs WHERE id = $1`, "Tariff %d does not exist."},
		{"an archive", `UPDATE energy.tariffs SET status = 'archived' WHERE id = $1`, "Tariff %d is archived."},
	}

	for _, race := range races {
		for _, p := range paths {
			t.Run(race.name+" against the "+p.name, func(t *testing.T) {
				body := powerTariffBody("Racy", "2026-01-01")
				if p.kind == "grid" {
					body = gridTariffBody("Racy", "2026-01-01")
				}
				tariff := createTariff(t, admin, body)
				assign := p.assign(t, tariff.Id)

				ctx := t.Context()
				tx, err := h.Pool().Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err := tx.Exec(ctx, `SELECT id FROM energy.tariffs WHERE id = $1 FOR UPDATE`, tariff.Id); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, race.write, tariff.Id); err != nil {
					t.Fatal(err)
				}

				done := make(chan *modtest.Response, 1)
				go func() { done <- assign() }()
				var r *modtest.Response
				waitForLockWait(t, h, done, &r)
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if r == nil {
					r = <-done
				}
				wantFieldError(t, r, p.field, fmt.Sprintf(race.msg, tariff.Id))
			})
		}
	}
}

// waitForLockWait returns once another backend of the test's database waits
// on a lock (the assignment blocked behind the competing write) or the
// assignment has already answered (then stored in *r) — the latter is what a
// write that does not wait on the tariff does. It gives up after five seconds.
func waitForLockWait(t *testing.T, h *modtest.Harness, done <-chan *modtest.Response, r **modtest.Response) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case *r = <-done:
			return
		default:
		}
		var waiting bool
		if err := h.Pool().QueryRow(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid())`,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
