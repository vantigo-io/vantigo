package energy_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The settlements endpoint's tests (energy settlements plan reading 12):
// GET /api/v1/energy/metering-points/{id}/settlements writes nothing and
// answers every closed period of the last 24 months — stored or computed for
// the answer — newest first.

// settlementsViewPermissions is what the endpoint needs.
var settlementsViewPermissions = []string{"energy:supply-periods-view", "energy:consumption-view"}

func getSettlements(t *testing.T, f stlFixture, point int32) []map[string]any {
	t.Helper()
	r := f.h.SignIn(t, settlementsViewPermissions...).Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/settlements", point), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET settlements: status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(r.Body, &rows); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
	return rows
}

// TestGetSettlements_AnUnknownPointIs404AndAPointWithNoSupplyIsEmpty.
func TestGetSettlements_AnUnknownPointIs404AndAPointWithNoSupplyIsEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	if r := c.Do(http.MethodGet, "/api/v1/energy/metering-points/999999/settlements", nil); r.Status != http.StatusNotFound {
		t.Errorf("unknown point: status %d body %s, want 404", r.Status, r.Body)
	}
	point := createMeteringPoint(t, c)
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/settlements", point.Id), nil)
	if r.Status != http.StatusOK || string(r.Body) != "[]\n" {
		t.Errorf("a point with no supply period: status %d body %q, want 200 []", r.Status, r.Body)
	}
}

// TestGetSettlements_ComputesWhatIsNotStoredAndWritesNothing: with nothing
// settled, July and August are computed for the answer — August first,
// blocked no_consumption, July 930.00 — with no id and no revision, and no
// row is written.
func TestGetSettlements_ComputesWhatIsNotStoredAndWritesNothing(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	rows := getSettlements(t, f, f.point)
	if len(rows) != 2 {
		t.Fatalf("answered %d settlements, want August and July: %v", len(rows), rows)
	}
	august, july := rows[0], rows[1]
	for _, row := range rows {
		for _, absent := range []string{"id", "revision", "invoice", "correctsId"} {
			if _, ok := row[absent]; ok {
				t.Errorf("a computed settlement carries %q: %v", absent, row)
			}
		}
		if row["state"] != contracts.SettlementDraft || row["kind"] != contracts.SettlementRegular {
			t.Errorf("state %v kind %v, want a draft regular", row["state"], row["kind"])
		}
	}
	if !stlSameInstant(august["periodStart"], stlAugStart) || august["blocked"] != contracts.SettlementNoConsumption {
		t.Errorf("first row = %v, want August blocked no_consumption", august)
	}
	if !stlSameInstant(july["periodStart"], stlJulyStart) || july["amount"] != 930.0 || july["kwh"] != 744.0 {
		t.Errorf("second row = %v, want July at 744 kWh and 930 NOK", july)
	}
	if _, ok := july["blocked"]; ok {
		t.Errorf("July carries a blocker: %v", july)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements`); n != 0 {
		t.Errorf("the GET wrote %d settlements, want none", n)
	}
}

// TestGetSettlements_AnswersStoredAndInvoicedRows: July settled and stamped
// is answered with its id, revision and invoice; August, never settled, is
// still computed for the answer.
func TestGetSettlements_AnswersStoredAndInvoicedRows(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	july := only(t, f.settle(t, stlAugStart))
	f.stamp(t, july)

	rows := getSettlements(t, f, f.point)
	if len(rows) != 2 {
		t.Fatalf("answered %d settlements, want August and July: %v", len(rows), rows)
	}
	if _, ok := rows[0]["id"]; ok {
		t.Errorf("August carries an id: %v", rows[0])
	}
	got := rows[1]
	if got["id"] != float64(july.ID) || got["revision"] != float64(july.Revision+1) || got["state"] != contracts.SettlementInvoiced {
		t.Errorf("July = %v, want id %d at revision %d, invoiced", got, july.ID, july.Revision+1)
	}
	invoice, ok := got["invoice"].(map[string]any)
	if !ok || invoice["id"] != 7001.0 || invoice["number"] != 10001.0 || invoice["issueDate"] != "2026-09-14" ||
		!stlSameInstant(invoice["invoicedAt"], stlIssuedAt) {
		t.Errorf("July's invoice = %v, want 7001 / 10001 issued 2026-09-14 at %v", got["invoice"], stlIssuedAt)
	}
	if _, ok := invoice["releasedAt"]; ok {
		t.Errorf("an invoiced settlement carries releasedAt: %v", invoice)
	}
	lines, _ := got["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("July's lines = %v, want the fixed-energy line", got["lines"])
	}
	line := lines[0].(map[string]any)
	if line["kind"] != "fixed_energy" || line["quantity"] != 744.0 || line["unitPrice"] != 1.25 || line["amount"] != 930.0 || line["unit"] != "kWh" {
		t.Errorf("July's line = %v, want 744 kWh of fixed_energy at 1.25, 930", line)
	}
	for _, absent := range []string{"band", "month"} {
		if _, ok := line[absent]; ok {
			t.Errorf("a fixed-energy line carries %q: %v", absent, line)
		}
	}
}

// stlSameInstant is whether a JSON timestamp is the instant want.
func stlSameInstant(got any, want time.Time) bool {
	text, ok := got.(string)
	if !ok {
		return false
	}
	at, err := time.Parse(time.RFC3339, text)
	return err == nil && at.Equal(want)
}
