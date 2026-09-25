package expenses_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Who is owed money back is one rule written twice on purpose (supplier
// invoices design D1): expenses.owes_employee, the SQL function migration
// 00033 defines and every owes-the-employee query calls, and owesEmployee, its
// Go mirror, which decides a row's canMarkReimbursed and owedToEmployee. This
// asks both about every kind a row can carry and every payer it can name, and
// fails on any pair they disagree about — the kind this delivery adds, which
// owes nobody whatever its paid_by says, among them.
func TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, company := "employee", "company"
	for _, kind := range []string{"outlay", "mileage", "per_diem", "supplier_invoice"} {
		for _, paidBy := range []*string{nil, &employee, &company} {
			inSQL := modtest.One[bool](t, h.Harness, `SELECT expenses.owes_employee($1::text, $2::text)`, kind, paidBy)
			if inGo := expenses.OwesEmployee(kind, paidBy); inGo != inSQL {
				payer := "nobody"
				if paidBy != nil {
					payer = *paidBy
				}
				t.Errorf("a %s paid by %s: Go says owed %v, SQL says %v", kind, payer, inGo, inSQL)
			}
		}
	}
	if expenses.OwesEmployee("supplier_invoice", &employee) {
		t.Error("a supplier invoice marked paid by the employee owes them something; a supplier invoice owes nobody")
	}
}
