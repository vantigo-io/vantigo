package invoices

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the billing runs' API (energy consumption billing design D10;
// billing runs plan readings 3, 8 and 9): POST /invoices/billing-runs makes a
// run — the customers named, or every customer Energy's DueSupplies names —
// and one queued item per customer, which the invoices-billing worker
// (billing_worker.go) carries; the two reads answer the runs and a run's
// items with their counts derived from the items, never stored; cancel ends a
// run that is not finished and its queued items no live lease holds. Every
// read of a directory or of Energy is made on the pool before the one
// transaction that writes the run.

// The billing runs' refusals, beside the consumption wizard's
// (consumption.go), and the codes an item records that are the run's own
// rather than a refusal's it was handed (plan reading 7).
const (
	codeBillingRunActive   = "billing_run_active"
	codeBillingRunFinished = "billing_run_finished"
	codeTooManyCustomers   = "too_many_customers"
	codeInvalidRequest     = "invalid_request"
	codeBillingError       = "billing_error"

	cannotRunBillingTitle  = "The billing run cannot be made"
	invalidBillingRunTitle = "Invalid billing run"
)

// maxBillingRunCustomers is the most customers one run is made for, named
// or found (plan readings 2 and 3); customerChunk is one directory read's.
const (
	maxBillingRunCustomers = 5000
	customerChunk          = contracts.MaxActualsRequests
)

// An item's statuses (plan reading 1).
const (
	itemQueued    = "queued"
	itemDrafted   = "drafted"
	itemIssued    = "issued"
	itemSkipped   = "skipped"
	itemFailed    = "failed"
	itemCancelled = "cancelled"
)

// itemStatuses is every status an item may have, the detail's filter.
var itemStatuses = []string{itemQueued, itemDrafted, itemIssued, itemSkipped, itemFailed, itemCancelled}

// errBillingRunActive is the unique index refusing a second run not
// finished (ux_billing_runs_active): two creates racing end in one.
var errBillingRunActive = errors.New("invoices: a billing run is queued or running")

// billingRunActive is the 409 naming the run that is not finished.
func billingRunActive(runID int64) gen.InvoicesConflictProblem {
	p := conflict(codeBillingRunActive, cannotRunBillingTitle,
		fmt.Sprintf("Billing run %d is queued or running; one run is carried at a time.", runID))
	if runID != 0 {
		p.RunId = &runID
	}
	return p
}

// parseBillingRun is step 1's 400s on customerIds (plan reading 3): when
// given, 1 to 5 000 of them, each positive and named once. through is a
// date the generated wrapper has decoded already.
func parseBillingRun(body gen.InvoicesBillingRunRequest) map[string][]string {
	if body.CustomerIds == nil {
		return nil
	}
	var errs map[string][]string
	given := *body.CustomerIds
	switch {
	case len(given) == 0:
		errs = withFieldError(errs, "customerIds", "Name the customers to bill, or leave customerIds out to bill every customer with consumption due")
	case len(given) > maxBillingRunCustomers:
		errs = withFieldError(errs, "customerIds", fmt.Sprintf("A billing run bills at most %d customers", maxBillingRunCustomers))
	}
	seen := make(map[int32]bool, len(given))
	for i, id := range given {
		field := fmt.Sprintf("customerIds[%d]", i)
		switch {
		case id <= 0:
			errs = withFieldError(errs, field, "A customer's id is positive")
		case seen[id]:
			errs = withFieldError(errs, field, "Each customer is named once")
		default:
			seen[id] = true
		}
	}
	return errs
}

// PostInvoicesBillingRuns Make a billing run
// (POST /api/v1/invoices/billing-runs)
//
// Plan reading 3, in its order: the body; issue without invoices:issue, a
// 403 (the router judged invoices:create, the body decides whether the issue
// is asked for); Energy off; a run queued or running; the customers — the
// named ones known to the directory, in chunks of one read's size, or the
// distinct customers of DueSupplies{Every} through the start of the day after
// through — then one transaction writing the run and an item per customer,
// queued and due now. A named customer with nothing due gets an item too,
// which the worker skips no_consumption_due, so the run says so.
func (s *server) PostInvoicesBillingRuns(ctx context.Context, req gen.PostInvoicesBillingRunsRequestObject) (gen.PostInvoicesBillingRunsResponseObject, error) {
	body := *req.Body
	if errs := parseBillingRun(body); len(errs) > 0 {
		return gen.PostInvoicesBillingRuns400ApplicationProblemPlusJSONResponse(invalid(invalidBillingRunTitle, errs)), nil
	}
	through, err := s.consumptionThrough(ctx, body.Through)
	if err != nil {
		return nil, err
	}
	if body.Issue && !s.has(ctx, "invoices:issue") {
		return gen.PostInvoicesBillingRuns403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	if s.deps.Consumption == nil {
		return gen.PostInvoicesBillingRuns409ApplicationProblemPlusJSONResponse(consumptionUnavailable()), nil
	}
	q := store.New(s.deps.Pool)
	if refusal, err := activeRunRefusal(ctx, q); refusal != nil || err != nil {
		if err != nil {
			return nil, err
		}
		return gen.PostInvoicesBillingRuns409ApplicationProblemPlusJSONResponse(*refusal), nil
	}

	var customers []int32
	if body.CustomerIds != nil {
		customers = *body.CustomerIds
		if s.deps.Directory != nil {
			known := make(map[int32]bool, len(customers))
			for chunk := range slices.Chunk(customers, customerChunk) {
				entries, err := s.customerEntries(ctx, chunk)
				if err != nil {
					return nil, fmt.Errorf("invoices: read the billing run's customers: %w", err)
				}
				for _, e := range entries {
					known[e.ID] = true
				}
			}
			var errs map[string][]string
			for i, id := range customers {
				if !known[id] {
					errs = withFieldError(errs, fmt.Sprintf("customerIds[%d]", i), "No customer has this id")
				}
			}
			if len(errs) > 0 {
				return gen.PostInvoicesBillingRuns400ApplicationProblemPlusJSONResponse(invalid(invalidBillingRunTitle, errs)), nil
			}
		}
	} else {
		page, err := s.dueSupplies(ctx, contracts.DueSuppliesRequest{Every: true, Through: throughInstant(through)})
		if err != nil {
			return nil, fmt.Errorf("invoices: read every customer's due supplies: %w", err)
		}
		if page.More {
			return gen.PostInvoicesBillingRuns409ApplicationProblemPlusJSONResponse(conflict(codeTooManyCustomers, cannotRunBillingTitle,
				fmt.Sprintf("More than %d supplies are due; name the customers to bill.", contracts.MaxBillableRows))), nil
		}
		for _, d := range page.Supplies {
			customers = append(customers, d.CustomerID)
		}
		slices.Sort(customers)
		customers = slices.Compact(customers)
		if len(customers) == 0 {
			return gen.PostInvoicesBillingRuns409ApplicationProblemPlusJSONResponse(conflict(codeNoConsumptionDue, cannotRunBillingTitle,
				fmt.Sprintf("No customer has consumption due through %s.", through.Format("2006-01-02")))), nil
		}
	}

	var run store.InvoicesBillingRun
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		now := s.deps.Clock()
		var err error
		run, err = txq.InsertBillingRun(ctx, store.InsertBillingRunParams{
			Through: pgDate(through), Issue: body.Issue, Customers: int32(len(customers)),
			CreatedByUserID: callerID(ctx), CreatedAt: now,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "ux_billing_runs_active" {
			return errBillingRunActive
		}
		if err != nil {
			return fmt.Errorf("invoices: write the billing run: %w", err)
		}
		if err := txq.InsertBillingRunItems(ctx, store.InsertBillingRunItemsParams{RunID: run.ID, Now: now, CustomerIds: customers}); err != nil {
			return fmt.Errorf("invoices: write billing run %d's items: %w", run.ID, err)
		}
		return nil
	})
	if errors.Is(err, errBillingRunActive) {
		refusal, err := activeRunRefusal(ctx, q)
		if err != nil {
			return nil, err
		}
		if refusal == nil {
			refusal = ptr(billingRunActive(0))
		}
		return gen.PostInvoicesBillingRuns409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	wire, err := s.billingRunsWire(ctx, q, []store.InvoicesBillingRun{run})
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBillingRuns201JSONResponse(wire[0]), nil
}

// activeRunRefusal is billing_run_active naming the run queued or running,
// nil when there is none.
func activeRunRefusal(ctx context.Context, q *store.Queries) (*gen.InvoicesConflictProblem, error) {
	id, err := q.ActiveBillingRun(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read the billing run in progress: %w", err)
	}
	return ptr(billingRunActive(id)), nil
}

// billingRunsWire is the runs on the wire, each with its items counted by
// status in one read (plan reading 1: counts are derived, never stored).
func (s *server) billingRunsWire(ctx context.Context, q *store.Queries, runs []store.InvoicesBillingRun) ([]gen.InvoicesBillingRun, error) {
	ids := make([]int64, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	counts := map[int64]*gen.InvoicesBillingRunCounts{}
	if len(ids) > 0 {
		rows, err := q.BillingRunCounts(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("invoices: count the billing runs' items: %w", err)
		}
		for _, r := range rows {
			c := counts[r.RunID]
			if c == nil {
				c = &gen.InvoicesBillingRunCounts{}
				counts[r.RunID] = c
			}
			switch r.Status {
			case itemQueued:
				c.Queued = r.Items
			case itemDrafted:
				c.Drafted = r.Items
			case itemIssued:
				c.Issued = r.Items
			case itemSkipped:
				c.Skipped = r.Items
			case itemFailed:
				c.Failed = r.Items
			case itemCancelled:
				c.Cancelled = r.Items
			}
		}
	}
	out := make([]gen.InvoicesBillingRun, 0, len(runs))
	for _, r := range runs {
		w := gen.InvoicesBillingRun{
			Id: r.ID, Through: wireDate(r.Through.Time), Issue: r.Issue, Status: r.Status, Customers: r.Customers,
			CreatedByUserId: r.CreatedByUserID, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		}
		if c := counts[r.ID]; c != nil {
			w.Counts = *c
		}
		out = append(out, w)
	}
	return out, nil
}

// GetInvoicesBillingRuns List the billing runs
// (GET /api/v1/invoices/billing-runs)
//
// Plan reading 8: the newest first, paged, each with its counts.
func (s *server) GetInvoicesBillingRuns(ctx context.Context, req gen.GetInvoicesBillingRunsRequestObject) (gen.GetInvoicesBillingRunsResponseObject, error) {
	p := req.Params
	if errs := validatePageParams(p.Page, p.PageSize); len(errs) > 0 {
		return gen.GetInvoicesBillingRuns400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	q := store.New(s.deps.Pool)
	total, err := q.CountBillingRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: count the billing runs: %w", err)
	}
	rows, err := q.ListBillingRuns(ctx, store.ListBillingRunsParams{PageOffset: (page - 1) * pageSize, PageSize: pageSize})
	if err != nil {
		return nil, fmt.Errorf("invoices: list the billing runs: %w", err)
	}
	data, err := s.billingRunsWire(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesBillingRuns200JSONResponse(gen.PaginatedResponseOfInvoicesBillingRun{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}

// GetInvoicesBillingRunsById Get a billing run
// (GET /api/v1/invoices/billing-runs/{id})
//
// Plan reading 8: the run and a page of its items in the order they were
// made, of one status when the query names one. Each item carries its
// customer's name as the directory answers it — absent when the directory is
// off or does not know the customer — and the documents it made that still
// exist, in the order it made them: a draft deleted since is not listed.
func (s *server) GetInvoicesBillingRunsById(ctx context.Context, req gen.GetInvoicesBillingRunsByIdRequestObject) (gen.GetInvoicesBillingRunsByIdResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && !slices.Contains(itemStatuses, *p.Status) {
		errs = append(errs, fmt.Sprintf("'status' must be one of queued, drafted, issued, skipped, failed or cancelled, but was '%s'.", *p.Status))
	}
	if len(errs) > 0 {
		return gen.GetInvoicesBillingRunsById400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	q := store.New(s.deps.Pool)
	run, err := q.GetBillingRun(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesBillingRunsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read billing run %d: %w", req.Id, err)
	}
	total, err := q.CountBillingRunItems(ctx, store.CountBillingRunItemsParams{RunID: run.ID, Status: p.Status})
	if err != nil {
		return nil, fmt.Errorf("invoices: count billing run %d's items: %w", run.ID, err)
	}
	rows, err := q.ListBillingRunItems(ctx, store.ListBillingRunItemsParams{
		RunID: run.ID, Status: p.Status, PageOffset: (page - 1) * pageSize, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: list billing run %d's items: %w", run.ID, err)
	}
	var customerIDs []int32
	var invoiceIDs []int64
	for _, r := range rows {
		customerIDs = append(customerIDs, r.CustomerID)
		invoiceIDs = append(invoiceIDs, r.InvoiceIds...)
	}
	names := map[int32]string{}
	if s.deps.Directory != nil {
		entries, err := s.customerEntries(ctx, customerIDs)
		if err != nil {
			return nil, fmt.Errorf("invoices: name billing run %d's customers: %w", run.ID, err)
		}
		for _, e := range entries {
			names[e.ID] = e.Name
		}
	}
	documents := map[int64]gen.InvoicesBillingRunDocument{}
	if len(invoiceIDs) > 0 {
		docs, err := q.BillingRunDocuments(ctx, invoiceIDs)
		if err != nil {
			return nil, fmt.Errorf("invoices: read billing run %d's documents: %w", run.ID, err)
		}
		for _, d := range docs {
			documents[d.ID] = gen.InvoicesBillingRunDocument{InvoiceId: d.ID, Number: d.Number, Status: d.Status}
		}
	}
	items := make([]gen.InvoicesBillingRunItem, 0, len(rows))
	for _, r := range rows {
		item := gen.InvoicesBillingRunItem{
			Id: r.ID, CustomerId: r.CustomerID, Status: r.Status, Code: r.Code, Detail: r.Detail,
			Attempts: r.Attempts, FinishedAt: r.FinishedAt, Invoices: []gen.InvoicesBillingRunDocument{},
		}
		if name, ok := names[r.CustomerID]; ok {
			item.CustomerName = &name
		}
		for _, id := range r.InvoiceIds {
			if d, ok := documents[id]; ok {
				item.Invoices = append(item.Invoices, d)
			}
		}
		items = append(items, item)
	}
	wire, err := s.billingRunsWire(ctx, q, []store.InvoicesBillingRun{run})
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesBillingRunsById200JSONResponse(gen.InvoicesBillingRunDetail{
		Run: wire[0], Items: items, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}

// PostInvoicesBillingRunsByIdCancel Cancel a billing run
// (POST /api/v1/invoices/billing-runs/{id}/cancel)
//
// Plan reading 9: a queued or running run becomes cancelled and its queued
// items no live lease holds cancelled with it, in one transaction; an item
// being carried finishes and records its outcome. A run already done or
// cancelled is billing_run_finished.
func (s *server) PostInvoicesBillingRunsByIdCancel(ctx context.Context, req gen.PostInvoicesBillingRunsByIdCancelRequestObject) (gen.PostInvoicesBillingRunsByIdCancelResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetBillingRun(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesBillingRunsByIdCancel404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read billing run %d: %w", req.Id, err)
	}
	var run store.InvoicesBillingRun
	finished := false
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		now := s.deps.Clock()
		var err error
		run, err = txq.CancelBillingRun(ctx, store.CancelBillingRunParams{Now: now, ID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			finished = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("invoices: cancel billing run %d: %w", req.Id, err)
		}
		if _, err := txq.CancelBillingRunItems(ctx, store.CancelBillingRunItemsParams{Now: now, RunID: run.ID}); err != nil {
			return fmt.Errorf("invoices: cancel billing run %d's items: %w", run.ID, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if finished {
		return gen.PostInvoicesBillingRunsByIdCancel409ApplicationProblemPlusJSONResponse(conflict(codeBillingRunFinished, "The billing run cannot be cancelled",
			fmt.Sprintf("Billing run %d is done or cancelled already.", req.Id))), nil
	}
	wire, err := s.billingRunsWire(ctx, q, []store.InvoicesBillingRun{run})
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBillingRunsByIdCancel200JSONResponse(wire[0]), nil
}
