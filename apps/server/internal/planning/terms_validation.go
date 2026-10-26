package planning

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the terms' field rules (design S14, §4.7, plan reading 3) and
// what an event records of the terms (S23). The field rules are pure; the customer
// directory's read of the non-compete's ids is the handler's, before its
// transaction (terms_write.go).

// The bounds of the terms' fields (S14).
const (
	minNoticeCount       = 1
	maxNoticeCount       = 365
	maxHandoverDays      = 90
	maxNonCompeteIDs     = 20
	maxTermsReferenceLen = 200
)

// termsFields is a valid PlanningTermsInput: the dates at midnight UTC, nil
// when left out; the non-compete's customer ids collapsed and ascending,
// empty for none; the reference trimmed, nil when empty.
type termsFields struct {
	noticeCount                 *int16
	noticeUnit                  *string
	noticeGivenOn, minimumUntil *time.Time
	replacementNeedsApproval    bool
	handoverDays                *int16
	nonCompeteUntil             *time.Time
	nonCompeteCustomerIDs       []int32
	reference                   *string
}

// parseTerms validates the terms' body on its own (S14): every rule a field
// error on its own field, every failure reported at once. The notice is a
// count from 1 to 365 and a unit, both or neither, the missing one blamed;
// noticeGivenOn needs a notice period; handoverDays is 0 to 90; the
// non-compete is an until date with at least one customer id and at most
// twenty — half of it binds nothing and would only mislead (plan reading 3).
// Whether each id is a customer is the handler's read.
func parseTerms(body gen.PlanningTermsInput) (termsFields, fieldErrors) {
	var f fieldErrors
	t := termsFields{
		noticeGivenOn:            optionalDay(body.NoticeGivenOn),
		minimumUntil:             optionalDay(body.MinimumUntil),
		nonCompeteUntil:          optionalDay(body.NonCompeteUntil),
		replacementNeedsApproval: body.ReplacementNeedsApproval != nil && *body.ReplacementNeedsApproval,
		nonCompeteCustomerIDs:    []int32{},
	}
	if body.NoticeCount != nil {
		if n := *body.NoticeCount; n < minNoticeCount || n > maxNoticeCount {
			f.add("noticeCount", fmt.Sprintf("noticeCount must be from %d to %d", minNoticeCount, maxNoticeCount))
		} else {
			t.noticeCount = ptrTo(int16(n))
		}
	}
	t.noticeUnit = enumField(&f, "noticeUnit", (*string)(body.NoticeUnit), noticeUnits)
	switch {
	case body.NoticeCount != nil && body.NoticeUnit == nil:
		f.add("noticeUnit", "noticeUnit is required with noticeCount: days, weeks or months")
	case body.NoticeCount == nil && body.NoticeUnit != nil:
		f.add("noticeCount", "noticeCount is required with noticeUnit")
	}
	if body.NoticeGivenOn != nil && (body.NoticeCount == nil || body.NoticeUnit == nil) {
		f.add("noticeGivenOn", "noticeGivenOn needs a notice period: noticeCount and noticeUnit")
	}
	if body.HandoverDays != nil {
		if n := *body.HandoverDays; n < 0 || n > maxHandoverDays {
			f.add("handoverDays", fmt.Sprintf("handoverDays must be from 0 to %d", maxHandoverDays))
		} else {
			t.handoverDays = ptrTo(int16(n))
		}
	}
	var ids []int32
	if body.NonCompeteCustomerIds != nil {
		ids = *body.NonCompeteCustomerIds
	}
	switch {
	case len(ids) > maxNonCompeteIDs:
		f.add("nonCompeteCustomerIds", fmt.Sprintf("A non-compete names at most %d customers", maxNonCompeteIDs))
	case len(ids) > 0 && body.NonCompeteUntil == nil:
		f.add("nonCompeteUntil", "nonCompeteUntil is required when the non-compete names a customer")
	case len(ids) == 0 && body.NonCompeteUntil != nil:
		f.add("nonCompeteCustomerIds", "A non-compete names at least one customer it is toward")
	case len(ids) > 0:
		t.nonCompeteCustomerIDs = slices.Compact(slices.Sorted(slices.Values(ids)))
	}
	t.reference = textField(&f, "reference", body.Reference, maxTermsReferenceLen)
	return t, f
}

// unknownCustomers is the field errors of the ids of t's non-compete the
// customer directory did not resolve (names, read before the transaction),
// each named on nonCompeteCustomerIds.
func unknownCustomers(t termsFields, names map[int32]string) fieldErrors {
	var f fieldErrors
	for _, id := range t.nonCompeteCustomerIDs {
		if _, ok := names[id]; !ok {
			f.add("nonCompeteCustomerIds", fmt.Sprintf("Customer %d is not one the customer directory knows", id))
		}
	}
	return f
}

// optionalDay is an optional contract date at midnight UTC.
func optionalDay(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	return ptrTo(dayOf(*d))
}

// optionalDayParam is an optional day as a nullable date parameter.
func optionalDayParam(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return dayParam(*d)
}

// termsRow is t as the row it would leave, for comparing with the row
// before (termsChanges): what a PUT writes and what it records.
func termsRow(allocationID int64, t termsFields) store.PlanningAllocationTerm {
	return store.PlanningAllocationTerm{
		AllocationID: allocationID, NoticeCount: t.noticeCount, NoticeUnit: t.noticeUnit,
		NoticeGivenOn: optionalDayParam(t.noticeGivenOn), MinimumUntil: optionalDayParam(t.minimumUntil),
		ReplacementNeedsApproval: t.replacementNeedsApproval, HandoverDays: t.handoverDays,
		NonCompeteUntil: optionalDayParam(t.nonCompeteUntil), NonCompeteCustomerIds: t.nonCompeteCustomerIDs,
		Reference: t.reference,
	}
}

// recordedTerms is a terms row's fields as an event records them, by the
// contract's input names, each present only when set: the dates as civil
// dates, the customers as the list of ids, replacementNeedsApproval only
// when true. nil is no terms.
func recordedTerms(t *store.PlanningAllocationTerm) map[string]any {
	v := map[string]any{}
	if t == nil {
		return v
	}
	if t.NoticeCount != nil {
		v["noticeCount"] = *t.NoticeCount
	}
	if t.NoticeUnit != nil {
		v["noticeUnit"] = *t.NoticeUnit
	}
	for name, d := range map[string]pgtype.Date{
		"noticeGivenOn": t.NoticeGivenOn, "minimumUntil": t.MinimumUntil, "nonCompeteUntil": t.NonCompeteUntil,
	} {
		if d.Valid {
			v[name] = civil(d).Format(time.DateOnly)
		}
	}
	if t.ReplacementNeedsApproval {
		v["replacementNeedsApproval"] = true
	}
	if t.HandoverDays != nil {
		v["handoverDays"] = *t.HandoverDays
	}
	if len(t.NonCompeteCustomerIds) > 0 {
		v["nonCompeteCustomerIds"] = slices.Clone(t.NonCompeteCustomerIds)
	}
	if t.Reference != nil {
		v["reference"] = *t.Reference
	}
	return v
}

// termsChanges is the fields that differ between before and after (either
// nil for no terms), their values before and after: terms_set's and
// terms_cleared's payload (S23).
func termsChanges(before, after *store.PlanningAllocationTerm) changes {
	b, a := recordedTerms(before), recordedTerms(after)
	out := changes{}
	for name := range b {
		if !reflect.DeepEqual(b[name], a[name]) {
			out[name] = fieldChange{From: b[name], To: a[name]}
		}
	}
	for name := range a {
		if _, seen := b[name]; !seen {
			out[name] = fieldChange{To: a[name]}
		}
	}
	return out
}
