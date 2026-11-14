package db_test

import (
	"fmt"
	"testing"
)

// 00182_invoices_write_offs.sql (accounting design A16, R4): a write-off is
// immutable but for its undoing, at most one stands per invoice, it belongs
// to an issued invoice; a recovered payment names its write-off; each writes
// its facts at commit; and the open amount reads the write-off.
func TestInvoicesWriteOffs_TheRulesAndTheFacts(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	writeOff := func(id int64) (int64, error) {
		return f.insert(`INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-10-06', 100, 'NOK', 'loss', gen_random_uuid(), now()) RETURNING id`, id)
	}
	first, err := writeOff(invoice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeOff(invoice); err == nil {
		t.Error("a second standing write-off was let through")
	}
	if err := f.exec(`UPDATE invoices.write_offs SET amount = 1 WHERE id = $1`, first); err == nil {
		t.Error("a write-off was edited")
	}
	if err := f.exec(`DELETE FROM invoices.write_offs WHERE id = $1`, first); err == nil {
		t.Error("a write-off was deleted")
	}
	if err := f.exec(`INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, vat_correction, registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-10-06', 1, 'NOK', 'rounding', true, gen_random_uuid(), now())`, f.issued(8)); err == nil {
		t.Error("a rounding with the VAT correction was let through")
	}
	if err := f.exec(`INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at, kind)
		VALUES ($1, DATE '2026-10-07', 10, 'NOK', gen_random_uuid(), now(), 'recovered')`, invoice); err == nil {
		t.Error("a recovery naming no write-off was let through")
	}
	recovered, err := f.insert(`INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at, kind, write_off_id)
		VALUES ($1, DATE '2026-10-07', 10, 'NOK', gen_random_uuid(), now(), 'recovered', $2) RETURNING id`, invoice, first)
	if err != nil {
		t.Fatal(err)
	}
	var settled, writtenOff string
	if err := f.pool.QueryRow(f.ctx, `SELECT invoices.settled_amount($1)::text, invoices.written_off_amount($1)::text`, invoice).
		Scan(&settled, &writtenOff); err != nil || settled != "100.00" || writtenOff != "100.00" {
		t.Errorf("settled %s, written off %s (%v): want the write-off and not the recovery", settled, writtenOff, err)
	}
	f.mustExec("remove the recovery", `UPDATE invoices.payments SET removed_at = now(), removed_by_user_id = gen_random_uuid(), removal_reason = 'Feil' WHERE id = $1`, recovered)
	f.mustExec("undo", `UPDATE invoices.write_offs SET undone_at = now(), undone_by_user_id = gen_random_uuid(), undo_reason = 'Angret' WHERE id = $1`, first)
	if got := journalFacts(t, f.ctx, f.pool); !equalStrings(got, []string{
		fmt.Sprintf("write_off:%d", first), fmt.Sprintf("recovered_payment:%d", recovered),
		fmt.Sprintf("recovered_payment_removed:%d", recovered), fmt.Sprintf("write_off_undone:%d", first),
	}) {
		t.Errorf("facts = %v", got)
	}
	if err := f.exec(`UPDATE invoices.write_offs SET undone_at = now() WHERE id = $1`, first); err == nil {
		t.Error("an undone write-off was changed again")
	}
	if _, err := writeOff(invoice); err != nil {
		t.Errorf("a write-off after the undo: %v", err)
	}
}
