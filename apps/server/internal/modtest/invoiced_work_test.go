package modtest

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

type fakeHolder struct{}

func (fakeHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceHours}
}

func (fakeHolder) MarkInvoiced(context.Context, pgx.Tx, contracts.InvoiceRef, []contracts.WorkSource) error {
	return nil
}

func (fakeHolder) ReleaseInvoiced(context.Context, pgx.Tx, contracts.InvoiceRef, []contracts.WorkSource) error {
	return nil
}

type fakeHours struct{}

func (*fakeHours) BillableHours(context.Context, contracts.BillableRequest) (contracts.BillableHoursPage, error) {
	return contracts.BillableHoursPage{}, nil
}

type fakeExpenses struct{}

func (*fakeExpenses) BillableExpenses(context.Context, contracts.BillableRequest) (contracts.BillableExpensesPage, error) {
	return contracts.BillableExpensesPage{}, nil
}

type fakeMilestones struct{}

func (*fakeMilestones) BillableMilestones(context.Context, contracts.BillableRequest) (contracts.BillableMilestonesPage, error) {
	return contracts.BillableMilestonesPage{}, nil
}

// Invoices' tests stand in for the three source modules with fakes —
// depguard keeps time, expenses and projects out of its test package — so a
// holder and the three billable reads set here must reach the module under
// test through Compose, which overwrites none of them when no composed module
// provides one, as WithActuals' provider survives.
func TestModtest_TheInvoicedWorkAndBillableSeamsSurviveCompose(t *testing.T) {
	t.Parallel()
	holder := fakeHolder{}
	hours, expenses, milestones := &fakeHours{}, &fakeExpenses{}, &fakeMilestones{}
	var got module.Deps
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d
			return http.NotFoundHandler(), nil
		},
	}

	New(t, WithRecorder(contracttest.NewForModule("customers")), WithModule(capture),
		WithInvoicedWork(holder), WithBillableHours(hours), WithBillableExpenses(expenses), WithBillableMilestones(milestones))

	if len(got.InvoicedWork) != 1 || got.InvoicedWork[0] != holder {
		t.Errorf("Deps.InvoicedWork = %v, want the one fake holder", got.InvoicedWork)
	}
	if got.BillableHours != hours || got.BillableExpenses != expenses || got.BillableMilestones != milestones {
		t.Errorf("Deps.Billable* = %v, %v, %v; want the three fakes", got.BillableHours, got.BillableExpenses, got.BillableMilestones)
	}
}
