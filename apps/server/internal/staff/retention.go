package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the retention run's erase (design S15 as amended by the A2
// pre-flight; plan Task 5, readings 3, 4, 19-22): every column of the
// register classed, the three schedules as three due queries over what
// remains, and one person's erases in one transaction on the lease's
// connection. The worker and the endpoint that run it are
// retention_worker.go's; the ledger that finishes the objects is
// object_deletions.go's.
//
// The classes are S15's: A pay and contract documentation, kept five years
// after the year the employment ended; C who was employed when, kept; D
// personnel administration, erased a grace period after the last employment
// ended; E health, erased healthRetentionDays after it — or, for what a
// sick-leave case, a child-sick row or a withdrawal owns, after their own
// close, year or lapse when that comes first; F the hold, which stops
// every erase and is erased by none. columnClasses is the erase's one source:
// the statements that null a class's columns are built from it, a test walks
// the database's columns against it, and another holds fieldClasses — what
// an event's payload_classes say — to it.
//
// An erase never calls out of the module: a document's row is deleted and its
// object's key queued in the ledger in the same transaction, and the ledger
// removes the object afterwards, holding no connection.

// classOfCategory is a documents column's class: the category's. A document
// is erased whole — its row deleted, its object queued — by the erase of its
// category's class, so its columns share that class row by row.
const classOfCategory = "category"

// columnClasses is the retention class of every column of the tables the
// erase reaches, keyed table.column (design §3.1-§3.3, §3.6, S15). Ids,
// foreign keys, revisions and timestamps are C: they say that a row existed
// and when, which is who-was-employed-when.
var columnClasses = map[string]string{
	// The person (§3.1). The national identity number's kind stays, so the
	// record still says what kind of number was on file; the number, its MAC
	// and a foreign id's country go. The self-certification withdrawal is
	// class E on delivery B's schedule — nothing writes it before B, and no
	// A2 erase reaches it. The legal hold is F.
	"people.id": "C", "people.employee_number": "C", "people.given_name": "C", "people.family_name": "C",
	"people.display_name": "C", "people.birth_date": "C", "people.gender": "C", "people.national_id_kind": "C",
	"people.national_id_sealed": "D", "people.national_id_mac": "D", "people.national_id_country": "D",
	"people.work_email": "D", "people.phone_work": "D", "people.phone_mobile": "D", "people.phone_private": "D",
	"people.private_email": "D", "people.street": "D", "people.postal_code": "D", "people.city": "D", "people.country": "D",
	"people.work_country": "C", "people.preferred_language": "C",
	"people.bank_account_sealed": "D", "people.bank_account_last4": "D", "people.bank_account_kind": "D",
	"people.children_under_12": "D", "people.sole_carer": "D", "people.chronically_ill_children": "D",
	"people.self_cert_withdrawn_until": "E", "people.self_cert_withdrawn_reason": "E", "people.self_cert_withdrawn_from": "E",
	"people.provision_account": "C", "people.provision_from": "C", "people.account_email_kind": "C", "people.account_revision": "C",
	"people.account_kind": "C",
	"people.legal_hold":   "F", "people.legal_hold_reason": "F", "people.legal_hold_by_user_id": "F", "people.legal_hold_at": "F",
	"people.erased_at": "C", "people.reassessed_at": "C", "people.reassessed_by_user_id": "C",
	"people.notes": "D", "people.revision": "C", "people.created_at": "C", "people.updated_at": "C",

	// The next of kin are class D whole (§3.1): every column, so the D erase
	// deletes the rows.
	"next_of_kin.id": "D", "next_of_kin.person_id": "D", "next_of_kin.position": "D", "next_of_kin.name": "D",
	"next_of_kin.relationship": "D", "next_of_kin.phone": "D", "next_of_kin.note": "D",
	"next_of_kin.created_at": "D", "next_of_kin.updated_at": "D",

	// The roles, the employments and the link are class C whole (§3.1,
	// §3.2, §3.6), but for a provider link's two addresses.
	"person_roles.id": "C", "person_roles.person_id": "C", "person_roles.role": "C", "person_roles.from_date": "C",
	"person_roles.to_date": "C", "person_roles.created_at": "C", "person_roles.updated_at": "C",

	"employments.id": "C", "employments.person_id": "C", "employments.employment_number": "C",
	"employments.relationship_type": "C", "employments.business_unit_id": "C", "employments.start_date": "C",
	"employments.end_date": "C", "employments.end_reason": "C", "employments.contract_signed_at": "C",
	"employments.holiday_scheme_id": "C", // delivery B: which scheme the holiday followed
	"employments.revision":          "C", "employments.created_at": "C", "employments.updated_at": "C",

	"identity_links.id": "C", "identity_links.person_id": "C", "identity_links.provider": "C",
	"identity_links.external_id": "C", "identity_links.state": "C", "identity_links.managed": "C",
	"identity_links.blocked_reason": "C", "identity_links.last_error": "C", "identity_links.attempts": "C",
	"identity_links.next_attempt_at": "C", "identity_links.lease_id": "C", "identity_links.lease_until": "C",
	"identity_links.last_synced_at": "C", "identity_links.last_spec_hash": "C", "identity_links.synced_employee_number": "C",
	"identity_links.disable_requested_at": "C",
	// A provider link's (00060): the principal name and the address the
	// provider answered are addresses, D as the person's own work email is;
	// the rest says what the worker did and when.
	"identity_links.external_upn": "D", "identity_links.external_email": "D", "identity_links.announced_at": "C",
	"identity_links.pass_issued_at": "C", "identity_links.reported_codes": "C",
	"identity_links.created_at": "C", "identity_links.updated_at": "C",

	// The terms (§3.3): what the job was is C; the pay, the seniority date
	// and the pay grade are A, kept five years past the end year; the note is
	// HR's, D.
	"employment_terms.id": "C", "employment_terms.employment_id": "C", "employment_terms.valid_from": "C",
	"employment_terms.employment_type": "C", "employment_terms.temporary_ground": "C",
	"employment_terms.expected_end_date": "C", "employment_terms.appointment_form": "C",
	"employment_terms.hired_out_to": "C", "employment_terms.fte_percent": "C",
	"employment_terms.working_hours_scheme": "C", "employment_terms.full_time_hours_per_week": "C",
	"employment_terms.occupation_code": "C", "employment_terms.title": "C", "employment_terms.employee_type": "C",
	"employment_terms.remuneration_type": "A", "employment_terms.pay_components": "A",
	"employment_terms.pay_method": "A", "employment_terms.payday_text": "A",
	"employment_terms.department_id": "C", "employment_terms.location_id": "C",
	"employment_terms.manager_person_id": "C", "employment_terms.cost_centre": "C", "employment_terms.division": "C",
	"employment_terms.working_time_text": "C", "employment_terms.break_minutes": "C",
	"employment_terms.special_hours_text": "C", "employment_terms.overtime_rule_text": "C",
	"employment_terms.probation_end_date": "C", "employment_terms.probation_extension_agreed": "C",
	"employment_terms.probation_extended_to": "C", "employment_terms.probation_extension_reason": "C",
	"employment_terms.notice_employee": "C", "employment_terms.notice_employer": "C",
	"employment_terms.notice_unit": "C", "employment_terms.tariff_reference": "C",
	"employment_terms.holiday_terms_text": "C", "employment_terms.competence_development_text": "C",
	"employment_terms.social_security_text": "C",
	"employment_terms.seniority_date":       "A", "employment_terms.pay_grade": "A",
	"employment_terms.note":     "D",
	"employment_terms.revision": "C", "employment_terms.created_at": "C", "employment_terms.updated_at": "C",

	// A document is its category's class, row by row (§3.6).
	"documents.id": classOfCategory, "documents.person_id": classOfCategory, "documents.category_id": classOfCategory,
	"documents.employment_id": classOfCategory, "documents.object_key": classOfCategory,
	"documents.file_name": classOfCategory, "documents.content_type": classOfCategory,
	"documents.size_bytes": classOfCategory, "documents.valid_to": classOfCategory,
	"documents.signed_at": classOfCategory, "documents.uploaded_by_user_id": classOfCategory,
	"documents.revision": classOfCategory, "documents.created_at": classOfCategory,
	"documents.updated_at": classOfCategory,
	// The case a health document belongs to (delivery B, S15): the case's
	// schedule reaches the document through it; still the category's class.
	"documents.case_id": classOfCategory,

	// Delivery B (Task 6). An absence's days, status and decision are who
	// was away when, C; its note and the decision's note are personnel
	// administration, D; a sick row's degree is health, E. Its type is C on
	// the column — a ferie row's is kept — and the case's schedule writes
	// the reserved absent in place of a sick one, a child-sick one's a year
	// after its year (plan readings 12 and 21): health data by the row, not
	// by the column.
	"absences.id": "C", "absences.person_id": "C", "absences.type_id": "C", "absences.first_day": "C",
	"absences.last_day": "C", "absences.hours": "C", "absences.percent": "C", "absences.status": "C",
	"absences.note": "D", "absences.case_id": "C", "absences.degree_percent": "E",
	"absences.requested_by_user_id": "C", "absences.decided_by_user_id": "C", "absences.decided_at": "C",
	"absences.decision_note": "D", "absences.self_approved": "C",
	"absences.revision": "C", "absences.created_at": "C", "absences.updated_at": "C",

	// A holiday adjustment is the account's history, C; its note is HR's, D.
	"holiday_adjustments.id": "C", "holiday_adjustments.person_id": "C", "holiday_adjustments.year": "C",
	"holiday_adjustments.days": "C", "holiday_adjustments.reason": "C", "holiday_adjustments.from_year": "C",
	"holiday_adjustments.note": "D", "holiday_adjustments.by_user_id": "C",
	"holiday_adjustments.created_at": "C", "holiday_adjustments.updated_at": "C",

	// A sick-leave case keeps its id and its two dates (design §3.5) — and
	// when HR closed it; its notes and estimate are E. A milestone keeps its
	// kind and its date; its facts — done, waived, by whom, why — and its
	// note are E.
	"sick_cases.id": "C", "sick_cases.person_id": "C", "sick_cases.first_day": "C", "sick_cases.last_day": "C",
	"sick_cases.closed_at": "C", "sick_cases.accommodation_note": "E", "sick_cases.max_benefit_estimate": "E",
	"sick_cases.revision": "C", "sick_cases.created_at": "C", "sick_cases.updated_at": "C",

	"sick_case_milestones.id": "C", "sick_case_milestones.case_id": "C", "sick_case_milestones.kind": "C",
	"sick_case_milestones.due_date": "C", "sick_case_milestones.done_at": "E", "sick_case_milestones.done_by_user_id": "E",
	"sick_case_milestones.waived": "E", "sick_case_milestones.waived_reason": "E", "sick_case_milestones.waived_by_user_id": "E",
	"sick_case_milestones.note":     "E",
	"sick_case_milestones.revision": "C", "sick_case_milestones.created_at": "C", "sick_case_milestones.updated_at": "C",

	// The absence types and the holiday schemes are the installation's
	// vocabulary, about nobody: C whole.
	"absence_types.id": "C", "absence_types.key": "C", "absence_types.name_en": "C", "absence_types.name_nb": "C",
	"absence_types.kind": "C", "absence_types.unit": "C", "absence_types.paid": "C", "absence_types.draws_holiday": "C",
	"absence_types.allows_percent": "C", "absence_types.needs_decision": "C", "absence_types.visibility": "C",
	"absence_types.amelding_leave": "C", "absence_types.seeded": "C", "absence_types.hidden": "C", "absence_types.sort": "C",
	"absence_types.revision": "C", "absence_types.created_at": "C", "absence_types.updated_at": "C",

	"holiday_schemes.id": "C", "holiday_schemes.name": "C", "holiday_schemes.entitlement_virkedager": "C",
	"holiday_schemes.extra_from_60_virkedager": "C", "holiday_schemes.main_period_virkedager": "C",
	"holiday_schemes.after_september_virkedager": "C", "holiday_schemes.max_carry_over_virkedager": "C",
	"holiday_schemes.is_default": "C", "holiday_schemes.seeded": "C", "holiday_schemes.archived_at": "C",
	"holiday_schemes.revision": "C", "holiday_schemes.created_at": "C", "holiday_schemes.updated_at": "C",
}

// erasedTo is what an erase writes in place of NULL in a column that cannot
// be null: a milestone that was waived is, once erased, a milestone of
// which nothing is known — not waived.
var erasedTo = map[string]string{"sick_case_milestones.waived": "false"}

// The kinds an erase reports, in the erased event and the run's answer:
// A2's three, delivery B's health schedules (Task 6) — a closed case's, a
// year's child-sick rows', a lapsed withdrawal's — and delivery C's
// pregnancy and health rows' (Task 5b), when its setting is on.
const (
	eraseKindD               = "D"
	eraseKindHealth          = "health"
	eraseKindA               = "A"
	eraseKindCases           = "cases"
	eraseKindChildSick       = "childSick"
	eraseKindWithdrawal      = "withdrawal"
	eraseKindPregnancyHealth = "pregnancyHealth"
)

// eraseKinds is the order the kinds are reported in.
var eraseKinds = []string{eraseKindD, eraseKindHealth, eraseKindA, eraseKindCases, eraseKindChildSick, eraseKindWithdrawal, eraseKindPregnancyHealth}

// healthEraseKinds are the kinds that say the person's health was erased:
// an erased event names them to the health group's readers alone — a
// pregnancy's erase among them, whose kind alone says what the rows were.
var healthEraseKinds = []string{eraseKindHealth, eraseKindCases, eraseKindChildSick, eraseKindWithdrawal, eraseKindPregnancyHealth}

// pregnancyHealthTypes are the seeds erasePregnancyHealthTypes reaches (B's
// open question 11, the owner's answer 7): the pregnancy check-up, pregnancy
// leave, birth leave and a doctor's or dentist's visit — kept as class C, B's
// default, while the setting is off.
var pregnancyHealthTypes = []string{"svangerskapskontroll", "svangerskapspermisjon", "fodselspermisjon", "helse"}

// pregnancyLeaveTypes are the leaves among them erased no earlier than a
// full year after their year ended, whatever healthRetentionDays says (the
// coordinator's decision on review L5): the holiday-pay year after a leave,
// and a payroll re-export of it, still read the leave.
var pregnancyLeaveTypes = []string{"svangerskapspermisjon", "fodselspermisjon"}

// documentEventKinds are the kinds of event about one document: the A erase
// of a person's last employment finds those of a document that is gone by
// them.
var documentEventKinds = []string{eventDocumentAdded, eventDocumentUpdated, eventDocumentRemoved}

// The schedules' constants (S15): the health class, and the years an
// employment's pay documentation outlives the year it ended.
const (
	healthClass  = "E"
	payYearsKept = 5
	// everyRow is the limit a due query asked about one person runs with.
	everyRow = math.MaxInt32
)

// erasedKind is one kind of erase and how many items it removed: each column
// value nulled, each row deleted (a next of kin, a document) and each event
// payload rewritten.
type erasedKind struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// columnsOf is table's columns of class, sorted.
func columnsOf(table, class string) []string {
	var out []string
	for key, c := range columnClasses {
		if t, column, _ := strings.Cut(key, "."); t == table && c == class {
			out = append(out, column)
		}
	}
	slices.Sort(out)
	return out
}

// wholeTable reports whether every column of table is of class: a table whose
// rows that class's erase deletes rather than nulls — the next of kin.
func wholeTable(table, class string) bool {
	found := false
	for key, c := range columnClasses {
		if t, _, _ := strings.Cut(key, "."); t == table {
			if c != class {
				return false
			}
			found = true
		}
	}
	return found
}

// tablesWith is the tables, but documents, that hold a column of class,
// sorted: what a class's erase nulls or deletes.
func tablesWith(class string) []string {
	var out []string
	for key, c := range columnClasses {
		if t, _, _ := strings.Cut(key, "."); c == class && t != "documents" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}

// hasColumn reports whether table has column.
func hasColumn(table, column string) bool {
	_, ok := columnClasses[table+"."+column]
	return ok
}

// fieldsOfClass is the payload fields of class, sorted.
func fieldsOfClass(class string) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(fieldClasses)) {
		if fieldClasses[name] == class {
			out = append(out, name)
		}
	}
	return out
}

// personScope is the rows of table that are one person's ($1), for the D erase.
func personScope(table string) string {
	switch table {
	case "people":
		return "id = $1"
	case "employment_terms":
		return "employment_id IN (SELECT id FROM staff.employments WHERE person_id = $1)"
	}
	return "person_id = $1"
}

// employmentScope is the rows of table that are one employment's ($1), for
// the A erase; the A columns live on the terms alone (the column test holds
// that).
func employmentScope(table string) (string, error) {
	switch table {
	case "employment_terms":
		return "employment_id = $1", nil
	case "employments":
		return "id = $1", nil
	}
	return "", fmt.Errorf("staff: the A erase has no employment scope for %s", table)
}

// eraseColumns erases class in table's rows that scope selects (its $1 is
// arg): a table of that class whole has those rows deleted; otherwise the
// class's columns are nulled — or set to their erasedTo value — where any
// still holds something else, and the row's revision and updated_at move
// on. The statements are built from columnClasses and erasedTo — no request
// reaches them, and each identifier is quoted all the same — and answer the
// items removed: rows deleted, or values erased.
func eraseColumns(ctx context.Context, tx pgx.Tx, table, class, scope string, arg int64, now time.Time) (int64, error) {
	relation := pgx.Identifier{"staff", table}.Sanitize()
	if wholeTable(table, class) {
		tag, err := tx.Exec(ctx, "DELETE FROM "+relation+" WHERE "+scope, arg)
		if err != nil {
			return 0, fmt.Errorf("staff: erase %s's rows of class %s: %w", table, class, err)
		}
		return tag.RowsAffected(), nil
	}
	columns := columnsOf(table, class)
	if len(columns) == 0 {
		return 0, nil
	}
	held := make([]string, len(columns))
	sets := make([]string, 0, len(columns)+2)
	for i, column := range columns {
		quoted := pgx.Identifier{column}.Sanitize()
		to, ok := erasedTo[table+"."+column]
		if !ok {
			to = "NULL"
		}
		held[i] = "(" + quoted + " IS DISTINCT FROM " + to + ")::int"
		sets = append(sets, quoted+" = "+to)
	}
	remaining := strings.Join(held, " + ")
	var values int64
	if err := tx.QueryRow(ctx, "SELECT coalesce(sum("+remaining+"), 0) FROM "+relation+" WHERE "+scope, arg).Scan(&values); err != nil {
		return 0, fmt.Errorf("staff: count %s's values of class %s: %w", table, class, err)
	}
	if values == 0 {
		return 0, nil
	}
	if hasColumn(table, "revision") {
		sets = append(sets, "revision = revision + 1")
	}
	args := []any{arg}
	if hasColumn(table, "updated_at") {
		sets = append(sets, "updated_at = $2::timestamptz")
		args = append(args, now)
	}
	statement := "UPDATE " + relation + " SET " + strings.Join(sets, ", ") + " WHERE (" + scope + ") AND " + remaining + " > 0"
	if _, err := tx.Exec(ctx, statement, args...); err != nil {
		return 0, fmt.Errorf("staff: erase %s's values of class %s: %w", table, class, err)
	}
	return values, nil
}

// retentionPlan is what one cycle's due queries are asked with, from the
// settings and the clock: "today" the settings zone's civil day.
type retentionPlan struct {
	dCutoff      time.Time
	healthCutoff time.Time
	thisYear     int32
	aColumns     []string
	aFields      []string
	// Delivery B's schedules (Task 6): the settings' zone a case closed by
	// HR is dated in, the instant healthRetentionDays before now, the
	// reserved absent type, and the class E columns of a case and of its
	// milestones.
	zone           string
	healthCutoffAt time.Time
	// orphanCutoffAt is sixteen days before healthCutoffAt: an event no
	// case owns any more is due by it, as its case — which closed sixteen
	// days after its last row at the latest — would have been.
	orphanCutoffAt   time.Time
	absentID         int64
	caseColumns      []string
	milestoneColumns []string
	// The two erase settings the owner asked for (delivery C, Task 5b), as
	// the cycle read them: the pregnancy and health rows on the health
	// schedule — their types (pregnancyTypeIDs), the reserved hour type
	// written in place of an hour one, and the zone an event's year is
	// judged in — and a case's erase removing the case itself.
	erasePregnancyHealth bool
	pregnancyTypeIDs     []int64
	absentHoursID        int64
	hrOnlyID             int64
	hrOnlyHoursID        int64
	// reservedIDs are the four reserved types: a value already one of them
	// is erased already (review L2). floorTypeIDs are the pregnancy leaves
	// erased no earlier than floorCutoff, a full year after their year ended
	// (review L5).
	reservedIDs  []int64
	floorTypeIDs []int64
	floorCutoff  time.Time
	loc          *time.Location
	removeCases  bool
}

// planRetention reads the settings on q and derives the cycle's dates in
// zone, the business time zone the caller read before holding anything.
func (s *server) planRetention(ctx context.Context, q *store.Queries, zone *time.Location) (retentionPlan, error) {
	row, err := settings(ctx, q)
	if err != nil {
		return retentionPlan{}, err
	}
	absentID, err := q.AbsentTypeID(ctx)
	if err != nil {
		return retentionPlan{}, fmt.Errorf("staff: read the reserved absent type: %w", err)
	}
	reservedRows, err := q.SeededTypesByKey(ctx, reservedTypes)
	if err != nil {
		return retentionPlan{}, fmt.Errorf("staff: read the reserved types: %w", err)
	}
	reserved := map[string]int64{}
	for _, r := range reservedRows {
		reserved[r.Key] = r.ID
	}
	var reservedIDs []int64
	for _, key := range reservedTypes {
		if reserved[key] == 0 {
			return retentionPlan{}, fmt.Errorf("staff: the reserved type %s is missing", key)
		}
		reservedIDs = append(reservedIDs, reserved[key])
	}
	floorTypeIDs, err := q.SeededTypeIDs(ctx, pregnancyLeaveTypes)
	if err != nil {
		return retentionPlan{}, fmt.Errorf("staff: read the pregnancy leave types: %w", err)
	}
	pregnancyTypeIDs, err := q.SeededTypeIDs(ctx, pregnancyHealthTypes)
	if err != nil {
		return retentionPlan{}, fmt.Errorf("staff: read the pregnancy and health types: %w", err)
	}
	now := s.deps.Clock()
	today := civilToday(now, zone)
	return retentionPlan{
		erasePregnancyHealth: row.ErasePregnancyHealthTypes,
		pregnancyTypeIDs:     pregnancyTypeIDs,
		absentHoursID:        reserved[reservedAbsenceHoursType],
		hrOnlyID:             reserved[reservedHROnlyType],
		hrOnlyHoursID:        reserved[reservedHROnlyHoursType],
		reservedIDs:          reservedIDs,
		floorTypeIDs:         floorTypeIDs,
		floorCutoff:          today.AddDate(-1, 0, 0),
		loc:                  zone,
		removeCases:          row.EraseSickCaseDates,
		dCutoff:              today.AddDate(0, 0, -int(row.RetentionGraceDays)),
		healthCutoff:         today.AddDate(0, 0, -int(row.HealthRetentionDays)),
		thisYear:             int32(today.Year()),
		aColumns:             columnsOf("employment_terms", "A"),
		aFields:              fieldsOfClass("A"),
		zone:                 zone.String(),
		healthCutoffAt:       now.AddDate(0, 0, -int(row.HealthRetentionDays)),
		orphanCutoffAt:       now.AddDate(0, 0, -int(row.HealthRetentionDays)-caseClosesAfter),
		absentID:             absentID,
		caseColumns:          columnsOf("sick_cases", healthClass),
		milestoneColumns:     columnsOf("sick_case_milestones", healthClass),
	}, nil
}

func (p retentionPlan) dueD(person *int64, limit int32) store.DuePeopleDParams {
	return store.DuePeopleDParams{PersonID: person, Cutoff: dayParam(p.dCutoff), RowLimit: limit}
}

func (p retentionPlan) dueHealth(person *int64, limit int32) store.DuePeopleHealthParams {
	return store.DuePeopleHealthParams{PersonID: person, Cutoff: dayParam(p.healthCutoff), Blanked: changed, ReservedIds: p.reservedIDs, RowLimit: limit}
}

func (p retentionPlan) dueCases(person *int64, limit int32) store.DueCasesEParams {
	return store.DueCasesEParams{
		PersonID: person, Zone: p.zone, Cutoff: dayParam(p.healthCutoff), CaseColumns: p.caseColumns,
		MilestoneColumns: p.milestoneColumns, ReservedIds: p.reservedIDs, RemoveCases: p.removeCases, Blanked: changed, RowLimit: limit,
	}
}

func (p retentionPlan) duePregnancyHealth(person *int64, limit int32) store.DuePeoplePregnancyHealthParams {
	return store.DuePeoplePregnancyHealthParams{
		PersonID: person, TypeIds: p.pregnancyTypeIDs, Cutoff: dayParam(p.healthCutoff), Zone: p.zone,
		FloorTypeIds: p.floorTypeIDs, FloorCutoff: dayParam(p.floorCutoff), RowLimit: limit,
	}
}

func (p retentionPlan) dueOrphans(person *int64, limit int32) store.DuePeopleOrphanedSickEventsParams {
	return store.DuePeopleOrphanedSickEventsParams{
		PersonID: person, CutoffAt: p.orphanCutoffAt, Cutoff: dayParam(p.healthCutoff), Blanked: changed, ReservedIds: p.reservedIDs, RowLimit: limit,
	}
}

func (p retentionPlan) dueChildSick(person *int64, limit int32) store.DuePeopleChildSickParams {
	return store.DuePeopleChildSickParams{PersonID: person, Cutoff: dayParam(p.healthCutoff), RowLimit: limit}
}

func (p retentionPlan) dueWithdrawal(person *int64, limit int32) store.DuePeopleWithdrawalParams {
	return store.DuePeopleWithdrawalParams{
		PersonID: person, Cutoff: dayParam(p.healthCutoff), CutoffAt: p.healthCutoffAt, Blanked: changed, RowLimit: limit,
	}
}

func (p retentionPlan) dueA(person *int64, limit int32) store.DueEmploymentsAParams {
	return store.DueEmploymentsAParams{
		BeforeYear: p.thisYear - payYearsKept, PersonID: person,
		AColumns: p.aColumns, AFields: p.aFields, Blanked: changed, DocumentKinds: documentEventKinds, RowLimit: limit,
	}
}

// eraseCycle is one erase cycle on conn, the lease's connection: the people
// something is due for — each schedule's oldest first, at most
// retentionBatch people — and each one's erases in one transaction of its
// own, on conn. One person failing is logged by id and the cycle moves on;
// they are due again next cycle. It answers what was erased, per kind,
// summed. zone is the business time zone the caller read before holding
// conn: nothing here asks the pool for a second connection.
func (s *server) eraseCycle(ctx context.Context, conn *pgxpool.Conn, who actor, zone *time.Location) ([]erasedKind, error) {
	q := store.New(conn)
	plan, err := s.planRetention(ctx, q, zone)
	if err != nil {
		return nil, err
	}
	d, err := q.DuePeopleD(ctx, plan.dueD(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people due for the D erase: %w", err)
	}
	health, err := q.DuePeopleHealth(ctx, plan.dueHealth(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people due for the health erase: %w", err)
	}
	a, err := q.DueEmploymentsA(ctx, plan.dueA(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the employments due for the A erase: %w", err)
	}
	cases, err := q.DueCasesE(ctx, plan.dueCases(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the cases due for the health erase: %w", err)
	}
	childSick, err := q.DuePeopleChildSick(ctx, plan.dueChildSick(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people due for the child-sick erase: %w", err)
	}
	withdrawal, err := q.DuePeopleWithdrawal(ctx, plan.dueWithdrawal(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people due for the withdrawal's erase: %w", err)
	}
	people := slices.Concat(d, health)
	for _, row := range a {
		people = append(people, row.PersonID)
	}
	for _, row := range cases {
		people = append(people, row.PersonID)
	}
	orphans, err := q.DuePeopleOrphanedSickEvents(ctx, plan.dueOrphans(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people with orphaned sick events due: %w", err)
	}
	people = slices.Concat(people, childSick, withdrawal, orphans)
	if plan.erasePregnancyHealth {
		pregnancy, err := q.DuePeoplePregnancyHealth(ctx, plan.duePregnancyHealth(nil, retentionBatch))
		if err != nil {
			return nil, fmt.Errorf("staff: select the people due for the pregnancy and health erase: %w", err)
		}
		people = append(people, pregnancy...)
	}
	var due []int64
	for _, id := range people {
		if !slices.Contains(due, id) && len(due) < retentionBatch {
			due = append(due, id)
		}
	}

	totals := map[string]int64{}
	erased, failed := 0, 0
	for _, id := range due {
		if ctx.Err() != nil {
			break
		}
		kinds, err := s.erasePersonRecovering(ctx, conn, id, plan, who)
		if err != nil {
			if ctx.Err() == nil {
				s.logger().ErrorContext(ctx, "staff: erasing a person failed; they are due again next cycle",
					"worker", retentionWorkerName, "person_id", id, "error", err.Error())
			}
			failed++
			continue
		}
		if len(kinds) > 0 {
			erased++
		}
		for _, k := range kinds {
			totals[k.Kind] += k.Count
		}
	}
	s.logger().InfoContext(ctx, "staff: retention erase cycle finished", "worker", retentionWorkerName, "erased", erased, "failed", failed)
	out := []erasedKind{}
	for _, kind := range eraseKinds {
		if count, ok := totals[kind]; ok {
			out = append(out, erasedKind{Kind: kind, Count: count})
		}
	}
	return out, nil
}

// erasePersonRecovering is erasePerson with a panic turned into that person's
// error: the transaction's deferred rollback has run, nothing of the person
// changed, and the cycle moves on — one bug must not stop the worker for
// good, since the runner never restarts it.
func (s *server) erasePersonRecovering(ctx context.Context, conn *pgxpool.Conn, personID int64, plan retentionPlan, who actor) (kinds []erasedKind, err error) {
	defer func() {
		if r := recover(); r != nil {
			kinds, err = nil, fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return s.erasePerson(ctx, conn, personID, plan, who)
}

// erasePerson runs every erase due for one person in one transaction on conn
// under the person's lock, each schedule judged again on the row the lock
// holds — a hold set, an employment reopened or a rehire since the cycle's
// selection is seen here — and records one erased event naming the kinds
// when anything was erased. The lock allows an erased person (allowErased):
// the health and the A erases run on people the D erase marked, and the D
// erase's own due query is what refuses a person already erased.
func (s *server) erasePerson(ctx context.Context, conn *pgxpool.Conn, personID int64, plan retentionPlan, who actor) ([]erasedKind, error) {
	var kinds []erasedKind
	err := s.withPersonLockOn(ctx, conn, personID, allowErased, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		kinds = nil
		now := s.deps.Clock()
		d, err := txq.DuePeopleD(ctx, plan.dueD(&personID, 1))
		if err != nil {
			return fmt.Errorf("staff: judge person %d's D erase: %w", personID, err)
		}
		if len(d) == 1 {
			n, err := s.eraseD(ctx, tx, txq, personID, now)
			if err != nil {
				return err
			}
			kinds = append(kinds, erasedKind{Kind: eraseKindD, Count: n})
		}
		health, err := txq.DuePeopleHealth(ctx, plan.dueHealth(&personID, 1))
		if err != nil {
			return fmt.Errorf("staff: judge person %d's health erase: %w", personID, err)
		}
		if len(health) == 1 {
			n, err := s.eraseHealth(ctx, txq, personID, now)
			if err != nil {
				return err
			}
			if n > 0 {
				kinds = append(kinds, erasedKind{Kind: eraseKindHealth, Count: n})
			}
		}
		employments, err := txq.DueEmploymentsA(ctx, plan.dueA(&personID, everyRow))
		if err != nil {
			return fmt.Errorf("staff: judge person %d's A erase: %w", personID, err)
		}
		var a int64
		for _, e := range employments {
			n, err := s.eraseA(ctx, tx, txq, personID, e.ID, now)
			if err != nil {
				return err
			}
			a += n
		}
		if a > 0 {
			kinds = append(kinds, erasedKind{Kind: eraseKindA, Count: a})
		}
		b, err := s.eraseAbsenceHealth(ctx, tx, txq, personID, plan, now)
		if err != nil {
			return err
		}
		kinds = append(kinds, b...)
		// The health kinds are recorded in an erased event of their own, of
		// class E, so it is listed to the health group's readers alone: an
		// entry with no kind a reader may read would still tell HR without
		// staff:personal-data, a manager or an Approver, after the fact, that
		// the person's health content — a case, a child's sickness, a
		// withdrawal, HR-only rows — was on file (the coordinator's decision on
		// Task 5b). The other kinds keep their event, of no class.
		var rest, healthKinds []erasedKind
		for _, k := range kinds {
			if slices.Contains(healthEraseKinds, k.Kind) {
				healthKinds = append(healthKinds, k)
			} else {
				rest = append(rest, k)
			}
		}
		if len(rest) > 0 {
			if err := s.recordEvent(ctx, txq, &personID, eventErased, who, map[string]any{"kinds": rest}, nil); err != nil {
				return err
			}
		}
		if len(healthKinds) > 0 {
			return s.recordEvent(ctx, txq, &personID, eventErased, who, map[string]any{"kinds": healthKinds}, []string{healthClass})
		}
		return nil
	})
	if errors.Is(err, errPersonNotFound) {
		// Deleted since the cycle chose them: nothing left to erase.
		return nil, nil
	}
	return kinds, err
}

// eraseD is the D erase (S15): every D column of the person and their terms
// nulled, their next of kin deleted, their documents of class D deleted and
// queued, every value of class D in their history replaced, and erased_at
// set. Class C is untouched.
func (s *server) eraseD(ctx context.Context, tx pgx.Tx, txq *store.Queries, personID int64, now time.Time) (int64, error) {
	var count int64
	for _, table := range tablesWith("D") {
		n, err := eraseColumns(ctx, tx, table, "D", personScope(table), personID, now)
		if err != nil {
			return 0, err
		}
		count += n
	}
	docs, err := txq.PersonDocumentsOfClass(ctx, store.PersonDocumentsOfClassParams{PersonID: personID, Class: "D"})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's documents of class D: %w", personID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.PersonDocumentsOfClassRow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	count += int64(len(removed))
	n, err := s.blankEvents(ctx, txq, personID, "D", func(eventIDs) bool { return true })
	if err != nil {
		return 0, err
	}
	count += n
	if err := txq.MarkPersonErased(ctx, store.MarkPersonErasedParams{ID: personID, Now: now}); err != nil {
		return 0, fmt.Errorf("staff: mark person %d erased: %w", personID, err)
	}
	return count, nil
}

// eraseHealth is the health erase (plan reading 3): the person's documents
// in restricted categories — class E — deleted and queued, and every value
// of class E in their history replaced.
func (s *server) eraseHealth(ctx context.Context, txq *store.Queries, personID int64, now time.Time) (int64, error) {
	docs, err := txq.PersonDocumentsOfClass(ctx, store.PersonDocumentsOfClassParams{PersonID: personID, Class: healthClass})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's health documents: %w", personID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.PersonDocumentsOfClassRow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	owned, err := ownedHealthEvents(ctx, txq, personID)
	if err != nil {
		return 0, err
	}
	n, err := s.blankEvents(ctx, txq, personID, healthClass, func(ids eventIDs) bool { return !owned(ids) })
	if err != nil {
		return 0, err
	}
	return int64(len(removed)) + n, nil
}

// ownedHealthEvents is whether an event's health content is on one of
// delivery B's own schedules (Task 6) rather than the person's: an event of
// a row in a case or of a child-sick row, of a ferie split for a sykmelding
// in a case (sickAbsenceId), of a case that exists or of a row in one
// (rowCaseId, so a row deleted from a case stays the case's), or of the
// withdrawal. DuePeopleHealth's predicate, the same rule in SQL. What none
// owns — an orphan: the events of a case removed with its rows, of a case
// merged away — is the person's, and erased by its own instant besides
// (eraseOrphans).
func ownedHealthEvents(ctx context.Context, txq *store.Queries, personID int64) (func(eventIDs) bool, error) {
	rows, err := txq.PersonAbsences(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's absences: %w", personID, err)
	}
	cased, childSick := map[int64]bool{}, map[int64]bool{}
	for _, r := range rows {
		if r.StaffAbsence.CaseID != nil {
			cased[r.StaffAbsence.ID] = true
		}
		if r.Kind == kindChildSick {
			childSick[r.StaffAbsence.ID] = true
		}
	}
	cases, err := txq.PersonCases(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's cases: %w", personID, err)
	}
	existing := map[int64]bool{}
	for _, c := range cases {
		existing[c.ID] = true
	}
	return func(ids eventIDs) bool {
		switch {
		case ids.Withdrawal:
			return true
		// A child-sick row owns only events that never named a case: one
		// naming a case that is gone is an orphan (retention re-check).
		case ids.AbsenceID != nil && (cased[*ids.AbsenceID] || (childSick[*ids.AbsenceID] && ids.RowCaseID == nil)):
			return true
		case ids.SickAbsenceID != nil && cased[*ids.SickAbsenceID]:
			return true
		case ids.CaseID != nil && existing[*ids.CaseID]:
			return true
		case ids.RowCaseID != nil && existing[*ids.RowCaseID]:
			return true
		}
		return false
	}, nil
}

// eraseAbsenceHealth is delivery B's health schedules for one person (design
// S15, §4 Erase; plan Task 6), each judged again under the person's lock:
// every closed case due (eraseCase), the child-sick rows of a year due, and
// the withdrawal of egenmelding lapsed. It answers what each erased.
func (s *server) eraseAbsenceHealth(ctx context.Context, tx pgx.Tx, txq *store.Queries, personID int64, plan retentionPlan, now time.Time) ([]erasedKind, error) {
	var kinds []erasedKind
	cases, err := txq.DueCasesE(ctx, plan.dueCases(&personID, everyRow))
	if err != nil {
		return nil, fmt.Errorf("staff: judge person %d's cases for the health erase: %w", personID, err)
	}
	var count int64
	for _, c := range cases {
		n, err := s.eraseCase(ctx, tx, txq, personID, c.ID, plan, now)
		if err != nil {
			return nil, err
		}
		count += n
	}
	// The person's own health cutoff, when it comes first, reaches what a
	// case, a child-sick row and a withdrawal own too (whole-branch review).
	past, err := txq.PersonHealthGone(ctx, store.PersonHealthGoneParams{PersonID: personID, Cutoff: dayParam(plan.healthCutoff)})
	if err != nil {
		return nil, fmt.Errorf("staff: judge person %d's health cutoff: %w", personID, err)
	}
	gone := past != nil && *past
	orphans, err := txq.DuePeopleOrphanedSickEvents(ctx, plan.dueOrphans(&personID, 1))
	if err != nil {
		return nil, fmt.Errorf("staff: judge person %d's orphaned sick events: %w", personID, err)
	}
	if len(orphans) == 1 {
		owned, err := ownedHealthEvents(ctx, txq, personID)
		if err != nil {
			return nil, err
		}
		n, err := s.blankEvents(ctx, txq, personID, healthClass, func(ids eventIDs) bool {
			return ids.sickFamily() && !owned(ids) && (gone || ids.At.Before(plan.orphanCutoffAt))
		})
		if err != nil {
			return nil, err
		}
		count += n
	}
	if count > 0 {
		kinds = append(kinds, erasedKind{Kind: eraseKindCases, Count: count})
	}
	child, err := txq.DuePeopleChildSick(ctx, plan.dueChildSick(&personID, 1))
	if err != nil {
		return nil, fmt.Errorf("staff: judge person %d's child-sick rows: %w", personID, err)
	}
	if len(child) == 1 {
		n, err := s.eraseChildSick(ctx, txq, personID, plan, now)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			kinds = append(kinds, erasedKind{Kind: eraseKindChildSick, Count: n})
		}
	}
	withdrawal, err := txq.DuePeopleWithdrawal(ctx, plan.dueWithdrawal(&personID, 1))
	if err != nil {
		return nil, fmt.Errorf("staff: judge person %d's withdrawal: %w", personID, err)
	}
	if len(withdrawal) == 1 {
		// The columns when the withdrawal on file lapsed before the cutoff;
		// each event by its own instant.
		w, err := txq.PersonSelfCertWithdrawal(ctx, personID)
		if err != nil {
			return nil, fmt.Errorf("staff: read person %d's withdrawal: %w", personID, err)
		}
		var n int64
		if w.SelfCertWithdrawnUntil.Valid && (gone || w.SelfCertWithdrawnUntil.Time.Before(plan.healthCutoff)) {
			if n, err = eraseColumns(ctx, tx, "people", healthClass, "id = $1", personID, now); err != nil {
				return nil, err
			}
		}
		e, err := s.blankEvents(ctx, txq, personID, healthClass, func(ids eventIDs) bool {
			return ids.Withdrawal && (gone || ids.At.Before(plan.healthCutoffAt))
		})
		if err != nil {
			return nil, err
		}
		if n+e > 0 {
			kinds = append(kinds, erasedKind{Kind: eraseKindWithdrawal, Count: n + e})
		}
	}
	if plan.erasePregnancyHealth {
		due, err := txq.DuePeoplePregnancyHealth(ctx, plan.duePregnancyHealth(&personID, 1))
		if err != nil {
			return nil, fmt.Errorf("staff: judge person %d's pregnancy and health rows: %w", personID, err)
		}
		if len(due) == 1 {
			n, err := s.erasePregnancyHealth(ctx, txq, personID, plan, gone, now)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				kinds = append(kinds, erasedKind{Kind: eraseKindPregnancyHealth, Count: n})
			}
		}
	}
	return kinds, nil
}

// erasePregnancyHealth is the erase of a person's pregnancy and health rows
// (erasePregnancyHealthTypes, Task 5b; B's open question 11): each row of
// one of those types whose year ended healthRetentionDays ago — or every one,
// once the person's own health cutoff has passed (gone) — given the reserved
// type of its unit, its days and hours kept, and the type values of its
// events replaced whatever class they were recorded in, B having written
// them as class C. An event naming such a type whose row no longer has one —
// deleted, or corrected to another type — goes by the year it was recorded
// in. It answers the rows and the events rewritten.
func (s *server) erasePregnancyHealth(ctx context.Context, txq *store.Queries, personID int64, plan retentionPlan, gone bool, now time.Time) (int64, error) {
	ids, err := txq.DuePregnancyHealthRows(ctx, store.DuePregnancyHealthRowsParams{
		PersonID: personID, TypeIds: plan.pregnancyTypeIDs, Cutoff: dayParam(plan.healthCutoff),
		FloorTypeIds: plan.floorTypeIDs, FloorCutoff: dayParam(plan.floorCutoff),
	})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's pregnancy and health rows due: %w", personID, err)
	}
	// The rows still of such a type, due or not: an event of one not due waits for its row.
	current := map[int64]bool{}
	rows, err := txq.PersonAbsences(ctx, personID)
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's absences: %w", personID, err)
	}
	for _, r := range rows {
		if slices.Contains(plan.pregnancyTypeIDs, r.StaffAbsence.TypeID) {
			current[r.StaffAbsence.ID] = true
		}
	}
	units, err := typeUnits(ctx, txq)
	if err != nil {
		return 0, err
	}
	visibilities, err := readTypeVisibilities(ctx, txq)
	if err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		if err := txq.SetRowsReserved(ctx, store.SetRowsReservedParams{
			HrOnlyHoursID: plan.hrOnlyHoursID, HrOnlyID: plan.hrOnlyID, AbsentHoursID: plan.absentHoursID, AbsentID: plan.absentID,
			Now: now, Ids: ids,
		}); err != nil {
			return 0, fmt.Errorf("staff: erase person %d's pregnancy and health rows' type: %w", personID, err)
		}
	}
	events, err := txq.PersonEventsNamingTypes(ctx, store.PersonEventsNamingTypesParams{PersonID: &personID, TypeIds: plan.pregnancyTypeIDs})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's events of pregnancy and health types: %w", personID, err)
	}
	// The reserved type an erased type value becomes: by its visibility —
	// an HR-only one stays HR-only, so a leave-reader reads it as before —
	// and by its unit, as SetRowsReserved writes the row.
	reserved := func(id int64) int64 {
		switch hours := units[id] == unitHours; {
		case visibilities[id] == visibilityHROnly && hours:
			return plan.hrOnlyHoursID
		case visibilities[id] == visibilityHROnly:
			return plan.hrOnlyID
		case hours:
			return plan.absentHoursID
		}
		return plan.absentID
	}
	count := int64(len(ids))
	for _, ev := range events {
		var about eventIDs
		if err := json.Unmarshal(ev.Payload, &about); err != nil {
			// Failed, not passed over (review item 7): passed over, the
			// event kept naming the type and the person stayed due, taking
			// a batch slot every cycle with nothing erased.
			return 0, fmt.Errorf("staff: read event %d's payload: %w", ev.ID, err)
		}
		yearEnd := time.Date(ev.At.In(plan.loc).Year(), time.December, 31, 0, 0, 0, 0, time.UTC)
		switch {
		case about.AbsenceID != nil && slices.Contains(ids, *about.AbsenceID):
		case about.AbsenceID != nil && current[*about.AbsenceID]:
			continue
		case !gone && !yearEnd.Before(plan.healthCutoff):
			continue
		case slices.ContainsFunc(about.TypeIDs, func(id int64) bool { return slices.Contains(plan.floorTypeIDs, id) }) && !yearEnd.Before(plan.floorCutoff):
			continue
		}
		payload, rewritten, err := blankTypes(ev.Payload, plan.pregnancyTypeIDs, reserved)
		if err != nil {
			return 0, fmt.Errorf("staff: erase event %d's types: %w", ev.ID, err)
		}
		if !rewritten {
			continue
		}
		if err := txq.SetEventPayload(ctx, store.SetEventPayloadParams{ID: ev.ID, Payload: payload}); err != nil {
			return 0, fmt.Errorf("staff: rewrite event %d: %w", ev.ID, err)
		}
		count++
	}
	return count, nil
}

// typeUnits is every type's unit by id, hidden ones included.
func typeUnits(ctx context.Context, q *store.Queries) (map[int64]string, error) {
	rows, err := q.ListAbsenceTypes(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("staff: read the absence types' units: %w", err)
	}
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Unit
	}
	return out, nil
}

// blankTypes is an absence event's payload with its type values erased
// whatever class they were recorded in (Task 5b): its type field replaced by
// "changed", and each of its typeIds that is one of types replaced by its
// reserved type (reserved) — the event's other fields, its classes and its
// kind kept. It answers whether anything changed, so a second run writes
// nothing.
func blankTypes(stored []byte, types []int64, reserved func(int64) int64) ([]byte, bool, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(stored, &payload); err != nil {
		return nil, false, err
	}
	blank, err := json.Marshal(changed)
	if err != nil {
		return nil, false, err
	}
	rewritten := false
	if raw, ok := payload["fields"]; ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, false, err
		}
		if value, ok := fields[fieldType]; ok && !bytes.Equal(value, blank) {
			fields[fieldType] = blank
			if payload["fields"], err = json.Marshal(fields); err != nil {
				return nil, false, err
			}
			rewritten = true
		}
	}
	if raw, ok := payload["typeIds"]; ok {
		var ids []int64
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, false, err
		}
		changedIDs := false
		for i, id := range ids {
			if slices.Contains(types, id) {
				ids[i] = reserved(id)
				changedIDs = true
			}
		}
		if changedIDs {
			if payload["typeIds"], err = json.Marshal(ids); err != nil {
				return nil, false, err
			}
			rewritten = true
		}
	}
	if !rewritten {
		return stored, false, nil
	}
	out, err := json.Marshal(payload)
	return out, true, err
}

// eraseCase is one closed case's health erase (design §4 Erase, plan Task
// 6): its class E columns and its milestones' erased (columnClasses — the
// case keeps its id and its dates, a milestone its kind and its date), its
// rows' sick type replaced by the reserved absent and their degree nulled,
// the documents filed under it deleted and queued, and the health fields of
// the events it owns replaced: its own and those of the cases it took in by
// a merge, its rows', and those of a ferie split for one of its rows.
func (s *server) eraseCase(ctx context.Context, tx pgx.Tx, txq *store.Queries, personID, caseID int64, plan retentionPlan, now time.Time) (int64, error) {
	var count int64
	for table, scope := range map[string]string{
		"sick_cases": "id = $1", "sick_case_milestones": "case_id = $1", "absences": "case_id = $1",
	} {
		n, err := eraseColumns(ctx, tx, table, healthClass, scope, caseID, now)
		if err != nil {
			return 0, err
		}
		count += n
	}
	id := caseID
	if err := txq.MarkCaseErased(ctx, store.MarkCaseErasedParams{Zone: plan.zone, ID: caseID}); err != nil {
		return 0, fmt.Errorf("staff: mark case %d erased: %w", caseID, err)
	}
	n, err := txq.SetCaseRowsAbsent(ctx, store.SetCaseRowsAbsentParams{AbsentID: plan.absentID, Now: now, CaseID: &id})
	if err != nil {
		return 0, fmt.Errorf("staff: erase case %d's rows' type: %w", caseID, err)
	}
	count += n
	docs, err := txq.CaseDocuments(ctx, &id)
	if err != nil {
		return 0, fmt.Errorf("staff: read case %d's documents: %w", caseID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.CaseDocumentsRow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	count += int64(len(removed))
	rows, err := txq.CaseRowIDs(ctx, &id)
	if err != nil {
		return 0, fmt.Errorf("staff: read case %d's rows: %w", caseID, err)
	}
	ownCases := []int64{caseID}
	for next := ownCases; len(next) > 0; {
		merged, err := txq.MergedCaseIDs(ctx, store.MergedCaseIDsParams{PersonID: &personID, CaseIds: next})
		if err != nil {
			return 0, fmt.Errorf("staff: read the cases case %d took in: %w", caseID, err)
		}
		next = nil
		for _, m := range merged {
			if !slices.Contains(ownCases, m) {
				ownCases = append(ownCases, m)
				next = append(next, m)
			}
		}
	}
	e, err := s.blankEvents(ctx, txq, personID, healthClass, func(ids eventIDs) bool {
		return (ids.CaseID != nil && slices.Contains(ownCases, *ids.CaseID)) ||
			(ids.RowCaseID != nil && slices.Contains(ownCases, *ids.RowCaseID)) ||
			(ids.AbsenceID != nil && slices.Contains(rows, *ids.AbsenceID)) ||
			(ids.SickAbsenceID != nil && slices.Contains(rows, *ids.SickAbsenceID))
	})
	if err != nil {
		return 0, err
	}
	count += e
	if plan.removeCases {
		n, err := s.removeCase(ctx, txq, personID, caseID, ownCases, now)
		if err != nil {
			return 0, err
		}
		count += n
	}
	return count, nil
}

// removeCase is what eraseSickCaseDates adds to a case's erase (Task 5b; B's
// open question 12), after B's erase has blanked its health content: the
// case's events — its own and those of the cases it took in — blanked to
// their kind, keeping only their rows' reserved types (typeIds) by which
// they are listed; its rows' events no longer naming it (rowCaseId); its
// milestones deleted, its rows unlinked keeping their days as the reserved
// absent, and the case row deleted. Its documents are gone already. It
// answers the items removed: the events rewritten, the milestones, the rows
// unlinked and the case.
func (s *server) removeCase(ctx context.Context, txq *store.Queries, personID, caseID int64, ownCases []int64, now time.Time) (int64, error) {
	events, err := txq.PersonEventsNamingCases(ctx, store.PersonEventsNamingCasesParams{PersonID: &personID, CaseIds: ownCases})
	if err != nil {
		return 0, fmt.Errorf("staff: read case %d's events: %w", caseID, err)
	}
	var count int64
	for _, ev := range events {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return 0, fmt.Errorf("staff: read event %d: %w", ev.ID, err)
		}
		next := payload
		if _, ofCase := payload["caseId"]; ofCase {
			next = map[string]json.RawMessage{"fields": json.RawMessage(`{}`)}
			for _, key := range []string{"typeIds", "typeClasses"} {
				if v, ok := payload[key]; ok {
					next[key] = v
				}
			}
		} else {
			delete(next, "rowCaseId")
		}
		out, err := json.Marshal(next)
		if err != nil {
			return 0, err
		}
		if err := txq.SetEventPayload(ctx, store.SetEventPayloadParams{ID: ev.ID, Payload: out}); err != nil {
			return 0, fmt.Errorf("staff: rewrite event %d: %w", ev.ID, err)
		}
		count++
	}
	milestones, err := txq.EraseCaseMilestones(ctx, caseID)
	if err != nil {
		return 0, fmt.Errorf("staff: delete case %d's milestones: %w", caseID, err)
	}
	id := caseID
	unlinked, err := txq.UnlinkCaseRows(ctx, store.UnlinkCaseRowsParams{Now: now, CaseID: &id})
	if err != nil {
		return 0, fmt.Errorf("staff: unlink case %d's rows: %w", caseID, err)
	}
	deleted, err := txq.EraseSickCase(ctx, caseID)
	if err != nil {
		return 0, fmt.Errorf("staff: delete case %d: %w", caseID, err)
	}
	return count + milestones + unlinked + deleted, nil
}

// eraseChildSick is the erase of a person's child-sick rows whose year ended
// healthRetentionDays ago (plan reading 21): their type replaced by the
// reserved absent, the days kept, and the health fields of their events
// replaced.
func (s *server) eraseChildSick(ctx context.Context, txq *store.Queries, personID int64, plan retentionPlan, now time.Time) (int64, error) {
	ids, err := txq.DueChildSickRows(ctx, store.DueChildSickRowsParams{PersonID: personID, Cutoff: dayParam(plan.healthCutoff)})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's child-sick rows due: %w", personID, err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := txq.SetRowsAbsent(ctx, store.SetRowsAbsentParams{AbsentID: plan.absentID, Now: now, Ids: ids}); err != nil {
		return 0, fmt.Errorf("staff: erase person %d's child-sick rows' type: %w", personID, err)
	}
	n, err := s.blankEvents(ctx, txq, personID, healthClass, func(e eventIDs) bool {
		return e.AbsenceID != nil && slices.Contains(ids, *e.AbsenceID)
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)) + n, nil
}

// eraseA is one employment's A erase (plan reading 4): its pay columns
// nulled on every terms row, its documents of class A deleted and queued —
// filed against it, or against no employment while it is the person's last
// — and the values of class A in its events replaced, with every value of
// the deleted documents' events. While it is the last, the events of a
// document of class A that is already gone — a contract or an attest deleted
// by hand, which no longer says which employment it was filed against — are
// replaced too. The person's other employments keep theirs.
func (s *server) eraseA(ctx context.Context, tx pgx.Tx, txq *store.Queries, personID, employmentID int64, now time.Time) (int64, error) {
	var count int64
	for _, table := range tablesWith("A") {
		scope, err := employmentScope(table)
		if err != nil {
			return 0, err
		}
		n, err := eraseColumns(ctx, tx, table, "A", scope, employmentID, now)
		if err != nil {
			return 0, err
		}
		count += n
	}
	last, err := txq.LastEmploymentID(ctx, personID)
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's last employment: %w", personID, err)
	}
	docs, err := txq.EmploymentDocumentsA(ctx, store.EmploymentDocumentsAParams{
		PersonID: personID, EmploymentID: &employmentID, IsLast: last == employmentID,
	})
	if err != nil {
		return 0, fmt.Errorf("staff: read employment %d's documents of class A: %w", employmentID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.EmploymentDocumentsARow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	count += int64(len(removed))
	var gone []int64
	if last == employmentID {
		if gone, err = txq.GoneDocumentsWithEventsA(ctx, store.GoneDocumentsWithEventsAParams{
			PersonID: &personID, DocumentKinds: documentEventKinds,
		}); err != nil {
			return 0, fmt.Errorf("staff: read person %d's events of documents gone: %w", personID, err)
		}
	}
	n, err := s.blankEvents(ctx, txq, personID, "A", func(ids eventIDs) bool {
		if ids.DocumentID != nil {
			return slices.Contains(removed, *ids.DocumentID) || slices.Contains(gone, *ids.DocumentID)
		}
		return ids.EmploymentID != nil && *ids.EmploymentID == employmentID
	})
	if err != nil {
		return 0, err
	}
	return count + n, nil
}

// erasedDocument is a document an erase deletes: its row and its object's key.
type erasedDocument struct {
	id  int64
	key string
}

// eraseDocuments deletes each document's row and queues its object in the
// ledger, in the caller's transaction (plan reading 21) — the hand delete's
// two writes, without its attempt: the ledger finishes the object once the
// transaction has committed. It answers the ids deleted.
func (s *server) eraseDocuments(ctx context.Context, txq *store.Queries, docs []erasedDocument, now time.Time) ([]int64, error) {
	out := make([]int64, 0, len(docs))
	for _, d := range docs {
		if err := txq.DeleteDocument(ctx, d.id); err != nil {
			return nil, fmt.Errorf("staff: erase document %d: %w", d.id, err)
		}
		if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: d.key, Now: now}); err != nil {
			return nil, fmt.Errorf("staff: queue erased document %d's object: %w", d.id, err)
		}
		out = append(out, d.id)
	}
	return out, nil
}

// eventIDs is what an event's payload says it is about: an employment, a
// document, and — delivery B — an absence, a case, the sykmelding a ferie
// was split for, or the withdrawal of egenmelding.
type eventIDs struct {
	EmploymentID  *int64 `json:"employmentId"`
	DocumentID    *int64 `json:"documentId"`
	AbsenceID     *int64 `json:"absenceId"`
	CaseID        *int64 `json:"caseId"`
	RowCaseID     *int64 `json:"rowCaseId"`
	SickAbsenceID *int64 `json:"sickAbsenceId"`
	Withdrawal    bool   `json:"selfCertWithdrawal"`
	// TypeIDs are an absence event's types (Task 5b): the pregnancy leave
	// floor reads them.
	TypeIDs []int64 `json:"typeIds"`
	// At is when the event was recorded, from its row rather than its
	// payload: the withdrawal's and the orphaned events' erases judge by it.
	At time.Time `json:"-"`
}

// sickFamily is whether an event is about an absence or a case: an orphan
// of one is erased by its own instant.
func (e eventIDs) sickFamily() bool {
	return !e.Withdrawal && (e.AbsenceID != nil || e.CaseID != nil || e.RowCaseID != nil || e.SickAbsenceID != nil)
}

// blankEvents replaces class's values in the person's events whose
// payload_classes hold it and that include accepts, answering the payloads
// rewritten. An event keeps its kind, actor, instant and classes — the
// history still says what happened when, and readers still know what kind of
// data it was about.
func (s *server) blankEvents(ctx context.Context, txq *store.Queries, personID int64, class string, include func(eventIDs) bool) (int64, error) {
	rows, err := txq.PersonEventsOfClass(ctx, store.PersonEventsOfClassParams{PersonID: &personID, Class: class})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's events of class %s: %w", personID, class, err)
	}
	absentID, err := txq.AbsentTypeID(ctx)
	if err != nil {
		return 0, fmt.Errorf("staff: read the reserved absent type: %w", err)
	}
	reservedRows, err := txq.SeededTypesByKey(ctx, reservedTypes)
	if err != nil {
		return 0, fmt.Errorf("staff: read the reserved types: %w", err)
	}
	reserved := mapRows(reservedRows, func(r store.SeededTypesByKeyRow) int64 { return r.ID })
	var count int64
	for _, row := range rows {
		var ids eventIDs
		if err := json.Unmarshal(row.Payload, &ids); err != nil {
			continue
		}
		ids.At = row.At
		if !include(ids) {
			continue
		}
		payload, rewritten, err := blankPayload(row.Payload, class, absentID, reserved...)
		if err != nil {
			return 0, fmt.Errorf("staff: erase event %d's values of class %s: %w", row.ID, class, err)
		}
		if !rewritten {
			continue
		}
		if err := txq.SetEventPayload(ctx, store.SetEventPayloadParams{ID: row.ID, Payload: payload}); err != nil {
			return 0, fmt.Errorf("staff: rewrite event %d: %w", row.ID, err)
		}
		count++
	}
	return count, nil
}

// blankPayload is a stored payload with class's values replaced by
// "changed" — the field's name kept, its value gone: what shapePayload shows
// a reader who may not read the field, now shown to everyone. A document's
// event is replaced whole, since its fields take its category's class; any
// other event field by field, by fieldClasses. It answers whether anything
// changed: a value already replaced is left, so an erase run twice writes
// nothing the second time.
//
// An absence or case event's typeIds — the row's types beside its fields, by
// which a reader is shown the whole event — are erased by their own classes
// (typeClasses, leak 3): each one of class's is replaced with absentID, the
// reserved type the erase writes in place of a sick one; an entry without a
// class is replaced by every erase (fail closed).
//
// An entry already one of the reserved types (reserved) is erased already
// and left as it is (review L2): an HR-only reserved id rewritten to absent
// would widen who reads the event.
func blankPayload(stored []byte, class string, absentID int64, reserved ...int64) ([]byte, bool, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(stored, &payload); err != nil {
		return nil, false, err
	}
	var fields map[string]json.RawMessage
	if raw, ok := payload["fields"]; ok {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, false, err
		}
	}
	blank, err := json.Marshal(changed)
	if err != nil {
		return nil, false, err
	}
	_, document := payload["documentId"]
	// An absence, adjustment, case or withdrawal event (delivery B) carries each
	// field's class in its own map, written when it was recorded: it is
	// blanked by that map, a field it does not class counting as D.
	var own map[string]string
	_, absence := payload["absenceId"]
	_, adjustment := payload["adjustmentId"]
	_, sickCase := payload["caseId"]
	_, withdrawal := payload["selfCertWithdrawal"]
	classed := absence || adjustment || sickCase || withdrawal
	if raw, ok := payload["classes"]; ok && classed {
		if err := json.Unmarshal(raw, &own); err != nil {
			return nil, false, err
		}
	}
	// A field its map does not class — or every field of an event without a
	// map — is blanked by every erase: what cannot be placed is erased, not
	// kept (review L1).
	erases := func(name string) bool {
		if !classed {
			return document || classOfField(name) == class
		}
		c, ok := own[name]
		return !ok || c == class
	}
	rewritten := false
	for name, value := range fields {
		if erases(name) && !bytes.Equal(value, blank) {
			fields[name] = blank
			rewritten = true
		}
	}
	fieldsRewritten := rewritten
	// A case event carries its rows' types too (Task 5): erased by the
	// same rule (Task 6).
	if raw, ok := payload["typeIds"]; ok && classed {
		var ids []int64
		var classes []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, false, err
		}
		if rawClasses, ok := payload["typeClasses"]; ok {
			if err := json.Unmarshal(rawClasses, &classes); err != nil {
				return nil, false, err
			}
		}
		idsRewritten := false
		for i, id := range ids {
			if (i >= len(classes) || classes[i] == class) && id != absentID && !slices.Contains(reserved, id) {
				ids[i] = absentID
				idsRewritten = true
			}
		}
		if idsRewritten {
			if payload["typeIds"], err = json.Marshal(ids); err != nil {
				return nil, false, err
			}
			rewritten = true
		}
	}
	if !rewritten {
		return stored, false, nil
	}
	if !fieldsRewritten {
		out, err := json.Marshal(payload)
		return out, true, err
	}
	if payload["fields"], err = json.Marshal(fields); err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(payload)
	return out, true, err
}

// erasedKindsResponse is the erase cycle's kinds as the contract answers them.
func erasedKindsResponse(kinds []erasedKind) []gen.StaffErasedKind {
	return mapRows(kinds, func(k erasedKind) gen.StaffErasedKind {
		return gen.StaffErasedKind{Kind: gen.StaffErasedKindKind(k.Kind), Count: k.Count}
	})
}
