package staff

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is absence's history (design S21, plan pre-flight B1 and B2,
// reading 3). An absence or holiday-adjustment event names what it is about
// (absenceId, adjustmentId; delivery B's cases add caseId) and carries its
// fields with, beside them, each field's retention class as the module wrote
// it at the time — classes, a map — since one name means different data on
// different rows: an absence's type is class C on a ferie row and class E,
// health data, on a sick or child-sick one. payload_classes is the map's
// union. None of these names enters fieldClasses or fieldGroups: the erase
// blanks such an event by its own map (blankPayload), and shapePayload shapes
// it by the absence groups — every type value, before and after, by that
// type's own visibility, looked up by id, an unknown one failing closed.

// The absence events of delivery B's Task 3. Each names the absence
// (absenceId) or the adjustment (adjustmentId), its fields before and after,
// and their classes.
const (
	eventAbsenceRequested  = "absence_requested"
	eventAbsenceRegistered = "absence_registered"
	eventAbsenceUpdated    = "absence_updated"
	eventAbsenceDeleted    = "absence_deleted"
	eventAbsenceCancelled  = "absence_cancelled"

	// The decisions (Task 4): an approval — by the person themselves, the
	// self-approver's, absence_self_approved (design S12's self_approved,
	// provisional) — a rejection, and HR's correction of a decided or
	// registered row. A decider's cancel is absence_cancelled.
	eventAbsenceApproved     = "absence_approved"
	eventAbsenceSelfApproved = "absence_self_approved"
	eventAbsenceRejected     = "absence_rejected"
	eventAbsenceCorrected    = "absence_corrected"

	// eventAbsenceChanged is the kind an absence event of a row shown as
	// "leave" is listed under to its reader (review H1): requested and
	// registered would say which types an HR-only row can be. It is never
	// stored.
	eventAbsenceChanged = "absence_changed"

	// eventAbsenceSplit is a row a sykmelding was registered over (Task 5):
	// approved ferie split around a 100 % sykmelding — cancelled when wholly
	// covered — or an egenmelding trimmed, removed when wholly covered
	// (readings 24, 29). The part after the sykmelding is a new row,
	// splitAbsenceId.
	eventAbsenceSplit = "absence_split"

	eventHolidayAdjusted          = "holiday_adjusted"
	eventHolidayAdjustmentDeleted = "holiday_adjustment_deleted"
	eventCarryOverRecorded        = "carry_over_recorded"
)

// absenceEventKinds are the absence family: listed to everyone who reads the
// person's history (seesEvents) although a sick row's event touches class E
// (plan pre-flight I6) — each field then shaped by its group, so a manager
// and HR read the dates and the degree and a sick type shown to them, as §5
// lets them read the rows themselves. Any other event touching E stays the
// health group's.
var absenceEventKinds = []string{
	eventAbsenceRequested, eventAbsenceRegistered, eventAbsenceUpdated, eventAbsenceDeleted, eventAbsenceCancelled,
	eventAbsenceApproved, eventAbsenceSelfApproved, eventAbsenceRejected, eventAbsenceCorrected,
	eventAbsenceSplit,
	eventCaseOpened, eventCaseUpdated, eventCaseClosed, eventCaseMerged, eventMilestoneDone, eventMilestoneWaived,
}

// The sick-leave case's events (Task 5): every field class E, each carrying
// the case's types (typeIds) as an absence event does, listed to everyone who
// reads the person's history and shaped per §5's case rows — the
// accommodation note to the person, the manager and HR, a milestone's note
// and a waiver's reason to the person and staff:personal-data alone. A
// milestone's note's own event is not of the absence family (review L4):
// touching E, it is listed to the health group's readers alone. case_merged
// names the case a merge took in (mergedCaseId).
const (
	eventCaseOpened           = "case_opened"
	eventCaseUpdated          = "case_updated"
	eventCaseClosed           = "case_closed"
	eventCaseMerged           = "case_merged"
	eventMilestoneDone        = "milestone_done"
	eventMilestoneWaived      = "milestone_waived"
	eventMilestoneNoteUpdated = "milestone_note_updated"
)

// The withdrawal of egenmelding and its lifting (ftrl. § 8-27, Task 5): class
// E, listed only to the readers of the care-day fields — the person,
// staff:manage, staff:personal-data — never to a manager (plan pre-flight
// I6). They name no row: their payload carries selfCertWithdrawal.
const (
	eventSelfCertWithdrawn = "self_cert_withdrawn"
	eventSelfCertRestored  = "self_cert_restored"
)

// careEventKinds are the events listed to the readers of the care-day
// fields alone.
var careEventKinds = []string{eventSelfCertWithdrawn, eventSelfCertRestored}

// The absence groups' field names, as an absence event and the contract name
// them.
const (
	fieldType   = "type"
	fieldDegree = "degree"
	// The employee's demand to postpone holiday for sickness and the
	// paragraph it falls under (ferieloven § 9 (1) or (2)), on an
	// absence_split event of the ferie it split (Task 5 review H1).
	fieldDemandedOn     = "demandedOn"
	fieldPostponedUnder = "postponedUnder"
)

// absenceFieldClass is the class of an absence event's field (Global
// Constraints "Event classes"): the note and the decision note D; the type
// and the degree E when the row is health data (a sick or child-sick type,
// on either side of a change), else C; the dates, the percent, the hours and
// the status C.
func absenceFieldClass(name string, health bool) string {
	switch name {
	case "note", "decisionNote":
		return "D"
	case fieldDemandedOn, fieldPostponedUnder:
		// The demand to postpone holiday for sickness (ferieloven § 9) says
		// the person was sick: class E whatever the row.
		return "E"
	case fieldType, fieldDegree:
		if health {
			return "E"
		}
	}
	return "C"
}

// adjustmentFieldClass is the class of an adjustment event's field: its note
// D, the rest — the year, the days, the reason, the year it comes from — C.
func adjustmentFieldClass(name string) string {
	if name == "note" {
		return "D"
	}
	return "C"
}

// classedPayload is an event's payload naming idName = id, with c's fields
// and each one's class by classOf, and the classes' union.
func classedPayload(idName string, id int64, c changes, classOf func(string) string) (map[string]any, []string) {
	classes := make(map[string]string, len(c))
	var union []string
	for name := range c {
		class := classOf(name)
		classes[name] = class
		if !slices.Contains(union, class) {
			union = append(union, class)
		}
	}
	slices.Sort(union)
	return map[string]any{idName: id, "fields": c, "classes": classes}, union
}

// absenceEventPayload is an absence event's payload from before to after
// (either nil: a creation, a deletion) and its payload_classes; health is
// whether the type on either side is health data.
func absenceEventPayload(id int64, before, after map[string]json.RawMessage, health bool) (map[string]any, []string) {
	return classedPayload("absenceId", id, diffFields(before, after), func(name string) string { return absenceFieldClass(name, health) })
}

// adjustmentEventPayload is a holiday adjustment event's payload and its
// payload_classes.
func adjustmentEventPayload(id int64, before, after map[string]json.RawMessage) (map[string]any, []string) {
	return classedPayload("adjustmentId", id, diffFields(before, after), adjustmentFieldClass)
}

// recordClassedEvent records kind for personID with payload and its classes;
// nothing when the payload names no field, since a write that changed
// nothing has nothing to say.
func (s *server) recordClassedEvent(ctx context.Context, txq *store.Queries, personID int64, kind string, who actor, payload map[string]any, classes []string) error {
	if c, _ := payload["fields"].(changes); len(c) == 0 {
		return nil
	}
	return s.recordEvent(ctx, txq, &personID, kind, who, payload, classes)
}

// typeVisibilities is the absence types' visibility by id — absent or
// hr_only — which an absence event's type values are shaped by.
type typeVisibilities map[int64]string

// plain is the types shown as themselves to every reader of types: those of
// visibility absent.
func (t typeVisibilities) plain() []int64 {
	out := []int64{}
	for id, v := range t {
		if v == visibilityAbsent {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// hidesLeaveRows is whether the absence events of a row a reads as "leave"
// are left out of the history a reads (leak 2): to a reader of types who
// does not read HR-only ones. They read the row's dates already; the
// events' count and kinds — one registration, against a request and its
// decision — would tell the HR-only notification from an HR-only request.
func (a audience) hidesLeaveRows() bool { return a.reads(groupAbsenceType) && !a.readsHROnlyTypes() }

// readTypeVisibilities is every type's visibility, hidden ones included.
func readTypeVisibilities(ctx context.Context, q *store.Queries) (typeVisibilities, error) {
	rows, err := q.ListAbsenceTypes(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("staff: read the absence types' visibility: %w", err)
	}
	out := make(typeVisibilities, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Visibility
	}
	return out, nil
}

// caseFieldGroup is a case event field's group (design §5's case rows): the
// days, the close, the estimate, a merge and the milestones' facts the
// case's readers'; the accommodation note its own group's; a milestone's
// note and a waiver's reason — never a diagnosis, yet free text about a
// sickness (review L5) — the health group's; a name it does not know
// nobody's.
func caseFieldGroup(name string) fieldGroup {
	switch name {
	case "firstDay", "lastDay", "closedAt", "maxBenefitEstimate", "doneAt", "waived", "mergedCaseId":
		return groupAbsenceDetail
	case "accommodationNote":
		return groupAccommodation
	case "note", "waivedReason":
		return groupHealth
	}
	return groupNobody
}

// withdrawalFieldGroup is a withdrawal event field's group: the care-day
// fields'.
func withdrawalFieldGroup(name string) fieldGroup {
	if name == "until" || name == "reason" {
		return groupCareDays
	}
	return groupNobody
}

// absenceFieldGroup is an absence event field's group: the dates and the
// status the absence group's, the type the type's, the rest the detail's; a
// name it does not know nobody's.
func absenceFieldGroup(name string) fieldGroup {
	switch name {
	case "firstDay", "lastDay", "status", "selfApproved":
		return groupAbsence
	case fieldType:
		return groupAbsenceType
	case "percent", "hours", "note", fieldDegree, "decisionNote", fieldDemandedOn, fieldPostponedUnder:
		return groupAbsenceDetail
	}
	return groupNobody
}

// adjustmentFieldGroup is an adjustment event field's group: what the account
// shows — its readers' — and the note, HR's own.
func adjustmentFieldGroup(name string) fieldGroup {
	switch name {
	case "year", "days", "reason", "fromYear":
		return groupAbsenceDetail
	case "note":
		return groupHRNotes
	}
	return groupNobody
}

// leaveLabel is what an HR-only type shows to a reader who may read absence
// types but not that one (design §5): leave.
const leaveLabel = "leave"

// typeShown is one type value as a reads it: the value when a reads every
// type (the person, staff:personal-data) or the type is one everybody may be
// shown; "leave" for an HR-only type, or one the lookup does not know (fail
// closed).
func typeShown(value any, a audience, types typeVisibilities) any {
	if a.readsHROnlyTypes() {
		return value
	}
	if id, ok := typeID(value); ok && types[id] == visibilityAbsent {
		return value
	}
	return leaveLabel
}

// typeID is a stored type value as an id.
func typeID(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), float64(int64(v)) == v
	case json.Number:
		id, err := strconv.ParseInt(string(v), 10, 64)
		return id, err == nil
	}
	return 0, false
}

// shapeClassedFields is a classed event's fields as a reads them, by group:
// a field of a group a reads kept — a type value each side by its own
// visibility — one of HR's notes left out, anything else "changed".
func shapeClassedFields(fields map[string]any, groupOf func(string) fieldGroup, a audience, types typeVisibilities) map[string]any {
	out := make(map[string]any, len(fields))
	for name, value := range fields {
		g := groupOf(name)
		switch {
		case !a.reads(g) && g == groupHRNotes:
		case !a.reads(g):
			out[name] = changed
		case g == groupAbsenceType:
			change, _ := value.(map[string]any)
			shaped := map[string]any{}
			for side, v := range change {
				shaped[side] = typeShown(v, a, types)
			}
			out[name] = shaped
		default:
			out[name] = value
		}
	}
	return out
}

// rowShownAsLeave is whether a reads an absence event's row as "leave"
// (review H1): a reader of types who does not read HR-only ones, and a row
// whose type on either side is HR-only, or one the module cannot place — an
// event naming no types included (fail closed).
func rowShownAsLeave(typeIDs *[]int64, a audience, types typeVisibilities) bool {
	if !a.reads(groupAbsenceType) || a.readsHROnlyTypes() {
		return false
	}
	if typeIDs == nil || len(*typeIDs) == 0 {
		return true
	}
	for _, id := range *typeIDs {
		if types[id] != visibilityAbsent {
			return true
		}
	}
	return false
}

// asLeaveFields is an absence event's shaped fields as a row shown as
// "leave" carries them: no hours — an hour type among the HR-only ones would
// otherwise stand out — and a registration read as an approval.
func asLeaveFields(fields map[string]any) map[string]any {
	delete(fields, "hours")
	if change, ok := fields["status"].(map[string]any); ok {
		for side, v := range change {
			if v == absenceRegistered {
				change[side] = absenceApproved
			}
		}
	}
	return fields
}

// absenceEventShownAsLeave is whether an absence event of kind is listed to
// a under the neutral absence_changed: its row is shown as "leave".
func absenceEventShownAsLeave(kind string, stored []byte, a audience, types typeVisibilities) bool {
	if !slices.Contains(absenceEventKinds, kind) {
		return false
	}
	var payload struct {
		TypeIDs *[]int64 `json:"typeIds"`
	}
	if json.Unmarshal(stored, &payload) != nil {
		return rowShownAsLeave(nil, a, types)
	}
	return rowShownAsLeave(payload.TypeIDs, a, types)
}
