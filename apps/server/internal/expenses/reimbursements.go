package expenses

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is decision X5's first track: what the employee is owed back. It
// is expenses:manage's whole job here — the list a payroll run is made from,
// the run itself, the way back from it, and the file a payroll system reads.
//
// The unit is a standalone expense, which is every expense there is in this
// delivery; travel claims become units of their own in delivery B, and the
// batch these operations run through is already the one that will take them.
//
// The period lock appears nowhere in it. The lock protects what an employee
// submitted and what an approver decided — a date, an amount, a status. A
// payroll run happens *after* the books close, and only expenses:manage may
// make one, whom the lock has never held back; a check here would either be
// dead code or would shut the books on the one person whose work starts when
// they shut.

// The two states the reimbursement list answers in.
const (
	reimbursementWaiting   = "waiting"
	reimbursementPaid      = "reimbursed"
	referenceMaxLength     = 100
	reimbursementRowsLimit = 5000
)

// reimbursementStates is what the state parameter may say, in the order the
// message lists them.
var reimbursementStates = []string{reimbursementWaiting, reimbursementPaid}

// exportMaxRows is how many rows one payroll file may hold. It is a variable
// rather than a constant only so the test that proves the bound bites does not
// have to record five thousand expenses (export_test.go); nothing in the
// module ever assigns it.
var exportMaxRows = reimbursementRowsLimit

// reimbursementFilter is what both the list and the export narrow by: which
// state, whose expenses, and over which entry dates.
type reimbursementFilter struct {
	Paid     bool
	UserID   *uuid.UUID
	FromDate pgtype.Date
	ToDate   pgtype.Date
}

// parseState reads the state parameter, defaulting to what is waiting. It
// answers the message a list refuses with when it is none of the two, in the
// words every other filter here is refused in.
func parseState(raw *string) (bool, string) {
	value := ""
	if raw != nil {
		value = *raw
	}
	switch value {
	case "", reimbursementWaiting:
		return false, ""
	case reimbursementPaid:
		return true, ""
	}
	return false, fmt.Sprintf("'state' must be one of %s, but was '%s'.",
		strings.Join(reimbursementStates, ", "), value)
}

// GetExpensesReimbursements List what is owed back
// (GET /api/v1/expenses/reimbursements)
//
// The approved expenses that owe their owner something and have not been paid,
// grouped per person and paged **by person in SQL**, exactly as the approval
// queue is — the database takes the page of people and hands back only their
// expenses. state=reimbursed answers what has already been paid instead, the
// latest payout first, so an undo never needs paging to reach.
func (s *server) GetExpensesReimbursements(ctx context.Context, req gen.GetExpensesReimbursementsRequestObject) (gen.GetExpensesReimbursementsResponseObject, error) {
	p := req.Params
	msgs := validatePageParams(p.Page, p.PageSize)
	paid, msg := parseState(p.State)
	if msg != "" {
		msgs = append(msgs, msg)
	}
	if len(msgs) > 0 {
		return gen.GetExpensesReimbursements400ApplicationProblemPlusJSONResponse(invalidQuery(msgs)), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	filter := reimbursementFilter{
		Paid: paid, UserID: p.UserId,
		FromDate: optionalDate(p.From), ToDate: optionalDate(p.To),
	}

	total, err := q.CountReimbursementGroups(ctx, store.CountReimbursementGroupsParams{
		Reimbursed: filter.Paid, UserID: filter.UserID,
		FromDate: filter.FromDate, ToDate: filter.ToDate,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: count the reimbursement list's groups: %w", err)
	}
	rows, err := q.ListReimbursementGroupEntries(ctx, store.ListReimbursementGroupEntriesParams{
		Reimbursed: filter.Paid, UserID: filter.UserID,
		FromDate: filter.FromDate, ToDate: filter.ToDate,
		PageSize: pageSize, PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list what is owed back: %w", err)
	}
	// One renderer: an expense here is shaped exactly as a single read of it
	// would be for this caller, capabilities and all.
	entries, err := s.entryResponses(ctx, c, rows)
	if err != nil {
		return nil, err
	}
	data, err := reimbursementGroups(rows, entries, filter.Paid)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesReimbursements200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// reimbursementGroups gathers one page's rows into one group per person, in
// the very order the page was taken in (ListReimbursementGroupEntries),
// re-derived here from the rows themselves — which it can be, because a page
// holds whole groups. The names come off the rendered expenses, so this asks
// identity for nothing the shaping has not already asked for.
func reimbursementGroups(rows []store.ExpensesEntry, entries []gen.ExpensesEntryResponse, paid bool) ([]gen.ExpensesReimbursementGroup, error) {
	type group struct {
		user    gen.ExpensesUserRef
		oldest  time.Time
		latest  time.Time
		entries []gen.ExpensesEntryResponse
		totals  map[string]*currencyTotal
	}
	order := make([]uuid.UUID, 0, len(rows))
	byUser := map[uuid.UUID]*group{}
	for i, row := range rows {
		g, ok := byUser[row.UserID]
		if !ok {
			g = &group{
				user:   gen.ExpensesUserRef(entries[i].Owner),
				oldest: row.EntryDate.Time,
				totals: map[string]*currencyTotal{},
			}
			byUser[row.UserID] = g
			order = append(order, row.UserID)
		}
		if row.EntryDate.Time.Before(g.oldest) {
			g.oldest = row.EntryDate.Time
		}
		if row.ReimbursedAt != nil && row.ReimbursedAt.After(g.latest) {
			g.latest = *row.ReimbursedAt
		}
		g.entries = append(g.entries, entries[i])
		if err := addToTotals(g.totals, row); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(order, func(a, b uuid.UUID) int {
		if paid {
			// The latest payout first, so an undo is the top of the list.
			return cmp.Or(byUser[b].latest.Compare(byUser[a].latest), strings.Compare(a.String(), b.String()))
		}
		return cmp.Or(byUser[a].oldest.Compare(byUser[b].oldest), strings.Compare(a.String(), b.String()))
	})

	data := make([]gen.ExpensesReimbursementGroup, 0, len(order))
	for _, id := range order {
		g := byUser[id]
		data = append(data, gen.ExpensesReimbursementGroup{
			User: g.user, Entries: g.entries, Totals: currencyTotals(g.totals),
		})
	}
	return data, nil
}

// parseReimbursedBody runs the payroll run's own rules over its body: the day
// it was made, which is a calendar day and never in the future, and the
// reference whoever made it will look it up by.
func (s *server) parseReimbursedBody(body gen.ExpensesReimbursedRequest) (pgtype.Date, *string, map[string][]string) {
	var errs map[string][]string
	date := utcDay(body.Date.Time)
	if date.IsZero() {
		errs = withFieldError(errs, "date", "The day the payroll run was made is required")
	} else if date.After(utcDay(s.deps.Clock())) {
		errs = withFieldError(errs, "date",
			"A payroll run is recorded on the day it was made, which cannot be in the future")
	}
	var reference *string
	if body.Reference != nil {
		if msg := optionalText(body.Reference, "A reference", referenceMaxLength, &reference); msg != "" {
			errs = withFieldError(errs, "reference", msg)
		}
	}
	return pgDate(date), reference, errs
}

// PostExpensesReimbursed Mark expenses reimbursed
// (POST /api/v1/expenses/reimbursed)
//
// One payroll run, recorded on the expenses it paid, all or nothing. It is one
// more decision value (flow.go) rather than a batch of its own: the locking,
// the per-id refusals and the all-or-nothing rule are the flow's, and only the
// refusals and the update differ.
func (s *server) PostExpensesReimbursed(ctx context.Context, req gen.PostExpensesReimbursedRequestObject) (gen.PostExpensesReimbursedResponseObject, error) {
	body := gen.ExpensesReimbursedRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	date, reference, errs := s.parseReimbursedBody(body)
	by, now := callerID(ctx), s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusApproved, verb: "marked reimbursed", manageOnly: true,
		also: func(id int64, row store.ExpensesEntry) string {
			switch {
			case row.ReimbursedAt != nil:
				return fmt.Sprintf("Expense %d has already been reimbursed", id)
			case !owesEmployee(row):
				return fmt.Sprintf("Expense %d owes the employee nothing", id)
			}
			return ""
		},
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.MarkEntriesReimbursed(ctx, store.MarkEntriesReimbursedParams{
				Ids: ids, ReimbursedBy: by, ReimbursementDate: date, Reference: reference, Now: now,
			})
			if err != nil {
				return nil, fmt.Errorf("expenses: mark expenses reimbursed: %w", err)
			}
			return rows, nil
		},
	}, gen.ExpensesFlowRequest{EntryIds: body.EntryIds, ClaimIds: body.ClaimIds}, errs)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesReimbursed403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesReimbursed400ApplicationProblemPlusJSONResponse(invalidReimbursement(out.errs)), nil
	}
	return gen.PostExpensesReimbursed200JSONResponse(out.entries), nil
}

// PostExpensesReimbursedUndo Undo marking expenses reimbursed
// (POST /api/v1/expenses/reimbursed/undo)
//
// The whole stamp comes off — the day, the reference and who made it — so the
// expenses stand in the waiting list exactly as they did before.
func (s *server) PostExpensesReimbursedUndo(ctx context.Context, req gen.PostExpensesReimbursedUndoRequestObject) (gen.PostExpensesReimbursedUndoResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	now := s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusApproved, verb: "taken back off a payroll run", manageOnly: true,
		also: func(id int64, row store.ExpensesEntry) string {
			if row.ReimbursedAt == nil {
				return fmt.Sprintf("Expense %d has not been reimbursed", id)
			}
			return ""
		},
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.UnmarkEntriesReimbursed(ctx, store.UnmarkEntriesReimbursedParams{Ids: ids, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: undo a reimbursement: %w", err)
			}
			return rows, nil
		},
	}, body, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesReimbursedUndo403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesReimbursedUndo400ApplicationProblemPlusJSONResponse(invalidReimbursement(out.errs)), nil
	}
	return gen.PostExpensesReimbursedUndo200JSONResponse(out.entries), nil
}

// csvDownload writes the payroll export itself: the generated response type
// hard-codes a bare text/csv and names no file, and this answer has to say
// which encoding it is in, what to call it and that nobody may cache it.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) VisitGetExpensesReimbursementsExportCsvResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A payroll file names what people are paid. It belongs in nobody's
	// cache, and least of all in a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

// GetExpensesReimbursementsExportCsv Export what is owed back as CSV
// (GET /api/v1/expenses/reimbursements/export.csv)
//
// The same expenses the list holds, as the file a payroll system reads. Every
// name it needs — the people, the projects, the categories — is resolved in
// bulk before a byte is written, so the file costs a fixed number of reads
// however many rows it has.
func (s *server) GetExpensesReimbursementsExportCsv(ctx context.Context, req gen.GetExpensesReimbursementsExportCsvRequestObject) (gen.GetExpensesReimbursementsExportCsvResponseObject, error) {
	p := req.Params
	paid, msg := parseState(p.State)
	if msg != "" {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			invalidExport(fieldError("state", msg))), nil
	}
	var ids []int64
	if p.EntryIds != nil {
		ids = uniqueIDs(*p.EntryIds)
	}
	byIDs := len(ids) > 0
	if len(ids) > exportMaxRows {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			invalidExport(fieldError("entryIds", fmt.Sprintf(
				"At most %d expense ids may be exported at once; %d were given", exportMaxRows, len(ids))))), nil
	}

	q := store.New(s.deps.Pool)
	rows, err := q.ListReimbursementRows(ctx, store.ListReimbursementRowsParams{
		ByIds: byIDs, Reimbursed: paid, AllIds: !byIDs, Ids: ids,
		UserID: p.UserId, FromDate: optionalDate(p.From), ToDate: optionalDate(p.To),
		// One row more than the cap, so "the whole file" and "more than a file
		// may hold" are told apart without a second count.
		RowLimit: int32(exportMaxRows) + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: read the payroll export: %w", err)
	}
	if byIDs {
		// Only expenses:manage reaches here, and expenses:manage sees every
		// expense, so an id with no row is an id with no expense.
		existing, err := q.GetEntries(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("expenses: read the expenses named for export: %w", err)
		}
		if refusals := missingExportIDs(ids, rows, existing); len(refusals) > 0 {
			return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
				invalidExport(map[string][]string{"entryIds": refusals})), nil
		}
	}
	if len(rows) > exportMaxRows {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			tooManyExportRows(exportMaxRows)), nil
	}

	names, err := s.namesFor(ctx, rows)
	if err != nil {
		return nil, err
	}
	body, err := reimbursementCSV(rows, names)
	if err != nil {
		return nil, err
	}
	return csvDownload{
		body:     body,
		fileName: fmt.Sprintf("expenses-reimbursements-%s.csv", utcDay(s.deps.Clock()).Format(time.DateOnly)),
	}, nil
}

// missingExportIDs is the per-id refusals of an export that named ids: an id
// that names no expense reads as the unknown id's "was not found", and one
// that names an expense a payroll run does not pay for says so. Nothing is
// left out of a payroll file silently — a file missing a line nobody was told
// about is worse than no file.
func missingExportIDs(ids []int64, rows, existing []store.ExpensesEntry) []string {
	exportable := make(map[int64]bool, len(rows))
	for _, row := range rows {
		exportable[row.ID] = true
	}
	known := make(map[int64]bool, len(existing))
	for _, row := range existing {
		known[row.ID] = true
	}
	var refusals []string
	for _, id := range ids {
		switch {
		case exportable[id]:
		case !known[id]:
			refusals = append(refusals, notFoundRefusal(id))
		default:
			refusals = append(refusals, fmt.Sprintf(
				"Expense %d cannot be exported: only an approved expense that owes somebody something can be", id))
		}
	}
	return refusals
}

// The payroll file's own shape. It is a contract with a spreadsheet rather
// than with a client, which is why it is written by hand here rather than
// through encoding/csv: that package writes commas and LF, and a Norwegian
// Excel reading this file needs semicolons, a decimal comma, CRLF and a byte
// order mark, or it puts every row in one column and reads "Anna Ås" as
// "Anna Ã…s".
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
)

// csvHeader is the columns, in order. They are English and untranslated: the
// file is read by a payroll system and by whoever set it up, not by every
// employee, and a column name that moved with the reader's language would
// break the import the first time somebody switched it.
var csvHeader = []string{
	"Employee", "User id", "Date", "Kind", "Description",
	"Category", "Currency", "Gross", "VAT", "Owed", "Project code",
}

// reimbursementCSV is rows as the payroll file, with every name already
// resolved (namesFor). Rows come out by the person's display name, then by
// date and id: a payroll file is read by a person, and the database's own
// order is by a uuid nobody can read.
func reimbursementCSV(rows []store.ExpensesEntry, names entryNames) ([]byte, error) {
	ordered := slices.Clone(rows)
	slices.SortFunc(ordered, func(a, b store.ExpensesEntry) int {
		return cmp.Or(
			strings.Compare(displayNameOf(a.UserID, names), displayNameOf(b.UserID, names)),
			a.EntryDate.Time.Compare(b.EntryDate.Time),
			cmp.Compare(a.ID, b.ID),
		)
	})

	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvHeader)
	for _, row := range ordered {
		cells, err := csvCells(row, names)
		if err != nil {
			return nil, err
		}
		writeCSVRow(&b, cells)
	}
	return b.Bytes(), nil
}

// displayNameOf is how a person is named in the file.
func displayNameOf(userID uuid.UUID, names entryNames) string {
	if user, ok := names.users[userID]; ok {
		return user.DisplayName
	}
	return unknownUser
}

// csvCells is one expense as its row of the file. Amounts are rendered from
// the exact decimals the columns hold, never from a float, and with the
// decimal comma the file's readers expect; a VAT or a project code there is
// none of is an empty cell rather than a zero or a dash.
func csvCells(row store.ExpensesEntry, names entryNames) ([]string, error) {
	gross, err := ratFromNumeric(row.GrossAmount)
	if err != nil {
		return nil, err
	}
	vat, err := ratPtrFromNumeric(row.VatAmount)
	if err != nil {
		return nil, err
	}
	category := ""
	if row.CategoryID != nil {
		if c, ok := names.categories[*row.CategoryID]; ok {
			category = c.Name
		}
	}
	// The project's code, and nothing at all when the entry is on none, when
	// the directory no longer lists it, or when this installation has no
	// projects module. The column stays either way, so a payroll system need
	// not know which modules an installation runs.
	project := ""
	if row.ProjectID != nil {
		if p, ok := names.projects[*row.ProjectID]; ok {
			project = p.Code
		}
	}
	return []string{
		displayNameOf(row.UserID, names),
		row.UserID.String(),
		row.EntryDate.Time.Format(time.DateOnly),
		row.Kind,
		row.Description,
		category,
		row.Currency,
		csvAmount(gross),
		csvAmountPtr(vat),
		csvAmount(owedToEmployee(row, gross)),
		project,
	}, nil
}

// csvAmount is an exact decimal with the decimal comma, at the scale the
// columns hold.
func csvAmount(v *big.Rat) string {
	return strings.Replace(decimalText(v, moneyPlaces), ".", ",", 1)
}

// csvAmountPtr is csvAmount, or an empty cell for a figure there is none of.
func csvAmountPtr(v *big.Rat) string {
	if v == nil {
		return ""
	}
	return csvAmount(v)
}

// writeCSVRow writes one row, each cell guarded and quoted as it needs.
func writeCSVRow(b *bytes.Buffer, cells []string) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteRune(csvSeparator)
		}
		b.WriteString(csvCell(cell))
	}
	b.WriteString(csvLineEnd)
}

// csvCell is one cell as it goes into the file: guarded against a spreadsheet
// reading it as a formula, then quoted if it holds anything that would
// otherwise end the cell or the row.
//
// The guard is the one every CSV exporter needs and most forget: a cell
// beginning with '=', '+', '-', '@', a tab or a carriage return is a formula
// to Excel and to LibreOffice, so a description somebody typed — which nobody
// vetted, and which a stranger to the installation may have typed — could run
// when a colleague opens the file. An apostrophe in front makes it text
// again. Amounts never take it: this module writes them itself, and none of
// them is negative.
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
