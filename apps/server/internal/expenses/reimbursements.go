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
// The unit is a standalone expense or a whole travel claim. A trip is paid as
// one, for the sum of what its lines owe its owner, and its lines never appear
// as loose expenses — the list, the batch and the stamp are all the claim's.
// The one place the *lines* come back is the payroll file, which is a file of
// lines: a payroll system wants each amount on its own row, with the trip it
// was on named beside it.
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
		FromDate: filter.FromDate, ToDate: filter.ToDate, TimeZone: c.Settings.TimeZone,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: count the reimbursement list's groups: %w", err)
	}
	// The page of *people* first, in the order the state asks for, and only
	// then their units — so a page always holds whole groups and the database
	// never hands Go more rows than the page needs.
	userIDs, err := q.ListReimbursementGroups(ctx, store.ListReimbursementGroupsParams{
		Reimbursed: filter.Paid, UserID: filter.UserID,
		FromDate: filter.FromDate, ToDate: filter.ToDate, TimeZone: c.Settings.TimeZone,
		PageSize: pageSize, PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: page the reimbursement list: %w", err)
	}
	rows, err := q.ListReimbursementGroupEntries(ctx, store.ListReimbursementGroupEntriesParams{
		Reimbursed: filter.Paid, UserIds: userIDs,
		FromDate: filter.FromDate, ToDate: filter.ToDate,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list what is owed back: %w", err)
	}
	claims, err := q.ListReimbursementGroupClaims(ctx, store.ListReimbursementGroupClaimsParams{
		Reimbursed: filter.Paid, UserIds: userIDs,
		FromDate: filter.FromDate, ToDate: filter.ToDate, TimeZone: c.Settings.TimeZone,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list the travel claims owed back: %w", err)
	}
	// One renderer: an expense here is shaped exactly as a single read of it
	// would be for this caller, capabilities and all.
	entries, err := s.entryResponses(ctx, c, rows)
	if err != nil {
		return nil, err
	}
	units, err := s.claimUnitsOf(ctx, q, c, claims)
	if err != nil {
		return nil, err
	}
	data, err := reimbursementGroups(userIDs, rows, entries, units)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesReimbursements200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// reimbursementGroups gathers one page's units into one group per person, in
// the very order the page was taken in (ListReimbursementGroups) — the user ids
// arrive already sorted, so Go re-derives nothing and the two can never
// disagree. The names come off the rendered expenses and the rendered claims, so
// this asks identity for nothing the shaping has not already asked for.
func reimbursementGroups(userIDs []uuid.UUID, rows []store.ExpensesEntry,
	entries []gen.ExpensesEntryResponse, units []claimUnitResponse,
) ([]gen.ExpensesReimbursementGroup, error) {
	type group struct {
		user    gen.ExpensesUserRef
		entries []gen.ExpensesEntryResponse
		claims  []gen.ExpensesClaimSummary
		totals  map[string]*currencyTotal
	}
	byUser := make(map[uuid.UUID]*group, len(userIDs))
	order := make([]uuid.UUID, 0, len(userIDs))
	for _, id := range userIDs {
		byUser[id] = &group{totals: map[string]*currencyTotal{}}
		order = append(order, id)
	}
	for i, row := range rows {
		g, ok := byUser[row.UserID]
		if !ok {
			continue
		}
		g.user = gen.ExpensesUserRef(entries[i].Owner)
		g.entries = append(g.entries, entries[i])
		if err := addToTotals(g.totals, row); err != nil {
			return nil, err
		}
	}
	for _, unit := range units {
		g, ok := byUser[unit.owner.UserId]
		if !ok {
			continue
		}
		g.user = unit.owner
		g.claims = append(g.claims, unit.summary)
		addSummaryToTotals(g.totals, unit.summary)
	}

	data := make([]gen.ExpensesReimbursementGroup, 0, len(order))
	for _, id := range order {
		g := byUser[id]
		if g.user.UserId == uuid.Nil {
			g.user = gen.ExpensesUserRef{UserId: id, DisplayName: unknownUser}
		}
		data = append(data, gen.ExpensesReimbursementGroup{
			User: g.user, Entries: entriesOrEmpty(g.entries), Claims: claimsOrEmpty(g.claims),
			Totals: currencyTotals(g.totals),
		})
	}
	return data, nil
}

// entriesOrEmpty and claimsOrEmpty keep a group's two lists arrays rather than
// nulls: a person with only trips and a person with only loose expenses read
// the same way.
func entriesOrEmpty(list []gen.ExpensesEntryResponse) []gen.ExpensesEntryResponse {
	if list == nil {
		return []gen.ExpensesEntryResponse{}
	}
	return list
}

func claimsOrEmpty(list []gen.ExpensesClaimSummary) []gen.ExpensesClaimSummary {
	if list == nil {
		return []gen.ExpensesClaimSummary{}
	}
	return list
}

// parseReimbursedBody runs the payroll run's own rules over its body: the day
// it was made, which is a calendar day and never in the future, and the
// reference whoever made it will look it up by.
func (s *server) parseReimbursedBody(body gen.ExpensesReimbursedRequest, today time.Time) (pgtype.Date, *string, map[string][]string) {
	var errs map[string][]string
	date := utcDay(body.Date.Time)
	if date.IsZero() {
		errs = withFieldError(errs, "date", "The day the payroll run was made is required")
	} else if date.After(today) {
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
	// "Today" is today in the installation's own zone: a clerk in Oslo making
	// a run at 00:30 on the 1st is making it on the 1st, not on the 31st.
	zone, err := s.businessZone(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	date, reference, errs := s.parseReimbursedBody(body, businessDay(s.deps.Clock(), zone))
	by, now := callerID(ctx), s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusApproved, verb: "marked reimbursed", manageOnly: true,
		claimImperative: "mark the claim reimbursed",
		also: func(id int64, row store.ExpensesEntry) string {
			switch {
			case row.ReimbursedAt != nil:
				return fmt.Sprintf("Expense %d has already been reimbursed", id)
			case !owesEmployee(row):
				return fmt.Sprintf("Expense %d owes the employee nothing", id)
			}
			return ""
		},
		claimAlso: func(id int64, claim store.ExpensesClaim, lines []store.ExpensesEntry) string {
			switch {
			case claim.ReimbursedAt != nil:
				return fmt.Sprintf("Travel claim %d has already been reimbursed", id)
			case !claimOwesEmployee(lines):
				return fmt.Sprintf("Travel claim %d owes the employee nothing", id)
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
		applyClaims: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesClaim, error) {
			rows, err := txq.MarkClaimsReimbursed(ctx, store.MarkClaimsReimbursedParams{
				Ids: ids, ReimbursedBy: by, ReimbursementDate: date, Reference: reference, Now: now,
			})
			if err != nil {
				return nil, fmt.Errorf("expenses: mark travel claims reimbursed: %w", err)
			}
			return rows, nil
		},
	}, body.EntryIds, body.ClaimIds, errs)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesReimbursed403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesReimbursed400ApplicationProblemPlusJSONResponse(invalidReimbursement(out.errs)), nil
	}
	return gen.PostExpensesReimbursed200JSONResponse(out.response()), nil
}

// claimOwesEmployee reports whether a travel claim owes its owner anything at
// all — the Go half of the EXISTS the reimbursement queries apply in SQL, and
// the two must stay one rule. A trip of nothing but company-paid outlays owes
// nothing and cannot go on a payroll run, exactly as such an outlay cannot on
// its own.
func claimOwesEmployee(lines []store.ExpensesEntry) bool {
	for _, line := range lines {
		if owesEmployee(line) {
			return true
		}
	}
	return false
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
		claimImperative: "take the claim off the payroll run",
		also: func(id int64, row store.ExpensesEntry) string {
			if row.ReimbursedAt == nil {
				return fmt.Sprintf("Expense %d has not been reimbursed", id)
			}
			return ""
		},
		claimAlso: func(id int64, claim store.ExpensesClaim, _ []store.ExpensesEntry) string {
			if claim.ReimbursedAt == nil {
				return fmt.Sprintf("Travel claim %d has not been reimbursed", id)
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
		applyClaims: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesClaim, error) {
			rows, err := txq.UnmarkClaimsReimbursed(ctx, store.UnmarkClaimsReimbursedParams{Ids: ids, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: undo a travel claim's reimbursement: %w", err)
			}
			return rows, nil
		},
	}, body.EntryIds, body.ClaimIds, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesReimbursedUndo403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesReimbursedUndo400ApplicationProblemPlusJSONResponse(invalidReimbursement(out.errs)), nil
	}
	return gen.PostExpensesReimbursedUndo200JSONResponse(out.response()), nil
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
// The same units the list holds, as the file a payroll system reads — but a
// file of **lines**: a standalone expense is its own row, and a travel claim
// writes one row per expense it holds, each carrying the trip it was on. Every
// name it needs — the people, the projects, the categories — is resolved in bulk
// before a byte is written, so the file costs a fixed number of reads however
// many rows it has.
func (s *server) GetExpensesReimbursementsExportCsv(ctx context.Context, req gen.GetExpensesReimbursementsExportCsvRequestObject) (gen.GetExpensesReimbursementsExportCsvResponseObject, error) {
	p := req.Params
	paid, msg := parseState(p.State)
	if msg != "" {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			invalidExportQuery([]string{msg})), nil
	}
	// A selection that is there and names nothing is a mistake, not "export
	// everything": a client building it from a row selection with nothing
	// ticked would otherwise be handed the whole unpaid list. Leaving both
	// parameters out is still the filter mode, which is what that button should
	// send.
	byIDs := p.EntryIds != nil || p.ClaimIds != nil
	var ids, claimIDs []int64
	if byIDs {
		ids = uniqueIDs(idsOf(p.EntryIds))
		claimIDs = uniqueIDs(idsOf(p.ClaimIds))
	}
	if byIDs && len(ids)+len(claimIDs) == 0 {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			invalidExport(fieldError("entryIds", "At least one expense or travel claim id is required"))), nil
	}
	if len(ids)+len(claimIDs) > exportMaxRows {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			invalidExport(fieldError("entryIds", fmt.Sprintf(
				"At most %d expense and travel claim ids may be exported at once; %d were given",
				exportMaxRows, len(ids)+len(claimIDs))))), nil
	}

	q := store.New(s.deps.Pool)
	// One reading of the settings for both the zone the filters judge a trip's
	// departure in and the zone the file is named in, so the two cannot differ.
	current, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	zone := zoneOf(current)
	rows, err := q.ListReimbursementRows(ctx, store.ListReimbursementRowsParams{
		ByIds: byIDs, Reimbursed: paid, AllIds: !byIDs, EntryIds: ids, ClaimIds: claimIDs,
		UserID: p.UserId, FromDate: optionalDate(p.From), ToDate: optionalDate(p.To),
		TimeZone: current.TimeZone,
		// One row more than the cap, so "the whole file" and "more than a file
		// may hold" are told apart without a second count.
		RowLimit: int32(exportMaxRows) + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: read the payroll export: %w", err)
	}
	if byIDs {
		// Only expenses:manage reaches here, and expenses:manage sees every
		// expense, so an id with no row is an id with nothing behind it.
		existing, err := q.GetEntries(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("expenses: read the expenses named for export: %w", err)
		}
		existingClaims, err := q.GetClaims(ctx, claimIDs)
		if err != nil {
			return nil, fmt.Errorf("expenses: read the travel claims named for export: %w", err)
		}
		if refusals := missingExportIDs(ids, claimIDs, rows, existing, existingClaims); refusals != nil {
			return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
				invalidExport(refusals)), nil
		}
	}
	if len(rows) > exportMaxRows {
		return gen.GetExpensesReimbursementsExportCsv400ApplicationProblemPlusJSONResponse(
			tooManyExportRows(exportMaxRows)), nil
	}

	names, err := s.namesFor(ctx, entriesOfExport(rows))
	if err != nil {
		return nil, err
	}
	body, err := reimbursementCSV(rows, names)
	if err != nil {
		return nil, err
	}
	return csvDownload{
		body: body,
		// Today in the installation's own zone, which is the day the clerk
		// downloading it would write on the folder.
		fileName: fmt.Sprintf("expenses-reimbursements-%s.csv",
			businessDay(s.deps.Clock(), zone).Format(time.DateOnly)),
	}, nil
}

// entriesOfExport is the export's rows as plain expenses, which is what the
// name resolution takes. The claim's purpose rides along on each row and needs
// no directory of its own.
func entriesOfExport(rows []store.ListReimbursementRowsRow) []store.ExpensesEntry {
	out := make([]store.ExpensesEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ExpensesEntry)
	}
	return out
}

// missingExportIDs is the per-id refusals of an export that named ids: an id
// that names nothing reads as the unknown id's "was not found", and one that
// names a unit a payroll run does not pay for says so. Nothing is left out of a
// payroll file silently — a file missing a line nobody was told about is worse
// than no file. The messages are keyed by the list that named the id, as every
// batch here keys them.
func missingExportIDs(ids, claimIDs []int64, rows []store.ListReimbursementRowsRow,
	existing []store.ExpensesEntry, existingClaims []store.ExpensesClaim,
) map[string][]string {
	exportable := make(map[int64]bool, len(rows))
	exportableClaims := map[int64]bool{}
	for _, row := range rows {
		exportable[row.ExpensesEntry.ID] = true
		if row.ExpensesEntry.ClaimID != nil {
			exportableClaims[*row.ExpensesEntry.ClaimID] = true
		}
	}
	known := make(map[int64]bool, len(existing))
	for _, row := range existing {
		known[row.ID] = true
	}
	knownClaims := make(map[int64]bool, len(existingClaims))
	for _, claim := range existingClaims {
		knownClaims[claim.ID] = true
	}
	var refusals, claimRefusals []string
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
	for _, id := range claimIDs {
		switch {
		case exportableClaims[id]:
		case !knownClaims[id]:
			claimRefusals = append(claimRefusals, notFoundClaimRefusal(id))
		default:
			claimRefusals = append(claimRefusals, fmt.Sprintf(
				"Travel claim %d cannot be exported: only an approved travel claim that owes somebody something can be", id))
		}
	}
	return refusalsOf(refusals, claimRefusals)
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
// employee, and a column name that moved with the reader's language would break
// the import the first time somebody switched it.
//
// Unit and Purpose lead, because the file is a file of *lines* and the first
// thing a reader needs is which unit a line belongs to: 'expense 2001' for a
// standalone one, 'claim 1012' for a line of a trip, with the trip's purpose
// beside it.
var csvHeader = []string{
	"Unit", "Purpose", "Employee", "User id", "Date", "Kind", "Description",
	"Category", "Currency", "Gross", "VAT", "Owed", "Project code",
}

// The two words the unit cell is built from. They are the module's own
// vocabulary rather than the contract's status values, and a payroll system
// keys its import on them, so they are here once.
const (
	csvUnitExpense = "expense"
	csvUnitClaim   = "claim"
)

// reimbursementCSV is rows as the payroll file, with every name already
// resolved (namesFor). Rows come out by the person's display name, then by
// date and id: a payroll file is read by a person, and the database's own
// order is by a uuid nobody can read.
func reimbursementCSV(rows []store.ListReimbursementRowsRow, names entryNames) ([]byte, error) {
	ordered := slices.Clone(rows)
	slices.SortFunc(ordered, func(a, b store.ListReimbursementRowsRow) int {
		return cmp.Or(
			strings.Compare(displayNameOf(a.ExpensesEntry.UserID, names), displayNameOf(b.ExpensesEntry.UserID, names)),
			a.ExpensesEntry.EntryDate.Time.Compare(b.ExpensesEntry.EntryDate.Time),
			cmp.Compare(a.ExpensesEntry.ID, b.ExpensesEntry.ID),
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

// csvCells is one line as its row of the file. Amounts are rendered from the
// exact decimals the columns hold, never from a float, and with the decimal
// comma the file's readers expect; a VAT, a purpose or a project code there is
// none of is an empty cell rather than a zero or a dash.
func csvCells(row store.ListReimbursementRowsRow, names entryNames) ([]string, error) {
	entry := row.ExpensesEntry
	gross, err := ratFromNumeric(entry.GrossAmount)
	if err != nil {
		return nil, err
	}
	vat, err := ratPtrFromNumeric(entry.VatAmount)
	if err != nil {
		return nil, err
	}
	unit, purpose := fmt.Sprintf("%s %d", csvUnitExpense, entry.ID), ""
	if entry.ClaimID != nil {
		unit = fmt.Sprintf("%s %d", csvUnitClaim, *entry.ClaimID)
		if row.ClaimPurpose != nil {
			purpose = *row.ClaimPurpose
		}
	}
	// A per diem day carries no description of its own unless its owner wrote
	// one, and no category at all: what the day *is* is its type, which is what
	// the file says instead. Everything else keeps what somebody typed.
	description, category := entry.Description, ""
	if entry.Kind == kindPerDiem {
		if entry.PerDiemType != nil {
			description = *entry.PerDiemType
		}
	} else if entry.CategoryID != nil {
		if c, ok := names.categories[*entry.CategoryID]; ok {
			category = c.Name
		}
	}
	// The project's code, and nothing at all when the entry is on none, when
	// the directory no longer lists it, or when this installation has no
	// projects module. The column stays either way, so a payroll system need
	// not know which modules an installation runs.
	project := ""
	if entry.ProjectID != nil {
		if p, ok := names.projects[*entry.ProjectID]; ok {
			project = p.Code
		}
	}
	return []string{
		unit,
		purpose,
		displayNameOf(entry.UserID, names),
		entry.UserID.String(),
		entry.EntryDate.Time.Format(time.DateOnly),
		entry.Kind,
		description,
		category,
		entry.Currency,
		csvAmount(gross),
		csvAmountPtr(vat),
		csvAmount(owedToEmployee(entry, gross)),
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
