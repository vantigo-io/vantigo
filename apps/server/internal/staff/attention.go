package staff

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the dashboard's attention items for the register (design S20,
// plan Task 6), shaped per caller: staff:manage's, staff:personal-data's and a
// manager's for the people they manage today. The rules are pure over the
// register's snapshot (stats.go) and the caller's today; the absence and the
// account items arrive with deliveries B and C. Each item's entityId is a
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
// each with the sentence its item carries.
var headcountThresholds = []struct {
	key   string
	count int
	title string
}{
	{"5", 5, "5 or more employees: a whistleblowing routine (varslingsrutine) and a safety representative (verneombud) are required"},
	{"30", 30, "30 or more employees: a working environment committee (arbeidsmiljøutvalg, AMU) is required"},
	{"50", 50, "50 employees: above fifty, the activity and reporting duty on equality (aktivitets- og redegjørelsesplikten, ARP) applies"},
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
	}
	if c.PersonalData {
		items = append(items, in.reassessmentDue(c.Today)...)
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
// agreed at the start, it lasts under a month or the person is hired out.
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
// out, or the employment — by its end, else a temporary row's expected end —
// lasts under a month.
func contractDueAtOnce(e store.StaffEmployment, t *store.StaffEmploymentTerm) bool {
	if hiredOut(t) {
		return true
	}
	end := e.EndDate
	if !end.Valid && t != nil && t.EmploymentType == "midlertidig" {
		end = t.ExpectedEndDate
	}
	start := civil(e.StartDate)
	return end.Valid && end.Time.Before(addMonths(start, 1).AddDate(0, 0, -1))
}

// probationEnding is every employment in force today whose probation — the
// extension's end when there is one — ends today or within soonDays: all of
// them for staff:manage, and the people the caller manages today for a
// manager.
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
		if !c.Manage && !slices.Contains(in.graph.managerOf(e.PersonID, c.Today), *c.Person) {
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

// reportsWithoutManager is every person with an employment in force today
// that has no manager in employment today (design §9): neither the manager
// its terms name nor the manager of its department or of a department above
// it — whoever is not the person. The head of a top-level department, whom
// by the structure nobody manages, is not listed. One per person, dated the
// first such employment's start.
func (in attentionInput) reportsWithoutManager(today time.Time) []gen.StaffStatsAttentionItem {
	active := in.activePeople(today)
	heads := map[int64]bool{}
	for _, d := range in.graph.departments {
		if d.ParentID == nil && d.Manager != nil {
			heads[*d.Manager] = true
		}
	}
	listed := map[int64]bool{}
	var out []gen.StaffStatsAttentionItem
	for _, e := range in.employments {
		if listed[e.PersonID] || heads[e.PersonID] || !inForce(e, today) {
			continue
		}
		if in.hasManager(termsInForce(e, in.chains[e.ID], today), e.PersonID, active) {
			continue
		}
		listed[e.PersonID] = true
		out = append(out, personItem(gen.ReportsWithoutManager, e.PersonID, e.PersonID, in.names[e.PersonID], civil(e.StartDate)))
	}
	return out
}

// hasManager reports whether terms t give person a manager in employment
// today (active): the one t names, else the first department from t's up
// whose manager is someone else in employment.
func (in attentionInput) hasManager(t *store.StaffEmploymentTerm, person int64, active map[int64]int64) bool {
	if t == nil {
		return false
	}
	employed := func(m *int64) bool {
		_, ok := active[*m]
		return ok && *m != person
	}
	if t.ManagerPersonID != nil && employed(t.ManagerPersonID) {
		return true
	}
	if t.DepartmentID == nil {
		return false
	}
	visited := map[int64]bool{}
	for d, ok := in.graph.departments[*t.DepartmentID]; ok && !visited[d.ID]; d, ok = in.graph.parentOf(d) {
		visited[d.ID] = true
		if d.Manager != nil && employed(d.Manager) {
			return true
		}
	}
	return false
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
		if _, done := in.acknowledged[h.key]; done || int(headcount) < h.count {
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
	if !caller.Manage && !caller.PersonalData && caller.Person == nil {
		return gen.GetStaffStatsAttention200JSONResponse([]gen.StaffStatsAttentionItem{}), nil
	}
	acknowledged, err := parseAcknowledged(caller.Settings.ThresholdsAcknowledged)
	if err != nil {
		return nil, err
	}
	var items []gen.StaffStatsAttentionItem
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		in := attentionInput{acknowledged: acknowledged, signed: map[int64]bool{}}
		if in.register, err = loadRegister(ctx, q); err != nil {
			return err
		}
		signed, err := q.SignedContractEmployments(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the signed contract documents: %w", err)
		}
		for _, id := range signed {
			in.signed[id] = true
		}
		if in.failing, err = q.FailingDeletions(ctx, s.deps.Clock().Add(-retentionFailingAfter)); err != nil {
			return fmt.Errorf("staff: read the failing deletions: %w", err)
		}
		items = attentionFor(in, caller)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffStatsAttention200JSONResponse(items), nil
}
