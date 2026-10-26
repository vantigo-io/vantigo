package planning

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The terms of an assignment (design S14) and the dates derived from them
// (S15): the reads every response shares. The earliest release and the
// handover start are computed on read and never stored — whenever a terms
// row exists, on a tentative row too, so the planner sees what confirming
// would bind (plan reading 1); they bind — the matcher and the non-compete —
// on a confirmed row alone. The terms themselves are planning:manage's; the
// derived dates are everyone's who reads the row.

// The units a notice period is counted in (S14), as Staff's terms carry
// notice.
const (
	noticeDays   = "days"
	noticeWeeks  = "weeks"
	noticeMonths = "months"
)

// noticeUnits is what noticeUnit takes, as the contract names it.
var noticeUnits = []string{noticeDays, noticeWeeks, noticeMonths}

// derivedDates is S15 for row under t: the earliest release, the latest of
// the row's to date, the minimum term and — when notice was given and the
// period is set — the notice's last day, noticeGivenOn plus the period (days
// as days, weeks as seven, months by Staff's month rule, monthsAfter); and
// the handover start, the release less handoverDays when set (0 is the
// release day itself). With no terms the release is the to date and there
// is no handover.
func derivedDates(row store.PlanningAllocation, t *store.PlanningAllocationTerm) (time.Time, *time.Time) {
	release := civil(row.ToDate)
	if t == nil {
		return release, nil
	}
	if t.MinimumUntil.Valid {
		release = latestOf(release, civil(t.MinimumUntil))
	}
	if t.NoticeGivenOn.Valid && t.NoticeCount != nil && t.NoticeUnit != nil {
		given, n := civil(t.NoticeGivenOn), int(*t.NoticeCount)
		var end time.Time
		switch *t.NoticeUnit {
		case noticeWeeks:
			end = addDays(given, 7*n)
		case noticeMonths:
			end = monthsAfter(given, n)
		default:
			end = addDays(given, n)
		}
		release = latestOf(release, end)
	}
	if t.HandoverDays == nil {
		return release, nil
	}
	handover := addDays(release, -int(*t.HandoverDays))
	return release, &handover
}

// termsByAllocation is the terms rows of ids, by allocation id; an
// allocation with none is absent. q may be the pool's or a transaction's;
// nothing is asked for an empty batch.
func termsByAllocation(ctx context.Context, q *store.Queries, ids []int64) (map[int64]store.PlanningAllocationTerm, error) {
	out := map[int64]store.PlanningAllocationTerm{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.TermsOfAllocations(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("planning: read the allocations' terms: %w", err)
	}
	for _, t := range rows {
		out[t.AllocationID] = t
	}
	return out, nil
}

// applyTerms sets the derived dates (S15) on every allocation of out and,
// when withTerms — the caller holds planning:manage — the terms whole, the
// customers of a non-compete named through the customer directory in one
// batch (an id it does not resolve is the id alone, rule 4; a directory read
// that fails is logged and every customer answered as its id alone, since a
// write's answer follows its commit and must not fail it). It reads the
// terms in one query with no lock, outside any transaction; every response
// that answers allocations calls it right after allocationResponse or
// allocationsView.
func (s *server) applyTerms(ctx context.Context, out []gen.PlanningAllocation, withTerms bool) error {
	if len(out) == 0 {
		return nil
	}
	ids := make([]int64, len(out))
	for i, a := range out {
		ids[i] = a.Id
	}
	terms, err := termsByAllocation(ctx, store.New(s.deps.Pool), ids)
	if err != nil {
		return err
	}
	names := map[int32]string{}
	if withTerms {
		var customers []int32
		for _, t := range terms {
			customers = append(customers, t.NonCompeteCustomerIds...)
		}
		if names, err = s.customerNames(ctx, slices.Compact(slices.Sorted(slices.Values(customers)))); errors.Is(err, errCallInsideTransaction) {
			return err
		} else if err != nil {
			s.deps.Logger.WarnContext(ctx, "planning: the non-compete's customers could not be named", "error", err.Error())
			names = map[int32]string{}
		}
	}
	for i := range out {
		a := &out[i]
		var t *store.PlanningAllocationTerm
		if row, ok := terms[a.Id]; ok {
			t = &row
		}
		release, handover := derivedDates(store.PlanningAllocation{ToDate: dayParam(dayOf(a.ToDate))}, t)
		a.EarliestRelease, a.HandoverStart = apiDate(release), nil
		if handover != nil {
			a.HandoverStart = ptrTo(apiDate(*handover))
		}
		a.Terms = nil
		if withTerms && t != nil {
			a.Terms = ptrTo(termsView(*t, names))
		}
	}
	return nil
}

// termsView is a terms row as the contract answers it to planning:manage
// (S14): the customers of the non-compete named when the directory resolves
// them, never null.
func termsView(t store.PlanningAllocationTerm, customers map[int32]string) gen.PlanningTerms {
	out := gen.PlanningTerms{
		NoticeGivenOn:            optionalDate(t.NoticeGivenOn),
		MinimumUntil:             optionalDate(t.MinimumUntil),
		ReplacementNeedsApproval: t.ReplacementNeedsApproval,
		NonCompeteUntil:          optionalDate(t.NonCompeteUntil),
		NonCompeteCustomers:      make([]gen.PlanningCustomerRef, 0, len(t.NonCompeteCustomerIds)),
		Reference:                t.Reference,
		Revision:                 t.Revision,
		CreatedAt:                t.CreatedAt,
		UpdatedAt:                t.UpdatedAt,
	}
	if t.NoticeCount != nil {
		out.NoticeCount = ptrTo(int32(*t.NoticeCount))
	}
	if t.NoticeUnit != nil {
		out.NoticeUnit = ptrTo(gen.PlanningNoticeUnit(*t.NoticeUnit))
	}
	if t.HandoverDays != nil {
		out.HandoverDays = ptrTo(int32(*t.HandoverDays))
	}
	for _, id := range t.NonCompeteCustomerIds {
		ref := gen.PlanningCustomerRef{Id: id}
		if name, ok := customers[id]; ok {
			ref.Name = &name
		}
		out.NonCompeteCustomers = append(out.NonCompeteCustomers, ref)
	}
	return out
}

// optionalDate is a nullable date column as the contract's optional date.
func optionalDate(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return ptrTo(apiDate(civil(d)))
}
