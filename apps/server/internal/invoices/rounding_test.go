package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
)

// Small differences (invoices phase 6 design I15): a payment that leaves an
// invoice's principal open by more than 0 and at most the tolerance writes a
// rounding write-off of the rest, source auto, by no one, in its own
// transaction; a standing rounding write-off reads paid, never written off;
// and money after it is a late residual, queued invoice_settled.

// setTolerance stores the rounding tolerance straight in the schema.
func setTolerance(t *testing.T, h *harness, nok string) {
	t.Helper()
	h.Exec(t, `UPDATE invoices.settings SET tolerance = $1::numeric`, nok)
}

// writeOffsOf is invoice id's write-offs, each "amount reason source user",
// the user "-" when none.
func writeOffsOf(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT concat_ws(' ', amount, reason, source, coalesce(registered_by_user_id::text, '-'))
		FROM invoices.write_offs WHERE invoice_id = $1 ORDER BY id`, id)
}

func TestRounding_TheToleranceByHand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	off, edge, above, exact := thousand(t, h), thousand(t, h), thousand(t, h), thousand(t, h)

	// At 0, the default, nothing is rounded off.
	if got := registered(t, h, off.ID, pay(998, "2026-09-12")); got.State != "partially_paid" {
		t.Errorf("998 of 1000 at tolerance 0 = %s, want partially_paid", got.State)
	}
	setTolerance(t, h, "5")
	registered(t, h, edge.ID, pay(995, "2026-09-12"))
	registered(t, h, above.ID, pay(994.99, "2026-09-12"))
	registered(t, h, exact.ID, pay(1000, "2026-09-12"))
	for _, c := range []struct {
		inv   invoiceJSON
		state string
		offs  []string
	}{
		{off, "partially_paid", nil},
		{edge, "paid", []string{"5.00 rounding auto -"}},
		{above, "partially_paid", nil},
		{exact, "paid", nil},
	} {
		if got := writeOffsOf(t, h, c.inv.ID); !slices.Equal(got, c.offs) {
			t.Errorf("invoice %d's write-offs = %v, want %v", c.inv.ID, got, c.offs)
		}
		var doc struct {
			State      string  `json:"state"`
			OpenAmount float64 `json:"openAmount"`
			WriteOffs  []struct {
				Source             string  `json:"source"`
				RegisteredByUserID *string `json:"registeredByUserId"`
			} `json:"writeOffs"`
		}
		payer(t, h).Do(http.MethodGet, invoicesPath+"/"+idKey(c.inv.ID), nil).JSON(&doc)
		if doc.State != c.state {
			t.Errorf("invoice %d = %s, want %s", c.inv.ID, doc.State, c.state)
		}
		if len(c.offs) == 1 && (len(doc.WriteOffs) != 1 || doc.WriteOffs[0].Source != "auto" || doc.WriteOffs[0].RegisteredByUserID != nil || doc.OpenAmount != 0) {
			t.Errorf("invoice %d's write-off on the wire = %+v open %v, want auto, no user, nothing open", c.inv.ID, doc.WriteOffs, doc.OpenAmount)
		}
	}
	// The list's state filter reads it paid too.
	if got := ids(list(t, h, "?state=paid")); !slices.Contains(got, edge.ID) {
		t.Errorf("?state=paid = %v, want the rounded-off invoice", got)
	}
	if got := ids(list(t, h, "?state=written_off")); len(got) != 0 {
		t.Errorf("?state=written_off = %v, want none", got)
	}
}

// A rounding write-off made by hand reads paid as well: a small difference
// is not a loss. A loss reads written off.
func TestRounding_AManualRoundingWriteOffReadsPaid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	small, lost := thousand(t, h), thousand(t, h)
	registered(t, h, small.ID, pay(990, "2026-09-12"))
	for inv, reason := range map[int64]string{small.ID: "rounding", lost.ID: "loss"} {
		if res := payer(t, h).Do(http.MethodPost, writeOffPath(inv), map[string]any{"writtenOffOn": "2026-09-12", "reason": reason}); res.Status != http.StatusOK {
			t.Fatalf("write off %d = %d %s", inv, res.Status, res.Body)
		}
	}
	if got := getInvoice(t, h, small.ID).State; got != "paid" {
		t.Errorf("an invoice a manual rounding write-off closed = %s, want paid", got)
	}
	if got := getInvoice(t, h, lost.ID).State; got != "written_off" {
		t.Errorf("an invoice written off as a loss = %s, want written_off", got)
	}
	if got := writeOffsOf(t, h, small.ID); len(got) != 1 || !strings.HasPrefix(got[0], "10.00 rounding manual ") || strings.HasSuffix(got[0], " -") {
		t.Errorf("the manual write-off = %v, want source manual with its user", got)
	}
}

// A bank line rounds off what it leaves; money for a rounded-off invoice is
// a late residual, queued invoice_settled, never invoice_written_off.
func TestRounding_FromABankFileAndTheLateResidual(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	short, later := kidInvoice(t, h), kidInvoice(t, h)
	setTolerance(t, h, "5")
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 997, *short.Kid, "1")))
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "matched -" {
		t.Fatalf("the short line = %s, want matched", got)
	}
	if got := writeOffsOf(t, h, short.ID); !slices.Equal(got, []string{"3.00 rounding auto -"}) {
		t.Errorf("the short invoice's write-offs = %v, want 3.00 rounding auto", got)
	}
	r = imported(t, importer(t, h), bankfiletest.OCR("2",
		kidPay(sellerAccount, 6, 3, *short.Kid, "2"),
		kidPay(sellerAccount, 6, 999, *later.Kid, "3")))
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "2")); got != "exception invoice_settled" {
		t.Errorf("the late residual = %s, want exception invoice_settled", got)
	}
	if got := writeOffsOf(t, h, later.ID); !slices.Equal(got, []string{"1.00 rounding auto -"}) {
		t.Errorf("the later invoice's write-offs = %v, want 1.00 rounding auto", got)
	}
}

// ck_write_offs_actor and ck_write_offs_auto: a manual write-off has its
// user, an automatic one none and only the rounding reason.
func TestRounding_TheActorConstraints(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	inv := thousand(t, h)
	for what, sql := range map[string]string{
		"manual without a user": `INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, source, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-09-12', 1, 'NOK', 'rounding', 'manual', NULL, now())`,
		"auto with a user": `INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, source, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-09-12', 1, 'NOK', 'rounding', 'auto', gen_random_uuid(), now())`,
		"auto as a loss": `INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, source, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-09-12', 1, 'NOK', 'loss', 'auto', NULL, now())`,
	} {
		_, err := h.Pool().Exec(context.Background(), sql, inv.ID)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23514" {
			t.Errorf("%s = %v, want a check violation", what, err)
		}
	}
	h.Exec(t, `INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, source, registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-09-12', 1, 'NOK', 'rounding', 'auto', NULL, now())`, inv.ID)
}

// PUT /settings carries the tolerance: 0 to 100 with two decimals, absent
// keeping what is stored.
func TestRounding_TheSetting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	body := completeSeller(1)
	body["tolerance"] = 12.5
	saved := saveSeller(t, h, body)
	var got struct {
		Tolerance float64 `json:"tolerance"`
		Revision  int32   `json:"revision"`
	}
	manager.Do(http.MethodGet, settingsPath, nil).JSON(&got)
	if got.Tolerance != 12.5 {
		t.Errorf("the tolerance = %v, want 12.5", got.Tolerance)
	}
	kept := saveSeller(t, h, completeSeller(saved.Revision))
	manager.Do(http.MethodGet, settingsPath, nil).JSON(&got)
	if got.Tolerance != 12.5 {
		t.Errorf("a PUT without it left %v, want 12.5 kept", got.Tolerance)
	}
	for _, v := range []any{100.01, -1, 1.234} {
		b := completeSeller(kept.Revision)
		b["tolerance"] = v
		if res := manager.Do(http.MethodPut, settingsPath, b); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["tolerance"]) == 0 {
			t.Errorf("tolerance %v = %d %s, want 400 on tolerance", v, res.Status, res.Body)
		}
	}
}
