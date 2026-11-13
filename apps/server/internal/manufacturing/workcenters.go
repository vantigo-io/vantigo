package manufacturing

// The work center operations (manufacturing design M8, M9): the CRUD of a
// work center, archive and reactivate, and its closures.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// The work center's limits (M8, M9).
const (
	maxNameLength     = 200
	maxClosureNote    = 500
	maxClosures       = 100
	maxDayIntervals   = 2
	defaultCapacity   = 1
	defaultEfficiency = 100
	maxEfficiency     = 500
	statusActive      = "active"
	statusArchived    = "archived"
	workCenterWhat    = "work center"
	workCenterTitle   = "The work center was not saved"
	closureTitle      = "The closure was not saved"
	codeUniqueIndex   = "ux_work_centers_code"
	minutesPerDay     = 24 * 60
	patternGridMinute = 15
)

// workCenterCode is a work center's code after upper-casing (M8).
var workCenterCode = regexp.MustCompile(`^[A-Z0-9]{1,16}$`)

// workCenterFields is a work center's input, whichever operation sent it:
// ManufacturingWorkCenterInput and ManufacturingWorkCenterUpdate carry the
// same fields.
type workCenterFields struct {
	Code, Name                         string
	SiteID                             int64
	Capacity, TimeEfficiency           *int32
	SetupMinutes, CleanupMinutes       *string
	MachineCostPerHour, LabourCostRate json.RawMessage
	Pattern                            *gen.ManufacturingWeekPattern
}

// workCenterValues is a work center's input judged: what is stored. A rate
// whose keep is true was absent and keeps the stored value.
type workCenterValues struct {
	code, name              string
	siteID                  int64
	capacity, efficiency    int32
	setup, cleanup          *big.Rat
	machine, labour         *big.Rat
	keepMachine, keepLabour bool
	pattern                 []byte
}

// judgeWorkCenter is the input's rules (M8, M9) but the site's, which the
// caller reads from Stock: every failing field at once. A rate sent by a
// caller without costs-view is the 403 costs_view_required before any field
// is judged (M38).
func (s *server) judgeWorkCenter(ctx context.Context, in workCenterFields, schema string) (workCenterValues, error) {
	if (len(in.MachineCostPerHour) > 0 || len(in.LabourCostRate) > 0) && !s.has(ctx, permCostsView) {
		return workCenterValues{}, costsViewRequired()
	}
	f := s.unknownProperties(ctx, schema)
	v := workCenterValues{siteID: in.SiteID, capacity: defaultCapacity, efficiency: defaultEfficiency}

	v.code = strings.ToUpper(strings.TrimSpace(in.Code))
	if !workCenterCode.MatchString(v.code) {
		f.add("code", "1–16 characters A–Z and 0–9")
	}
	v.name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(v.name); n < 1 || n > maxNameLength {
		f.add("name", "1–200 characters")
	}
	if in.Capacity != nil {
		v.capacity = *in.Capacity
		if v.capacity < 1 {
			f.add("capacity", "Must be 1 or more")
		}
	}
	if in.TimeEfficiency != nil {
		v.efficiency = *in.TimeEfficiency
		if v.efficiency < 1 || v.efficiency > maxEfficiency {
			f.add("timeEfficiency", "Must be from 1 to 500")
		}
	}
	v.setup = judgeMinutes(&f, "setupMinutes", in.SetupMinutes)
	v.cleanup = judgeMinutes(&f, "cleanupMinutes", in.CleanupMinutes)
	v.machine, v.keepMachine = judgeRate(&f, "machineCostPerHour", in.MachineCostPerHour)
	v.labour, v.keepLabour = judgeRate(&f, "labourCostPerHour", in.LabourCostRate)

	pattern := defaultPattern()
	if in.Pattern != nil {
		pattern = judgePattern(&f, *in.Pattern)
	}
	b, err := json.Marshal(pattern)
	if err != nil {
		return workCenterValues{}, fmt.Errorf("manufacturing: encode a pattern: %w", err)
	}
	v.pattern = b

	if f.failed() {
		return workCenterValues{}, refuseInvalid(workCenterTitle, f)
	}
	return v, nil
}

// judgeMinutes is an optional minutes field, 0 or more with at most two
// decimals; absent is 0.
func judgeMinutes(f *fieldErrors, field string, in *string) *big.Rat {
	if in == nil {
		return new(big.Rat)
	}
	r, msg := parseDecimal(*in, minutePlaces, maxMinutes)
	f.add(field, msg)
	return r
}

// judgeRate is a cost rate's raw property: absent keeps the stored value,
// null clears it, else a decimal of 0 or more with at most two places.
func judgeRate(f *fieldErrors, field string, raw json.RawMessage) (*big.Rat, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if string(raw) == "null" {
		return nil, false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		f.add(field, "Must be decimal text or null")
		return nil, false
	}
	r, msg := parseDecimal(text, ratePlaces, maxRate)
	f.add(field, msg)
	return r, false
}

// defaultPattern is Monday to Friday 07:00–15:00, the weekend closed (M9).
func defaultPattern() gen.ManufacturingWeekPattern {
	day := func() []gen.ManufacturingInterval { return []gen.ManufacturingInterval{{Start: "07:00", End: "15:00"}} }
	return gen.ManufacturingWeekPattern{
		Monday: day(), Tuesday: day(), Wednesday: day(), Thursday: day(), Friday: day(),
		Saturday: []gen.ManufacturingInterval{}, Sunday: []gen.ManufacturingInterval{},
	}
}

// judgePattern is a weekly pattern's rules (M9): per day at most two
// intervals of HH:MM on the 15-minute grid, each starting before it ends
// (24:00 may end one), the two not overlapping — sorted by start.
func judgePattern(f *fieldErrors, p gen.ManufacturingWeekPattern) gen.ManufacturingWeekPattern {
	days := []struct {
		name string
		day  *[]gen.ManufacturingInterval
	}{
		{"monday", &p.Monday}, {"tuesday", &p.Tuesday}, {"wednesday", &p.Wednesday}, {"thursday", &p.Thursday},
		{"friday", &p.Friday}, {"saturday", &p.Saturday}, {"sunday", &p.Sunday},
	}
	for _, d := range days {
		*d.day = judgeDay(f, "pattern."+d.name, *d.day)
	}
	return p
}

func judgeDay(f *fieldErrors, field string, day []gen.ManufacturingInterval) []gen.ManufacturingInterval {
	if day == nil {
		return []gen.ManufacturingInterval{}
	}
	if len(day) > maxDayIntervals {
		f.add(field, "At most two intervals a day")
		return day
	}
	ok := true
	for i, in := range day {
		at := fmt.Sprintf("%s[%d]", field, i)
		start, startOK := clockMinutes(in.Start, false)
		end, endOK := clockMinutes(in.End, true)
		switch {
		case !startOK:
			f.add(at+".start", "HH:MM on the quarter hour, 00:00–23:45")
			ok = false
		case !endOK:
			f.add(at+".end", "HH:MM on the quarter hour, 00:15–24:00")
			ok = false
		case start >= end:
			f.add(at+".end", "Must be after the start")
			ok = false
		}
	}
	if !ok {
		return day
	}
	sorted := slices.Clone(day)
	slices.SortFunc(sorted, func(a, b gen.ManufacturingInterval) int {
		x, _ := clockMinutes(a.Start, false)
		y, _ := clockMinutes(b.Start, false)
		return x - y
	})
	if len(sorted) == maxDayIntervals {
		firstEnd, _ := clockMinutes(sorted[0].End, true)
		secondStart, _ := clockMinutes(sorted[1].Start, false)
		if secondStart < firstEnd {
			f.add(field, "The two intervals overlap")
		}
	}
	return sorted
}

// clockMinutes is HH:MM as minutes after midnight, on the 15-minute grid;
// 24:00 only as an end.
func clockMinutes(s string, end bool) (int, bool) {
	if len(s) != len("00:00") || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h, m := int(s[0]-'0')*10+int(s[1]-'0'), int(s[3]-'0')*10+int(s[4]-'0')
	total := h*60 + m
	if m >= 60 || m%patternGridMinute != 0 || total > minutesPerDay || (!end && total == minutesPerDay) {
		return 0, false
	}
	return total, true
}

// siteOrRefuse is the active site id names, read before any transaction, or
// the 400 site_unknown on siteId.
func (s *server) siteOrRefuse(ctx context.Context, id int64) ([]contracts.StockSite, error) {
	sites, err := s.activeSites(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := siteOf(sites, id); !ok {
		return nil, refuseField(workCenterTitle, "siteId", codeSiteUnknown, "Not an active Inventory site")
	}
	return sites, nil
}

// workCenterOf is a work center as the contract answers it, its rates shaped
// by costs (M38) and its site named from sites.
func workCenterOf(w store.ManufacturingWorkCenter, sites []contracts.StockSite, costs bool) gen.ManufacturingWorkCenter {
	var pattern gen.ManufacturingWeekPattern
	if json.Unmarshal(w.Pattern, &pattern) != nil {
		pattern = defaultPattern()
	}
	for _, day := range []*[]gen.ManufacturingInterval{&pattern.Monday, &pattern.Tuesday, &pattern.Wednesday,
		&pattern.Thursday, &pattern.Friday, &pattern.Saturday, &pattern.Sunday} {
		if *day == nil {
			*day = []gen.ManufacturingInterval{}
		}
	}
	site, _ := siteOf(sites, w.SiteID)
	return gen.ManufacturingWorkCenter{
		Id: w.ID, Code: w.Code, Name: w.Name, SiteId: w.SiteID, SiteCode: site.Code, SiteName: site.Name,
		Capacity: w.Capacity, TimeEfficiency: w.TimeEfficiency,
		SetupMinutes: numericText(w.SetupMinutes, minutePlaces), CleanupMinutes: numericText(w.CleanupMinutes, minutePlaces),
		MachineCostPerHour: rateJSON(costs, w.MachineCostPerHour), LabourCostPerHour: rateJSON(costs, w.LabourCostPerHour),
		Pattern: pattern, WorkingState: gen.ManufacturingWorkingState(w.WorkingState), Status: gen.ManufacturingStatus(w.Status),
		Revision: w.Revision, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
}

// rateJSON is a stored rate, cost-shaped.
func rateJSON(costs bool, n pgtype.Numeric) json.RawMessage {
	return amountJSON(costs, ratOf(n))
}

// workCenterAnswer is a written work center as the contract answers it, its
// site named from a read after the transaction.
func (s *server) workCenterAnswer(ctx context.Context, w store.ManufacturingWorkCenter, sites []contracts.StockSite) (gen.ManufacturingWorkCenter, error) {
	if sites == nil {
		var err error
		if sites, err = s.activeSites(ctx); err != nil {
			return gen.ManufacturingWorkCenter{}, err
		}
	}
	return workCenterOf(w, sites, s.has(ctx, permCostsView)), nil
}

// lockWorkCenter is the work center's row FOR UPDATE under the revision the
// client read: a 404 for an unknown id, a 409 for a stale revision.
func lockWorkCenter(ctx context.Context, txq *store.Queries, id int64, revision int32) (store.ManufacturingWorkCenter, error) {
	w, err := txq.GetWorkCenterForUpdate(ctx, id)
	if noRows(err) {
		return w, notFound()
	}
	if err != nil {
		return w, err
	}
	if w.Revision != revision {
		return w, revisionConflict(workCenterWhat, w.Revision, revision)
	}
	return w, nil
}

// codeTaken is the 409 work_center_code_taken.
func codeTaken(code string) error {
	return conflict(codeWorkCenterCodeTaken, "The code is taken", fmt.Sprintf("Another work center has the code %s.", code))
}

// ListManufacturingWorkCenters is the work centers in a status (active by
// default), at a site, matching q, by code.
func (s *server) ListManufacturingWorkCenters(ctx context.Context, req gen.ListManufacturingWorkCentersRequestObject) (gen.ListManufacturingWorkCentersResponseObject, error) {
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, answers[gen.ListManufacturingWorkCentersResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingWorkCentersResponseObject {
				return gen.ListManufacturingWorkCenters503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	params := store.ListWorkCentersParams{Status: statusFilter(req.Params.Status), SiteID: req.Params.SiteId}
	if req.Params.Q != nil && strings.TrimSpace(*req.Params.Q) != "" {
		q := strings.TrimSpace(*req.Params.Q)
		params.Q = &q
	}
	rows, err := store.New(s.deps.Pool).ListWorkCenters(ctx, params)
	if err != nil {
		return nil, err
	}
	costs := s.has(ctx, permCostsView)
	items := make([]gen.ManufacturingWorkCenter, 0, len(rows))
	for _, w := range rows {
		items = append(items, workCenterOf(w, sites, costs))
	}
	return gen.ListManufacturingWorkCenters200JSONResponse{Items: items}, nil
}

// statusFilter is a list's status: active by default, nil for all.
func statusFilter(f *gen.ManufacturingStatusFilter) *string {
	status := statusActive
	if f != nil {
		switch *f {
		case gen.ManufacturingStatusFilterAll:
			return nil
		case gen.ManufacturingStatusFilterArchived:
			status = statusArchived
		}
	}
	return &status
}

// createAnswers are the create's typed refusals.
func createAnswers() answers[gen.CreateManufacturingWorkCenterResponseObject] {
	return answers[gen.CreateManufacturingWorkCenterResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterResponseObject {
			return gen.CreateManufacturingWorkCenter400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterResponseObject {
			return gen.CreateManufacturingWorkCenter403ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterResponseObject {
			return gen.CreateManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterResponseObject {
			return gen.CreateManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CreateManufacturingWorkCenter creates a work center (M8).
func (s *server) CreateManufacturingWorkCenter(ctx context.Context, req gen.CreateManufacturingWorkCenterRequestObject) (gen.CreateManufacturingWorkCenterResponseObject, error) {
	in := req.Body
	v, err := s.judgeWorkCenter(ctx, workCenterFields{
		Code: in.Code, Name: in.Name, SiteID: in.SiteId, Capacity: in.Capacity, TimeEfficiency: in.TimeEfficiency,
		SetupMinutes: in.SetupMinutes, CleanupMinutes: in.CleanupMinutes,
		MachineCostPerHour: in.MachineCostPerHour, LabourCostRate: in.LabourCostPerHour, Pattern: in.Pattern,
	}, workCenterInputSchema)
	if err != nil {
		return answer(err, createAnswers())
	}
	sites, err := s.siteOrRefuse(ctx, v.siteID)
	if err != nil {
		return answer(err, createAnswers())
	}
	var created store.ManufacturingWorkCenter
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		taken, err := txq.WorkCenterCodeTaken(ctx, store.WorkCenterCodeTakenParams{Code: v.code})
		if err != nil {
			return err
		}
		if taken {
			return codeTaken(v.code)
		}
		created, err = txq.InsertWorkCenter(ctx, store.InsertWorkCenterParams{
			Code: v.code, Name: v.name, SiteID: v.siteID, Capacity: v.capacity, TimeEfficiency: v.efficiency,
			SetupMinutes: numeric(v.setup, minutePlaces), CleanupMinutes: numeric(v.cleanup, minutePlaces),
			MachineCostPerHour: numeric(v.machine, ratePlaces), LabourCostPerHour: numeric(v.labour, ratePlaces),
			Pattern: v.pattern, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, codeUniqueIndex) {
			return codeTaken(v.code)
		}
		return err
	})
	if err != nil {
		return answer(err, createAnswers())
	}
	return gen.CreateManufacturingWorkCenter201JSONResponse(workCenterOf(created, sites, s.has(ctx, permCostsView))), nil
}

// GetManufacturingWorkCenter is one work center.
func (s *server) GetManufacturingWorkCenter(ctx context.Context, req gen.GetManufacturingWorkCenterRequestObject) (gen.GetManufacturingWorkCenterResponseObject, error) {
	w, err := store.New(s.deps.Pool).GetWorkCenter(ctx, req.Id)
	if noRows(err) {
		return gen.GetManufacturingWorkCenter404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.workCenterAnswer(ctx, w, nil)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingWorkCenterResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingWorkCenterResponseObject {
				return gen.GetManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingWorkCenter200JSONResponse(out), nil
}

// updateAnswers are the update's typed refusals.
func updateAnswers() answers[gen.UpdateManufacturingWorkCenterResponseObject] {
	return answers[gen.UpdateManufacturingWorkCenterResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterResponseObject {
			return gen.UpdateManufacturingWorkCenter400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterResponseObject {
			return gen.UpdateManufacturingWorkCenter403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateManufacturingWorkCenterResponseObject {
			return gen.UpdateManufacturingWorkCenter404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterResponseObject {
			return gen.UpdateManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterResponseObject {
			return gen.UpdateManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// UpdateManufacturingWorkCenter replaces a work center's fields under its
// revision. A rate left out keeps what is stored; the working state and the
// status are never an edit's (M8).
func (s *server) UpdateManufacturingWorkCenter(ctx context.Context, req gen.UpdateManufacturingWorkCenterRequestObject) (gen.UpdateManufacturingWorkCenterResponseObject, error) {
	in := req.Body
	v, err := s.judgeWorkCenter(ctx, workCenterFields{
		Code: in.Code, Name: in.Name, SiteID: in.SiteId, Capacity: in.Capacity, TimeEfficiency: in.TimeEfficiency,
		SetupMinutes: in.SetupMinutes, CleanupMinutes: in.CleanupMinutes,
		MachineCostPerHour: in.MachineCostPerHour, LabourCostRate: in.LabourCostPerHour, Pattern: in.Pattern,
	}, workCenterUpdateSchema)
	if err != nil {
		return answer(err, updateAnswers())
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, updateAnswers())
	}
	var updated store.ManufacturingWorkCenter
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		// The BoM graph first, as every BoM write takes it before reading
		// the work centers its operations name: a site change and a BoM
		// write do not pass each other (M12a).
		if err := txq.LockBomGraph(ctx); err != nil {
			return err
		}
		w, err := lockWorkCenter(ctx, txq, req.Id, in.Revision)
		if err != nil {
			return err
		}
		if v.siteID != w.SiteID {
			// A work center whose site was archived since keeps it through an
			// edit; a move goes to an active site only.
			if _, ok := siteOf(sites, v.siteID); !ok {
				return refuseField(workCenterTitle, "siteId", codeSiteUnknown, "Not an active Inventory site")
			}
			used, err := txq.CountActiveBomsAtWorkCenter(ctx, &w.ID)
			if err != nil {
				return err
			}
			if used > 0 {
				return refuseField(workCenterTitle, "siteId", codeWorkCenterOtherSite,
					fmt.Sprintf("%d active bill(s) of materials have operations at %s; its site does not change while they do", used, w.Code))
			}
			// A group holds centers of its own site (manufacturing phase 2 M117).
			groups, err := txq.CountGroupsOfWorkCenter(ctx, w.ID)
			if err != nil {
				return err
			}
			if groups > 0 {
				return refuseField(workCenterTitle, "siteId", codeWorkCenterOtherSite,
					fmt.Sprintf("%s is a member of %d work center group(s); its site does not change while it is", w.Code, groups))
			}
		}
		taken, err := txq.WorkCenterCodeTaken(ctx, store.WorkCenterCodeTakenParams{Code: v.code, ID: w.ID})
		if err != nil {
			return err
		}
		if taken {
			return codeTaken(v.code)
		}
		machine, labour := numeric(v.machine, ratePlaces), numeric(v.labour, ratePlaces)
		if v.keepMachine {
			machine = w.MachineCostPerHour
		}
		if v.keepLabour {
			labour = w.LabourCostPerHour
		}
		updated, err = txq.UpdateWorkCenter(ctx, store.UpdateWorkCenterParams{
			ID: w.ID, Code: v.code, Name: v.name, SiteID: v.siteID, Capacity: v.capacity, TimeEfficiency: v.efficiency,
			SetupMinutes: numeric(v.setup, minutePlaces), CleanupMinutes: numeric(v.cleanup, minutePlaces),
			MachineCostPerHour: machine, LabourCostPerHour: labour, Pattern: v.pattern, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, codeUniqueIndex) {
			return codeTaken(v.code)
		}
		if err != nil {
			return err
		}
		// The board draws the pattern's days (M36): the site it was at and
		// the site it is at both hear of it.
		n := newNotices()
		n.sites[w.SiteID], n.sites[updated.SiteID] = true, true
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, updateAnswers())
	}
	return gen.UpdateManufacturingWorkCenter200JSONResponse(workCenterOf(updated, sites, s.has(ctx, permCostsView))), nil
}

// DeleteManufacturingWorkCenter deletes a work center no BoM operation names;
// one that is named is archived instead (work_center_in_use, M8).
func (s *server) DeleteManufacturingWorkCenter(ctx context.Context, req gen.DeleteManufacturingWorkCenterRequestObject) (gen.DeleteManufacturingWorkCenterResponseObject, error) {
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The BoM graph first, as every BoM write takes it before reading the
		// work centers its operations name: a delete does not pass a BoM write
		// about to name the work center (M14a).
		if err := txq.LockBomGraph(ctx); err != nil {
			return err
		}
		w, err := lockWorkCenter(ctx, txq, req.Id, req.Params.Revision)
		if err != nil {
			return err
		}
		used, err := txq.CountOperationsOfWorkCenter(ctx, w.ID)
		if err != nil {
			return err
		}
		if used > 0 {
			return conflict(codeWorkCenterInUse, "The work center is in use",
				fmt.Sprintf("%d BoM operation(s), work order(s) or time log(s) name %s. Archive it instead.", used, w.Code))
		}
		_, err = txq.DeleteWorkCenter(ctx, w.ID)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.DeleteManufacturingWorkCenterResponseObject]{
			notFound: func() gen.DeleteManufacturingWorkCenterResponseObject {
				return gen.DeleteManufacturingWorkCenter404Response{}
			},
			conflict: func(p gen.ManufacturingProblem) gen.DeleteManufacturingWorkCenterResponseObject {
				return gen.DeleteManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.DeleteManufacturingWorkCenter204Response{}, nil
}

// ArchiveManufacturingWorkCenter archives a work center under its revision;
// one with open work or blocked is work_center_has_open_work.
func (s *server) ArchiveManufacturingWorkCenter(ctx context.Context, req gen.ArchiveManufacturingWorkCenterRequestObject) (gen.ArchiveManufacturingWorkCenterResponseObject, error) {
	reply := answers[gen.ArchiveManufacturingWorkCenterResponseObject]{
		notFound: func() gen.ArchiveManufacturingWorkCenterResponseObject {
			return gen.ArchiveManufacturingWorkCenter404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ArchiveManufacturingWorkCenterResponseObject {
			return gen.ArchiveManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ArchiveManufacturingWorkCenterResponseObject {
			return gen.ArchiveManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
		},
	}
	// The sites are read before the transaction: an Inventory that is not
	// composed refuses before anything changes.
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, reply)
	}
	var archived store.ManufacturingWorkCenter
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		w, err := lockWorkCenter(ctx, txq, req.Id, req.Body.Revision)
		if err != nil {
			return err
		}
		if w.Status == statusArchived {
			archived = w
			return nil
		}
		// Archiving is refused while a work order not done or cancelled
		// names the work center (work_center_has_open_work, M8).
		open, err := txq.CountOpenWorkOrdersAtWorkCenter(ctx, w.ID)
		if err != nil {
			return err
		}
		if open > 0 {
			return conflict(codeWorkCenterHasOpenWork, "The work center has open work",
				fmt.Sprintf("%d work order(s) not done or cancelled are at this work center. Finish or cancel their orders first.", open))
		}
		// A blocked work center is refused too: its own downtime log is
		// open, and only an unblock ends it.
		if w.WorkingState == stateBlocked {
			return conflict(codeWorkCenterHasOpenWork, "The work center is blocked",
				"The work center is blocked and its downtime is still running. Unblock it first.")
		}
		archived, err = txq.SetWorkCenterStatus(ctx, store.SetWorkCenterStatusParams{ID: w.ID, Status: statusArchived, Now: s.deps.Clock()})
		return err
	})
	if err != nil {
		return answer(err, reply)
	}
	return gen.ArchiveManufacturingWorkCenter200JSONResponse(workCenterOf(archived, sites, s.has(ctx, permCostsView))), nil
}

// ReactivateManufacturingWorkCenter reactivates an archived work center,
// refused while its site is no longer an active Inventory site
// (site_archived).
func (s *server) ReactivateManufacturingWorkCenter(ctx context.Context, req gen.ReactivateManufacturingWorkCenterRequestObject) (gen.ReactivateManufacturingWorkCenterResponseObject, error) {
	reply := answers[gen.ReactivateManufacturingWorkCenterResponseObject]{
		notFound: func() gen.ReactivateManufacturingWorkCenterResponseObject {
			return gen.ReactivateManufacturingWorkCenter404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingWorkCenterResponseObject {
			return gen.ReactivateManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingWorkCenterResponseObject {
			return gen.ReactivateManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
		},
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, reply)
	}
	var active store.ManufacturingWorkCenter
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		w, err := lockWorkCenter(ctx, txq, req.Id, req.Body.Revision)
		if err != nil {
			return err
		}
		if w.Status == statusActive {
			active = w
			return nil
		}
		if _, ok := siteOf(sites, w.SiteID); !ok {
			return conflict(codeSiteArchived, "The work center's site is archived",
				"Its Inventory site is no longer active. Move the work center to an active site first.")
		}
		active, err = txq.SetWorkCenterStatus(ctx, store.SetWorkCenterStatusParams{ID: w.ID, Status: statusActive, Now: s.deps.Clock()})
		return err
	})
	if err != nil {
		return answer(err, reply)
	}
	return gen.ReactivateManufacturingWorkCenter200JSONResponse(workCenterOf(active, sites, s.has(ctx, permCostsView))), nil
}

// closureOf is a closure as the contract answers it.
func closureOf(c store.ManufacturingWorkCenterClosure) gen.ManufacturingClosure {
	return gen.ManufacturingClosure{
		Id: c.ID, WorkCenterId: c.WorkCenterID, FromDate: openapi_types.Date{Time: c.FromDate.Time},
		ToDate: openapi_types.Date{Time: c.ToDate.Time}, Note: c.Note,
	}
}

// ListManufacturingClosures is a work center's closures, by first day.
func (s *server) ListManufacturingClosures(ctx context.Context, req gen.ListManufacturingClosuresRequestObject) (gen.ListManufacturingClosuresResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetWorkCenter(ctx, req.Id); noRows(err) {
		return gen.ListManufacturingClosures404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := q.ListClosures(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	items := make([]gen.ManufacturingClosure, 0, len(rows))
	for _, c := range rows {
		items = append(items, closureOf(c))
	}
	return gen.ListManufacturingClosures200JSONResponse{Items: items}, nil
}

// CreateManufacturingClosure adds a closure to a work center, at most 100
// each, counted under the work center's row lock (M9).
func (s *server) CreateManufacturingClosure(ctx context.Context, req gen.CreateManufacturingClosureRequestObject) (gen.CreateManufacturingClosureResponseObject, error) {
	reply := answers[gen.CreateManufacturingClosureResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingClosureResponseObject {
			return gen.CreateManufacturingClosure400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.CreateManufacturingClosureResponseObject {
			return gen.CreateManufacturingClosure404Response{}
		},
	}
	in := req.Body
	f := s.unknownProperties(ctx, closureInputSchema)
	if in.ToDate.Before(in.FromDate.Time) {
		f.add("toDate", "Must be on or after fromDate")
	}
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	if utf8.RuneCountInString(note) > maxClosureNote {
		f.add("note", "At most 500 characters")
	}
	if f.failed() {
		return answer(refuseInvalid(closureTitle, f), reply)
	}
	var created store.ManufacturingWorkCenterClosure
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		center, err := txq.GetWorkCenterForUpdate(ctx, req.Id)
		if noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		n, err := txq.CountClosures(ctx, req.Id)
		if err != nil {
			return err
		}
		if n >= maxClosures {
			return refuseField(closureTitle, "fromDate", codeClosuresTooMany, "A work center has at most 100 closures; delete an old one first")
		}
		created, err = txq.InsertClosure(ctx, store.InsertClosureParams{
			WorkCenterID: req.Id, FromDate: pgtype.Date{Time: in.FromDate.Time, Valid: true},
			ToDate: pgtype.Date{Time: in.ToDate.Time, Valid: true}, Note: note,
		})
		if err != nil {
			return err
		}
		return notifySchedule(ctx, tx, center.SiteID)
	})
	if err != nil {
		return answer(err, reply)
	}
	return gen.CreateManufacturingClosure201JSONResponse(closureOf(created)), nil
}

// DeleteManufacturingClosure removes one closure of a work center.
func (s *server) DeleteManufacturingClosure(ctx context.Context, req gen.DeleteManufacturingClosureRequestObject) (gen.DeleteManufacturingClosureResponseObject, error) {
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		center, err := txq.GetWorkCenterForUpdate(ctx, req.Id)
		if noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		n, err := txq.DeleteClosure(ctx, store.DeleteClosureParams{ID: req.ClosureId, WorkCenterID: req.Id})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound()
		}
		return notifySchedule(ctx, tx, center.SiteID)
	})
	if err != nil {
		return answer(err, answers[gen.DeleteManufacturingClosureResponseObject]{
			notFound: func() gen.DeleteManufacturingClosureResponseObject {
				return gen.DeleteManufacturingClosure404Response{}
			},
		})
	}
	return gen.DeleteManufacturingClosure204Response{}, nil
}

// notifySchedule tells the site's board that its calendars changed (M36).
func notifySchedule(ctx context.Context, tx pgx.Tx, siteID int64) error {
	n := newNotices()
	n.sites[siteID] = true
	return n.send(ctx, tx)
}
