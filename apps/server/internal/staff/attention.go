package staff

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the dashboard's attention items for the register (design S20,
// plan Task 6), shaped per caller: staff:manage's, staff:personal-data's,
// staff:approve's and a manager's for the people they manage today. The rules
// are pure over the register's snapshot (stats.go) and the caller's today;
// delivery B's absence items are read in the same snapshot (absence
// attention below), and delivery C's account items (accounts_attention.go). Each item's entityId is a
// person's id, or the literal 'retention' or 'thresholds', or a headcount
// threshold — what the host's link is built from.

// The rules' periods (R §4.1, §4.2, §4.8; design S15).
const (
	contractDueDays          = 7  // a written contract within seven days of the start (aml. § 14-5)
	temporaryNoticeMonths    = 12 // a temporary employment past a year is owed a month's notice (§ 14-9)
	temporaryConvertsMonths  = 36 // a run on grounds a or b converts after three years (§ 14-9 (7))
	retentionFailingAfter    = 24 * time.Hour
	otpFullPosition          = 7500  // 75 %, in hundredths of a percent
	otpPartTimeFloor         = 2000  // 20 %
	otpPartTimeSum           = 20000 // two full-time equivalents
	thresholdOTP             = "otp"
	retentionAttentionEntity = "retention"
	thresholdsEntity         = "thresholds"
)

// headcountThresholds are the headcounts that bring a duty (R §4.10, §4.12),
// each with the headcount from which it is crossed and the sentence its item
// carries: 5 or more, 30 or more, and more than 50 — the equality duty is an
// employer's with more than fifty (ldl. § 26), so fifty itself is not over it.
var headcountThresholds = []struct {
	key     string
	crossed int
	title   string
}{
	{"5", 5, "5 or more employees: a whistleblowing routine (varslingsrutine) and a safety representative (verneombud) are required"},
	{"30", 30, "30 or more employees: a working environment committee (arbeidsmiljøutvalg, AMU) is required"},
	{"50", 51, "More than 50 employees: the activity and reporting duty on equality (aktivitets- og redegjørelsesplikten, ARP) applies"},
}

// otpTitle is the OTP item's sentence. No owner is recorded, so the rule
// cannot tell the owner's own position from an employee's (plan reading 17):
// the sentence names the exception instead.
const otpTitle = "The OTP threshold is crossed: an occupational pension scheme is required, unless the only such person is the owner"

// retentionFailingTitle is the ledger item's sentence.
const retentionFailingTitle = "Deletions from the object store have failed for more than a day"

// attentionInput is what the items are judged on beyond the register: the
// employments a signed contract document is filed for, the failing ledger
// rows and the acknowledged thresholds.
type attentionInput struct {
	register
	signed       map[int64]bool
	failing      store.FailingDeletionsRow
	acknowledged map[string]time.Time
	// selfApproved is the approved absences their persons approved
	// themselves that have not ended (delivery B, Task 4), never of an
	// HR-only type; hrOnlySelfApproved the HR-only ones the sole holder of
	// staff:personal-data approved (delivery C, Task 5).
	selfApproved       []store.SelfApprovedAbsencesRow
	hrOnlySelfApproved []store.HROnlySelfApprovedAbsencesRow
	// affected is the approved ferie a 100 % sykmelding lies over (Task 5
	// review H1).
	affected []store.HolidayAffectedBySicknessRow
	// Delivery B's other items (Task 6), read for the caller's roles:
	// the requests they decide, the HR-only requests stalled, the open
	// cases' unsettled milestones, the people the caller manages today, and
	// the people with holiday untaken last year or short in the main period.
	awaiting    requestsWaiting
	stalled     store.StalledHROnlyRequestsRow
	milestones  []store.OpenCaseMilestonesRow
	reports     []int64
	untaken     []int64
	mainShort   []int64
	holidayYear int
	// accounts is staff:manage's account items' rows (delivery C, Task 4c,
	// accounts_attention.go).
	accounts accountAttention
}

// attentionFor is the caller's items, oldest first, then by type and id, each
// once whichever of the caller's roles found it.
func attentionFor(in attentionInput, c *callerContext) []gen.StaffStatsAttentionItem {
	items := []gen.StaffStatsAttentionItem{}
	if c.Manage || c.Person != nil {
		items = append(items, in.probationEnding(c)...)
	}
	if c.Manage {
		items = append(items, in.contractOverdue(c.Today)...)
		items = append(items, in.temporaryNoticeDue(c.Today)...)
		items = append(items, in.temporaryConverts(c.Today)...)
		items = append(items, in.reportsWithoutManager(c.Today)...)
		items = append(items, in.retentionFailing()...)
		items = append(items, in.thresholds(c.Today)...)
		items = append(items, in.selfApprovedItems()...)
		items = append(items, in.holidayAffectedItems()...)
		items = append(items, in.accounts.items()...)
	}
	if c.PersonalData {
		items = append(items, in.reassessmentDue(c.Today)...)
		items = append(items, in.hrOnlyStalled()...)
		items = append(items, in.hrOnlySelfApprovedItems()...)
	}
	items = append(items, in.awaitingDecision()...)
	items = append(items, in.milestonesDue(c)...)
	if c.Manage {
		items = append(items, in.holidayItems()...)
	}
	slices.SortFunc(items, func(a, b gen.StaffStatsAttentionItem) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.Type, b.Type), cmp.Compare(a.Id, b.Id))
	})
	return items
}

// personItem is an item about a person, keyed by key (an employment's or the
// person's id) and named by title.
func personItem(kind gen.StaffStatsAttentionItemType, key, personID int64, title string, occurred time.Time) gen.StaffStatsAttentionItem {
	return gen.StaffStatsAttentionItem{
		Id: string(kind) + "/" + strconv.FormatInt(key, 10), Type: kind, Title: title,
		OccurredAt: occurred, EntityId: strconv.FormatInt(personID, 10),
	}
}

// employmentItem is an item about one employment: the person's name, and the
// employment number after it when the person has more than one.
func (r register) employmentItem(kind gen.StaffStatsAttentionItemType, e store.StaffEmployment, occurred time.Time) gen.StaffStatsAttentionItem {
	title := r.names[e.PersonID]
	if r.counts[e.PersonID] > 1 {
		title = fmt.Sprintf("%s (%s)", title, e.EmploymentNumber)
	}
	return personItem(kind, e.ID, e.PersonID, title, occurred)
}

// contractOverdue is every employment in force today whose written contract
// is overdue (R §4.1): neither a typed signing day nor a signed contract
// document filed for it, seven days after its start — at once when, as
// agreed at the start, it lasts under a month or the person is hired out. An
// end typed later, an employment ended early, does not make it due at once.
func (in attentionInput) contractOverdue(today time.Time) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, e := range in.employments {
		if !inForce(e, today) || e.ContractSignedAt.Valid || in.signed[e.ID] {
			continue
		}
		start := civil(e.StartDate)
		due := start.AddDate(0, 0, contractDueDays)
		if contractDueAtOnce(e, termsInForce(e, in.chains[e.ID], start)) {
			due = start
		}
		if !today.Before(due) {
			out = append(out, in.employmentItem(gen.ContractOverdue, e, due))
		}
	}
	return out
}

// contractDueAtOnce reports whether e's contract is owed from its first day
// (§ 14-5): judged on t, the terms in force on the start, the person is hired
// out, or t is a temporary row whose expected end is under a month from the
// start. The employment's end is not read: as it stands now it may have been
// typed later, and what counts is what was agreed at the start.
func contractDueAtOnce(e store.StaffEmployment, t *store.StaffEmploymentTerm) bool {
	if hiredOut(t) {
		return true
	}
	if t == nil || t.EmploymentType != "midlertidig" || !t.ExpectedEndDate.Valid {
		return false
	}
	return t.ExpectedEndDate.Time.Before(addMonths(civil(e.StartDate), 1).AddDate(0, 0, -1))
}

// probationEnding is every employment in force today whose probation — the
// extension's end when there is one — ends today or within soonDays: all of
// them for staff:manage, and for a manager the employments whose manager
// today, by the graph's effectiveManager, is the caller — each employment
// judged on its own terms, so a manager of one of two concurrent employments
// sees that one's.
func (in attentionInput) probationEnding(c *callerContext) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	horizon := c.Today.AddDate(0, 0, soonDays)
	for _, e := range in.employments {
		if !inForce(e, c.Today) {
			continue
		}
		t := termsInForce(e, in.chains[e.ID], c.Today)
		if t == nil {
			continue
		}
		end := t.ProbationEndDate
		if t.ProbationExtendedTo.Valid {
			end = t.ProbationExtendedTo
		}
		if !end.Valid || end.Time.Before(c.Today) || end.Time.After(horizon) {
			continue
		}
		if !c.Manage && !in.manages(*c.Person, e, c.Today) {
			continue
		}
		out = append(out, in.employmentItem(gen.ProbationEnding, e, end.Time))
	}
	return out
}

// temporary reports whether t is a temporary row, and on ground a or b.
func temporary(t *store.StaffEmploymentTerm) (temp, groundAB bool) {
	if t == nil || t.EmploymentType != "midlertidig" {
		return false, false
	}
	return true, t.TemporaryGround != nil && (*t.TemporaryGround == "a" || *t.TemporaryGround == "b")
}

// temporaryNoticeDue is every temporary employment in force today that has
// lasted twelve months (R §4.2), any ground.
func (in attentionInput) temporaryNoticeDue(today time.Time) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, e := range in.employments {
		if !inForce(e, today) {
			continue
		}
		if temp, _ := temporary(termsInForce(e, in.chains[e.ID], today)); !temp {
			continue
		}
		if due := addMonths(civil(e.StartDate), temporaryNoticeMonths); !today.Before(due) {
			out = append(out, in.employmentItem(gen.TemporaryNoticeDue, e, due))
		}
	}
	return out
}

// temporaryConverts is every person whose run of temporary employments on
// ground a or b has passed thirty-six months (R §4.2). An employment is on
// such a ground when its terms in force on its last day so far (today, or its
// end) say so; a run is those employments by start, each starting no later
// than the day after the run so far ended — a gap of a day breaks it; an
// employment on another ground is no part of one; and a run counts while one
// of its employments is in force today.
func (in attentionInput) temporaryConverts(today time.Time) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for person, run := range in.runs(today) {
		if due := addMonths(run, temporaryConvertsMonths); !today.Before(due) {
			out = append(out, personItem(gen.TemporaryConverts, person, person, in.names[person], due))
		}
	}
	return out
}

// runs is the start of each person's run on ground a or b that has an
// employment in force today.
func (in attentionInput) runs(today time.Time) map[int64]time.Time {
	type run struct{ start, reach time.Time }
	open := map[int64]*run{}
	out := map[int64]time.Time{}
	far := time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
	for _, e := range in.employments { // by person, by start
		if _, ab := temporary(termsInForce(e, in.chains[e.ID], judgedOn(e, today))); !ab {
			continue
		}
		start, end := civil(e.StartDate), far
		if e.EndDate.Valid {
			end = e.EndDate.Time
		}
		r := open[e.PersonID]
		if r == nil || start.After(r.reach.AddDate(0, 0, 1)) {
			r = &run{start: start, reach: end}
			open[e.PersonID] = r
		} else if end.After(r.reach) {
			r.reach = end
		}
		if inForce(e, today) {
			out[e.PersonID] = r.start
		}
	}
	return out
}

// manages reports whether manager is e's manager today by the graph's
// effectiveManager — the rule both the probation item's audience and
// reportsWithoutManager read.
func (in attentionInput) manages(manager int64, e store.StaffEmployment, today time.Time) bool {
	m := in.graph.effectiveManager(asGraphEmployment(e), today)
	return m != nil && *m == manager
}

// reportsOf is the people with an employment in force today whose manager
// today is manager, by the same rule, each once.
func (in attentionInput) reportsOf(manager int64, today time.Time) []int64 {
	var out []int64
	for _, e := range in.employments {
		if inForce(e, today) && in.manages(manager, e, today) && !slices.Contains(out, e.PersonID) {
			out = append(out, e.PersonID)
		}
	}
	return out
}

// reportsWithoutManager is every person with an employment in force today
// that has no manager today by the graph's effectiveManager (design §9): the
// manager its terms name, else of its department or one above it, each one
// someone else in employment today. A person nobody can manage by the
// structure is not listed: one whose terms place them in a department they
// manage, with no manager named in any department above it
// (headsTopOfStructure). One per person, dated the first such employment's
// start.
func (in attentionInput) reportsWithoutManager(today time.Time) []gen.StaffStatsAttentionItem {
	listed := map[int64]bool{}
	var out []gen.StaffStatsAttentionItem
	for _, e := range in.employments {
		if listed[e.PersonID] || !inForce(e, today) {
			continue
		}
		g := asGraphEmployment(e)
		if in.graph.effectiveManager(g, today) != nil || in.graph.headsTopOfStructure(g, today) {
			continue
		}
		listed[e.PersonID] = true
		out = append(out, personItem(gen.ReportsWithoutManager, e.PersonID, e.PersonID, in.names[e.PersonID], civil(e.StartDate)))
	}
	return out
}

// reassessmentDue is every erased person not yet reassessed whose last
// employment ended twelve months ago or more (design S15, plan reading 16),
// dated the day it fell due.
func (in attentionInput) reassessmentDue(today time.Time) []gen.StaffStatsAttentionItem {
	type last struct {
		end   time.Time
		open  bool
		count int
	}
	ends := map[int64]*last{}
	for _, e := range in.employments {
		l := ends[e.PersonID]
		if l == nil {
			l = &last{}
			ends[e.PersonID] = l
		}
		l.count++
		if !e.EndDate.Valid {
			l.open = true
		} else if e.EndDate.Time.After(l.end) {
			l.end = e.EndDate.Time
		}
	}
	var out []gen.StaffStatsAttentionItem
	for _, p := range in.people {
		l := ends[p.ID]
		if p.ErasedAt == nil || p.ReassessedAt != nil || l == nil || l.open {
			continue
		}
		if due := addMonths(l.end, reassessmentAfterMonths); !today.Before(due) {
			out = append(out, personItem(gen.ReassessmentDue, p.ID, p.ID, p.DisplayName, due))
		}
	}
	return out
}

// selfApprovedItems is one item per absence its person approved themselves
// that has not ended (provisional, plan reading 1, pre-flight I7): a holder
// of staff:approve or staff:manage heading the structure with no manager who
// has an active user. staff:manage's, so somebody sees what nobody else
// decided; entityId the person, keyed by the absence, named by the person
// and never the type.
func (in attentionInput) selfApprovedItems() []gen.StaffStatsAttentionItem {
	out := make([]gen.StaffStatsAttentionItem, 0, len(in.selfApproved))
	for _, r := range in.selfApproved {
		var at time.Time
		if r.DecidedAt != nil {
			at = *r.DecidedAt
		}
		out = append(out, personItem(gen.SelfApproved, r.ID, r.PersonID, r.DisplayName, at))
	}
	return out
}

// hrOnlySelfApprovedItems is staff:personal-data's one item per HR-only
// absence its person approved as the sole holder of staff:personal-data,
// until it has ended (delivery C, Task 5; the owner's decision on B's open
// question 9): entityId the person, keyed by the absence, named by the
// person and never by the type — every holder of staff:personal-data reads
// it, a holder added later the second pair of eyes the rule lacked (reading
// 22) — occurredAt when it was approved.
func (in attentionInput) hrOnlySelfApprovedItems() []gen.StaffStatsAttentionItem {
	out := make([]gen.StaffStatsAttentionItem, 0, len(in.hrOnlySelfApproved))
	for _, r := range in.hrOnlySelfApproved {
		var at time.Time
		if r.DecidedAt != nil {
			at = *r.DecidedAt
		}
		out = append(out, personItem(gen.HrOnlySelfApproved, r.ID, r.PersonID, r.DisplayName, at))
	}
	return out
}

// holidayAffectedItems is one item per approved ferie a 100 % sykmelding
// lies over, until four weeks after the ferie's last day (Task 5 review H1):
// the employee may demand to postpone it (ferieloven § 9), which HR records.
// staff:manage's alone — it says the person was sick, which HR reads; never
// a colleague's. entityId the person, keyed by the ferie, named by the
// person, occurredAt the ferie's first day.
func (in attentionInput) holidayAffectedItems() []gen.StaffStatsAttentionItem {
	out := make([]gen.StaffStatsAttentionItem, 0, len(in.affected))
	for _, r := range in.affected {
		out = append(out, personItem(gen.HolidayAffectedBySickness, r.ID, r.PersonID, r.DisplayName, civil(r.FirstDay)))
	}
	return out
}

// retentionFailing is the one item about the deletion ledger: rows the store
// has refused at least once, queued more than a day ago.
func (in attentionInput) retentionFailing() []gen.StaffStatsAttentionItem {
	if in.failing.Failing == 0 {
		return nil
	}
	count := int32(in.failing.Failing)
	return []gen.StaffStatsAttentionItem{{
		Id: string(gen.RetentionFailing), Type: gen.RetentionFailing, Title: retentionFailingTitle,
		OccurredAt: in.failing.Oldest, EntityId: retentionAttentionEntity, Count: &count,
	}}
}

// thresholds is the OTP item and the headcount items not acknowledged.
func (in attentionInput) thresholds(today time.Time) []gen.StaffStatsAttentionItem {
	active := in.activePeople(today)
	var out []gen.StaffStatsAttentionItem
	if _, done := in.acknowledged[thresholdOTP]; !done && otpCrossed(active) {
		out = append(out, gen.StaffStatsAttentionItem{
			Id: string(gen.OtpThreshold), Type: gen.OtpThreshold, Title: otpTitle,
			OccurredAt: today, EntityId: thresholdsEntity,
		})
	}
	headcount := int32(len(active))
	for _, h := range headcountThresholds {
		if _, done := in.acknowledged[h.key]; done || int(headcount) < h.crossed {
			continue
		}
		out = append(out, gen.StaffStatsAttentionItem{
			Id: string(gen.HeadcountThreshold) + "/" + h.key, Type: gen.HeadcountThreshold, Title: h.title,
			OccurredAt: today, EntityId: h.key, Count: &headcount,
		})
	}
	return out
}

// otpCrossed is the OTP test without an owner flag (plan reading 17, R §4.8)
// over each person's percentage in force: someone at 75 % or more, or the
// people at 20 % or more adding up to two full-time equivalents.
func otpCrossed(fte map[int64]int64) bool {
	var partTime int64
	for _, f := range fte {
		if f >= otpFullPosition {
			return true
		}
		if f >= otpPartTimeFloor {
			partTime += f
		}
	}
	return partTime >= otpPartTimeSum
}

// GetStaffStatsAttention Get the register's attention items
// (GET /api/v1/staff/stats/attention)
//
// staff:access, shaped per caller: a caller with none of the roles gets an
// empty list, never a 403 — the host asks every module it shows.
func (s *server) GetStaffStatsAttention(ctx context.Context, _ gen.GetStaffStatsAttentionRequestObject) (gen.GetStaffStatsAttentionResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.Manage && !caller.PersonalData && !caller.Approve && caller.Person == nil {
		return gen.GetStaffStatsAttention200JSONResponse([]gen.StaffStatsAttentionItem{}), nil
	}
	acknowledged, err := parseAcknowledged(caller.Settings.ThresholdsAcknowledged)
	if err != nil {
		return nil, err
	}
	// Whether the caller approves their own requests is the directory's to
	// say, asked before the snapshot (deciders.go), as GET /approvals asks:
	// a plain one by B's rule, an HR-only one as the sole holder of
	// staff:personal-data (holders.go).
	selfApproves := false
	if (caller.Approve || caller.Manage) && caller.Person != nil {
		d, err := s.judgeDecider(ctx, caller, *caller.Person, false)
		if err != nil {
			return nil, err
		}
		selfApproves = d.selfApproves
	}
	solePersonalData := s.solePersonalData(ctx, caller)
	var items []gen.StaffStatsAttentionItem
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		items, err = s.readAttention(ctx, q, caller, acknowledged, selfApproves, solePersonalData)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffStatsAttention200JSONResponse(items), nil
}

// readAttention reads on q, a snapshot's, what the caller's items are judged
// on, and judges them. Each table is read once, and only what the caller's
// roles need: a caller who is neither staff:manage nor staff:personal-data
// is judged as a manager — the employments, their terms and the departments
// first, and nothing more when they manage nobody today; else the names of
// the people they manage, for the one item a manager has.
func (s *server) readAttention(ctx context.Context, q *store.Queries, c *callerContext, acknowledged map[string]time.Time, selfApproves, solePersonalData bool) ([]gen.StaffStatsAttentionItem, error) {
	in := attentionInput{acknowledged: acknowledged, signed: map[int64]bool{}}
	var err error
	if in.register, err = loadEmployments(ctx, q); err != nil {
		return nil, err
	}
	if c.Person != nil {
		in.reports = in.reportsOf(*c.Person, c.Today)
	}
	if !c.Manage && !c.PersonalData && !c.Approve {
		if len(in.reports) == 0 {
			return []gen.StaffStatsAttentionItem{}, nil
		}
		rows, err := q.PeopleSummaries(ctx, in.reports)
		if err != nil {
			return nil, fmt.Errorf("staff: read the names of a manager's reports: %w", err)
		}
		for _, p := range rows {
			in.names[p.ID] = p.DisplayName
		}
	} else if err := in.loadPeople(ctx, q); err != nil {
		return nil, err
	}
	if err := s.readAbsenceAttention(ctx, q, c, selfApproves, solePersonalData, &in); err != nil {
		return nil, err
	}
	if !c.Manage {
		return attentionFor(in, c), nil
	}
	{
		signed, err := q.SignedContractEmployments(ctx)
		if err != nil {
			return nil, fmt.Errorf("staff: read the signed contract documents: %w", err)
		}
		for _, id := range signed {
			in.signed[id] = true
		}
		if in.failing, err = q.FailingDeletions(ctx, s.deps.Clock().Add(-retentionFailingAfter)); err != nil {
			return nil, fmt.Errorf("staff: read the failing deletions: %w", err)
		}
		if in.selfApproved, err = q.SelfApprovedAbsences(ctx, dayParam(c.Today)); err != nil {
			return nil, fmt.Errorf("staff: read the self-approved absences: %w", err)
		}
		if in.affected, err = q.HolidayAffectedBySickness(ctx, dayParam(c.Today)); err != nil {
			return nil, fmt.Errorf("staff: read the ferie affected by sickness: %w", err)
		}
		if in.accounts, err = readAccountAttention(ctx, q, c.Today); err != nil {
			return nil, err
		}
	}
	return attentionFor(in, c), nil
}

// ---- Absence (delivery B, Task 6) ------------------------------------------

// The absence items' constants: the entity the approvals items link to, the
// days an HR-only request waits before it is stalled, and the windows of the
// holiday items (plan reading 23).
const (
	approvalsEntity     = "approvals"
	hrOnlyStalledDays   = 7
	milestoneWithinDays = 7
	mainPeriodFrom      = time.April
	mainPeriodTo        = time.September
)

// requestsWaiting is how many requests the caller decides wait, and when
// the oldest was made.
type requestsWaiting struct {
	Count  int64
	Oldest time.Time
}

// readAbsenceAttention reads on q what the caller's absence items are judged
// on, for their roles alone: the requests they decide (the approvals
// inbox's count — an HR-only one only for staff:personal-data, so no count
// reveals one to anybody else), staff:personal-data's stalled HR-only
// requests and the HR-only absences the sole holder approved, the open
// cases' milestones for a manager, staff:approve and staff:manage, and
// staff:manage's holiday items. solePersonalData is whether the caller is
// the sole active holder of staff:personal-data, who decides their own
// HR-only request — so it counts as awaiting their decision, not as stalled.
func (s *server) readAbsenceAttention(ctx context.Context, q *store.Queries, c *callerContext, selfApproves, solePersonalData bool, in *attentionInput) error {
	everyone := c.Approve || c.Manage
	if everyone || c.PersonalData || len(in.reports) > 0 {
		params := store.CountRequestedAbsencesParams{
			Everyone: everyone, PersonIds: in.reports, PersonalData: c.PersonalData, SelfID: c.Person, SelfApproves: selfApproves,
			SolePersonalData: solePersonalData,
		}
		count, err := q.CountRequestedAbsences(ctx, params)
		if err != nil {
			return fmt.Errorf("staff: count the requests the caller decides: %w", err)
		}
		in.awaiting.Count = count
		if count > 0 {
			oldest, err := q.RequestedAbsences(ctx, store.RequestedAbsencesParams{
				Everyone: everyone, PersonIds: in.reports, PersonalData: c.PersonalData, SelfID: c.Person, SelfApproves: selfApproves,
				SolePersonalData: solePersonalData,
				PageSize:         1,
			})
			if err != nil {
				return fmt.Errorf("staff: read the oldest request the caller decides: %w", err)
			}
			if len(oldest) == 1 {
				in.awaiting.Oldest = oldest[0].StaffAbsence.CreatedAt
			}
		}
	}
	if c.PersonalData {
		var err error
		params := store.StalledHROnlyRequestsParams{Before: s.deps.Clock().AddDate(0, 0, -hrOnlyStalledDays)}
		if solePersonalData {
			params.SoleSelfID = c.Person
		}
		if in.stalled, err = q.StalledHROnlyRequests(ctx, params); err != nil {
			return fmt.Errorf("staff: count the stalled HR-only requests: %w", err)
		}
		if in.hrOnlySelfApproved, err = q.HROnlySelfApprovedAbsences(ctx, dayParam(c.Today)); err != nil {
			return fmt.Errorf("staff: read the HR-only absences the sole holder approved: %w", err)
		}
	}
	if everyone || len(in.reports) > 0 {
		var err error
		if in.milestones, err = q.OpenCaseMilestones(ctx, dayParam(c.Today)); err != nil {
			return fmt.Errorf("staff: read the open cases' milestones: %w", err)
		}
	}
	if c.Manage {
		return s.readHolidayAttention(ctx, q, c, in)
	}
	return nil
}

// readHolidayAttention reads staff:manage's holiday items: in January, the
// people employed today with last year's holiday remaining and no carry-over
// from it recorded — a carry-in derived by automatic carry-over counting as
// recorded (pre-flight M6); from 1 April to 30 September, the people
// employed today since before 1 June with fewer approved holiday working
// days in 1 June – 30 September than their scheme's main period converted
// (ferieloven § 7 (1), reading 23).
func (s *server) readHolidayAttention(ctx context.Context, q *store.Queries, c *callerContext, in *attentionInput) error {
	today := c.Today
	year := today.Year()
	in.holidayYear = year
	active := in.activePeople(today)
	ids := slices.Sorted(maps.Keys(active))
	if today.Month() == time.January {
		for _, id := range ids {
			account, err := readAccountInput(ctx, q, c.Settings, id)
			if err != nil {
				return err
			}
			last := accountFor(account, year-1)
			if last.Remaining <= 0 || accountFor(account, year).CarriedInDerived > 0 {
				continue
			}
			recorded := slices.ContainsFunc(account.Adjustments, func(a accountAdjustment) bool {
				return carryOverReason(a.Reason) && a.FromYear != nil && *a.FromYear == year-1
			})
			if !recorded {
				in.untaken = append(in.untaken, id)
			}
		}
	}
	if today.Month() < mainPeriodFrom || today.Month() > mainPeriodTo {
		return nil
	}
	june1 := time.Date(year, time.June, 1, 0, 0, 0, 0, time.UTC)
	sep30 := time.Date(year, time.September, 30, 0, 0, 0, 0, time.UTC)
	schemes, err := q.ListHolidaySchemes(ctx)
	if err != nil {
		return fmt.Errorf("staff: read the holiday schemes: %w", err)
	}
	mainPeriod := map[int64]int{}
	var fallback int
	for _, sc := range schemes {
		mainPeriod[sc.ID] = convertVirkedager(int(sc.MainPeriodVirkedager))
		if sc.IsDefault {
			fallback = mainPeriod[sc.ID]
		}
	}
	rows, err := q.AbsencesBetween(ctx, store.AbsencesBetweenParams{FromDay: dayParam(june1), ToDay: dayParam(sep30)})
	if err != nil {
		return fmt.Errorf("staff: read the summer's absences: %w", err)
	}
	byPerson := map[int64][]store.AbsencesBetweenRow{}
	for _, a := range rows {
		if a.Kind == kindHoliday && a.Status == absenceApproved {
			byPerson[a.PersonID] = append(byPerson[a.PersonID], a)
		}
	}
	jan1, dec31 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC), time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	for _, id := range ids {
		// Owed by someone employed today since before 1 June; the main
		// period is that of the scheme the holiday account reads — the
		// earliest employment's in the year (whole-branch review).
		eligible := false
		var earliest *store.StaffEmployment
		for i, e := range in.employments {
			if e.PersonID != id {
				continue
			}
			if inForce(e, today) && civil(e.StartDate).Before(june1) {
				eligible = true
			}
			if civil(e.StartDate).After(dec31) || (e.EndDate.Valid && e.EndDate.Time.Before(jan1)) {
				continue
			}
			if earliest == nil || civil(e.StartDate).Before(civil(earliest.StartDate)) {
				earliest = &in.employments[i]
			}
		}
		if !eligible || earliest == nil {
			continue
		}
		days := fallback
		if earliest.HolidaySchemeID != nil {
			days = mainPeriod[*earliest.HolidaySchemeID]
		}
		owed := &days
		var taken int64
		for d := june1; !d.After(sep30); d = d.AddDate(0, 0, 1) {
			if worksOn(d) {
				taken += sharesOn(byPerson[id], d).Holiday
			}
		}
		if taken < int64(*owed)*wholeDay {
			in.mainShort = append(in.mainShort, id)
		}
	}
	return nil
}

// awaitingDecision is the one item of the requests the caller decides,
// counted, dated the oldest's making; none when nothing waits.
func (in attentionInput) awaitingDecision() []gen.StaffStatsAttentionItem {
	if in.awaiting.Count == 0 {
		return nil
	}
	count := int32(in.awaiting.Count)
	return []gen.StaffStatsAttentionItem{{
		Id: string(gen.AbsenceAwaitingDecision), Type: gen.AbsenceAwaitingDecision, Title: string(gen.AbsenceAwaitingDecision),
		OccurredAt: in.awaiting.Oldest, EntityId: approvalsEntity, Count: &count,
	}}
}

// hrOnlyStalled is staff:personal-data's one item of the HR-only requests
// waiting more than hrOnlyStalledDays — the caller's own included, so a
// holder whose own request another holder must decide sees it stall (open
// question 9), unless the caller is the sole holder, who decides it
// themselves — dated the oldest's seventh day.
func (in attentionInput) hrOnlyStalled() []gen.StaffStatsAttentionItem {
	if in.stalled.Waiting == 0 {
		return nil
	}
	count := in.stalled.Waiting
	return []gen.StaffStatsAttentionItem{{
		Id: string(gen.HrOnlyAwaitingDecision), Type: gen.HrOnlyAwaitingDecision, Title: string(gen.HrOnlyAwaitingDecision),
		OccurredAt: in.stalled.Oldest.AddDate(0, 0, hrOnlyStalledDays), EntityId: approvalsEntity, Count: &count,
	}}
}

// milestonesDue is the open cases' milestones the caller is shown: due by
// Task 5's rule (milestoneDue — the case open and lasted to within a week
// of the date, neither done nor waived; pre-flight I5) and dated within
// milestoneWithinDays of today or past. Every person's for staff:manage and
// staff:approve, a manager's reports' for a manager; never the end of the
// arbeidsgiverperiode, a date and not an action.
func (in attentionInput) milestonesDue(c *callerContext) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, m := range in.milestones {
		if m.Kind == milestoneEmployerPeriodEnd {
			continue
		}
		if !c.Manage && !c.Approve && !slices.Contains(in.reports, m.PersonID) {
			continue
		}
		due := civilPtr(m.DueDate)
		if !milestoneDue(true, false, civil(m.LastDay), due) || due.After(c.Today.AddDate(0, 0, milestoneWithinDays)) {
			continue
		}
		out = append(out, gen.StaffStatsAttentionItem{
			Id:   fmt.Sprintf("%s/%d/%s", gen.SickMilestoneDue, m.CaseID, m.Kind),
			Type: gen.SickMilestoneDue, Title: m.DisplayName, OccurredAt: *due, EntityId: strconv.FormatInt(m.PersonID, 10),
		})
	}
	return out
}

// holidayItems is staff:manage's holiday items, each a person's.
func (in attentionInput) holidayItems() []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	jan1 := time.Date(in.holidayYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	apr1 := time.Date(in.holidayYear, mainPeriodFrom, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range in.untaken {
		out = append(out, personItem(gen.HolidayUntaken, id, id, in.names[id], jan1))
	}
	for _, id := range in.mainShort {
		out = append(out, personItem(gen.MainPeriodShort, id, id, in.names[id], apr1))
	}
	return out
}
