package manufacturing

// Quality checks (manufacturing phase 2 M95–M98): the check templates of a
// BoM's operations, edited with the BoM in its one request; their snapshot
// onto the work orders at confirm; the results recorded at the station, a
// measure judged by the server; and what they gate — a production while a
// check of a work order before the output work order, or a first-piece check
// of the output work order itself, has no result, and a work order's finish
// while one of its own has none. A failed result opens a deviation in its
// own transaction (deviations.go).

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// The bounds of M95 and M97.
const (
	maxChecksPerOperation = 20
	maxCheckTitle         = 200
	maxCheckInstructions  = 5000
	maxCheckUnit          = 20
	maxResultNote         = 1000
	measurePlaces         = 4
	checksTitle           = "The result was not recorded"
)

// The codes of quality (M95–M99, M103).
const (
	codeCheckLimitsInvalid = "check_limits_invalid" // 400
	codePhotoRequired      = "photo_required"       // 400
	codeChecksIncomplete   = "checks_incomplete"    // 409
	codeCheckNotOpen       = "check_not_open"       // 409
	codeDeviationClosed    = "deviation_closed"     // 409
	codeLotNotOfOrder      = "lot_not_of_order"     // 400
	codeLotsViewRequired   = "lots_view_required"   // 403
)

// The kinds, frequencies and results of a check (M95, M97).
const (
	checkInstruction = "instruction"
	checkPassFail    = "pass_fail"
	checkMeasure     = "measure"
	checkPhoto       = "photo"

	frequencyEach       = "each_work_order"
	frequencyFirstPiece = "first_piece"

	resultDone = "done"
	resultPass = "pass"
	resultFail = "fail"
)

// maxMeasure is the largest magnitude a numeric(14,4) keeps.
var maxMeasure = mustRat("9999999999.9999")

// measureText matches a measure: an optional sign, digits and an optional
// fraction.
var measureText = regexp.MustCompile(`^[-+]?[0-9]+(\.[0-9]+)?$`)

// parseMeasure is s as an exact decimal of at most four decimals, of either
// sign, or why it is not one (M95, §16: kept to four decimals and judged
// unrounded).
func parseMeasure(s string) (*big.Rat, string) {
	s = strings.TrimSpace(s)
	if !measureText.MatchString(s) {
		return nil, "Not a decimal number"
	}
	if _, fraction, ok := strings.Cut(s, "."); ok && len(fraction) > measurePlaces {
		return nil, fmt.Sprintf("At most %d decimals", measurePlaces)
	}
	r := mustRat(strings.TrimPrefix(s, "+"))
	if new(big.Rat).Abs(r).Cmp(maxMeasure) > 0 {
		return nil, "Must be within ±" + maxMeasure.FloatString(measurePlaces)
	}
	return r, ""
}

// measureNumeric is a measure as its column keeps it; nil is NULL.
func measureNumeric(v *big.Rat) pgtype.Numeric { return numeric(v, measurePlaces) }

// measureOut is a measure column as the contract answers it, nil for NULL.
func measureOut(n pgtype.Numeric) *string {
	r := ratOf(n)
	if r == nil {
		return nil
	}
	t := text(r, measurePlaces)
	return &t
}

// checkValues is one check template as judged.
type checkValues struct {
	kind, title, instructions, frequency string
	nominal, min, max                    *big.Rat
	unit                                 *string
	quarantine                           bool
}

// judgeChecks is an operation's checks (M95): at most 20, a title, a known
// kind and frequency, and a measure's limits — min or max, and min ≤ nominal
// ≤ max where given (check_limits_invalid). at is the operation's path.
func judgeChecks(f *fieldErrors, at string, in []gen.ManufacturingCheckTemplateInput) []checkValues {
	if len(in) > maxChecksPerOperation {
		f.add(at+"checks", fmt.Sprintf("At most %d checks an operation", maxChecksPerOperation))
		return nil
	}
	out := make([]checkValues, 0, len(in))
	for i, c := range in {
		path := fmt.Sprintf("%schecks[%d].", at, i)
		v := checkValues{kind: string(c.Kind), frequency: string(c.Frequency), title: strings.TrimSpace(c.Title)}
		if !slices.Contains([]string{checkInstruction, checkPassFail, checkMeasure, checkPhoto}, v.kind) {
			f.add(path+"kind", "The kind is instruction, pass_fail, measure or photo")
		}
		if !slices.Contains([]string{frequencyEach, frequencyFirstPiece}, v.frequency) {
			f.add(path+"frequency", "The frequency is each_work_order or first_piece")
		}
		if n := utf8.RuneCountInString(v.title); n < 1 || n > maxCheckTitle {
			f.add(path+"title", "1–200 characters")
		}
		if c.Instructions != nil {
			v.instructions = *c.Instructions
			if utf8.RuneCountInString(v.instructions) > maxCheckInstructions {
				f.add(path+"instructions", "At most 5000 characters")
			}
		}
		if c.Unit != nil {
			if unit := strings.TrimSpace(*c.Unit); unit != "" {
				if utf8.RuneCountInString(unit) > maxCheckUnit {
					f.add(path+"unit", "At most 20 characters")
				}
				v.unit = &unit
			}
		}
		if c.QuarantineOnFail != nil {
			v.quarantine = *c.QuarantineOnFail
		}
		limit := func(name string, s *string) *big.Rat {
			if s == nil || strings.TrimSpace(*s) == "" {
				return nil
			}
			r, msg := parseMeasure(*s)
			f.add(path+name, msg)
			return r
		}
		v.nominal, v.min, v.max = limit("nominal", c.Nominal), limit("min", c.Min), limit("max", c.Max)
		if v.kind == checkMeasure {
			switch {
			case v.min == nil && v.max == nil:
				f.addCoded(path+"min", codeCheckLimitsInvalid, "A measure has a min, a max or both")
			case v.min != nil && v.max != nil && v.min.Cmp(v.max) > 0:
				f.addCoded(path+"max", codeCheckLimitsInvalid, "The max is not below the min")
			case v.nominal != nil && v.min != nil && v.nominal.Cmp(v.min) < 0,
				v.nominal != nil && v.max != nil && v.nominal.Cmp(v.max) > 0:
				f.addCoded(path+"nominal", codeCheckLimitsInvalid, "The nominal lies between the min and the max")
			}
		} else {
			v.nominal, v.min, v.max, v.unit = nil, nil, nil, nil
		}
		out = append(out, v)
	}
	return out
}

// writeChecks writes an operation's checks, in the request's order.
func writeChecks(ctx context.Context, txq *store.Queries, operationID int64, checks []checkValues) error {
	for i, c := range checks {
		if err := txq.InsertBomOperationCheck(ctx, store.InsertBomOperationCheckParams{
			OperationID: operationID, Sequence: int32(i + 1), Kind: c.kind, Title: c.title, Instructions: c.instructions,
			Nominal: measureNumeric(c.nominal), MinValue: measureNumeric(c.min), MaxValue: measureNumeric(c.max), Unit: c.unit,
			Frequency: c.frequency, QuarantineOnFail: c.quarantine,
		}); err != nil {
			return err
		}
	}
	return nil
}

// checkTemplatesOf is the operation's checks as the contract answers them.
func checkTemplatesOf(rows []store.ManufacturingBomOperationCheck, operationID int64) []gen.ManufacturingCheckTemplate {
	out := []gen.ManufacturingCheckTemplate{}
	for _, c := range rows {
		if c.OperationID != operationID {
			continue
		}
		out = append(out, gen.ManufacturingCheckTemplate{
			Id: c.ID, Sequence: c.Sequence, Kind: gen.ManufacturingCheckKind(c.Kind), Title: c.Title, Instructions: c.Instructions,
			Nominal: measureOut(c.Nominal), Min: measureOut(c.MinValue), Max: measureOut(c.MaxValue), Unit: c.Unit,
			Frequency: gen.ManufacturingCheckFrequency(c.Frequency), QuarantineOnFail: c.QuarantineOnFail,
		})
	}
	return out
}

// workOrderChecksOf is the work orders' checks with their results in force,
// by work order.
func workOrderChecksOf(rows []store.ChecksOfWorkOrdersRow) map[int64][]gen.ManufacturingWorkOrderCheck {
	out := map[int64][]gen.ManufacturingWorkOrderCheck{}
	for _, r := range rows {
		c := r.ManufacturingWorkOrderCheck
		check := gen.ManufacturingWorkOrderCheck{
			Id: c.ID, WorkOrderId: c.WorkOrderID, Sequence: c.Sequence, Kind: gen.ManufacturingCheckKind(c.Kind), Title: c.Title,
			Instructions: c.Instructions, Nominal: measureOut(c.Nominal), Min: measureOut(c.MinValue), Max: measureOut(c.MaxValue),
			Unit: c.Unit, Frequency: gen.ManufacturingCheckFrequency(c.Frequency), QuarantineOnFail: c.QuarantineOnFail,
		}
		if r.ResultID != nil && r.Result != nil && r.RecordedAt != nil {
			result := &gen.ManufacturingCheckResult{Id: *r.ResultID, Result: gen.ManufacturingCheckResultValue(*r.Result),
				Value: measureOut(r.Value), Note: r.Note, PhotoId: r.PhotoID, RecordedAt: *r.RecordedAt,
				RecordedBy: gen.ManufacturingDeviationActor{UserId: r.UserID, DisplayName: deref(r.UserDisplay)}, DeviationId: r.DeviationID}
			if r.DeviationNumber != nil {
				n := deviationNumber(*r.DeviationNumber)
				result.DeviationNumber = &n
			}
			check.Result = result
		}
		out[c.WorkOrderID] = append(out[c.WorkOrderID], check)
	}
	return out
}

// checksIncomplete is the 409 checks_incomplete naming every missing check
// (M98).
func checksIncomplete(title, detail string, missing []store.MissingChecksOfWorkOrdersRow) error {
	p := problem(http.StatusConflict, codeChecksIncomplete, title, detail)
	checks := make([]gen.ManufacturingMissingCheck, 0, len(missing))
	for _, m := range missing {
		checks = append(checks, gen.ManufacturingMissingCheck{CheckId: m.ID, Title: m.Title, Frequency: gen.ManufacturingCheckFrequency(m.Frequency),
			WorkOrderId: m.WorkOrderID, WorkOrderName: m.WorkOrderName, WorkOrderSequence: m.WorkOrderSequence})
	}
	p.Checks = &checks
	return refuse(p)
}

// productionGate is M98's gate on a production with good output, read under
// the order's lock (which every result takes too): every check of every work
// order before the output work order — the last that is not cancelled — and
// the output work order's first-piece checks must have a result. Precedes is
// the work order graph's (manufacturing phase 2 M114): the work orders from
// which the output work order is reachable, cancelled ones left out.
func productionGate(ctx context.Context, txq *store.Queries, orderID int64) error {
	wos, err := txq.WorkOrdersOfOrder(ctx, orderID)
	if err != nil {
		return err
	}
	var output *store.ManufacturingWorkOrder
	for i := range wos {
		if wos[i].Status != workOrderCancelled {
			output = &wos[i]
		}
	}
	if output == nil {
		return nil
	}
	graph, err := readGraph(ctx, txq, []int64{orderID})
	if err != nil {
		return err
	}
	precedes := graph.ancestors(output.ID)
	before := []int64{}
	for _, w := range wos {
		if w.Status != workOrderCancelled && precedes[w.ID] {
			before = append(before, w.ID)
		}
	}
	missing, err := txq.MissingChecksOfWorkOrders(ctx, store.MissingChecksOfWorkOrdersParams{WorkOrderIds: before,
		Frequencies: []string{frequencyEach, frequencyFirstPiece}})
	if err != nil {
		return err
	}
	first, err := txq.MissingChecksOfWorkOrders(ctx, store.MissingChecksOfWorkOrdersParams{WorkOrderIds: []int64{output.ID},
		Frequencies: []string{frequencyFirstPiece}})
	if err != nil {
		return err
	}
	missing = append(missing, first...)
	if len(missing) == 0 {
		return nil
	}
	return checksIncomplete("Checks are missing", fmt.Sprintf("%d check(s) of the order's work orders have no result; record them before the production.", len(missing)), missing)
}

// finishGate is M98's gate on a work order's finish: every one of its own
// checks, either frequency, has a result.
func finishGate(ctx context.Context, txq *store.Queries, workOrderID int64) error {
	missing, err := txq.MissingChecksOfWorkOrders(ctx, store.MissingChecksOfWorkOrdersParams{WorkOrderIds: []int64{workOrderID},
		Frequencies: []string{frequencyEach, frequencyFirstPiece}})
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	return checksIncomplete("Checks are missing", fmt.Sprintf("%d check(s) of the work order have no result; record them before it finishes.", len(missing)), missing)
}

// closeGate is M98's gate on an order's close: once the order has good
// output, every check of each of its work orders that is not cancelled has a
// result — a check left without one is refused, not closed over; a fail is
// a result. A cancelled work order's checks are not asked for.
func closeGate(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder) error {
	if ratOrZero(o.Produced).Sign() <= 0 {
		return nil
	}
	wos, err := txq.WorkOrdersOfOrder(ctx, o.ID)
	if err != nil {
		return err
	}
	var open []int64
	for _, w := range wos {
		if w.Status != workOrderCancelled {
			open = append(open, w.ID)
		}
	}
	missing, err := txq.MissingChecksOfWorkOrders(ctx, store.MissingChecksOfWorkOrdersParams{WorkOrderIds: open,
		Frequencies: []string{frequencyEach, frequencyFirstPiece}})
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	return checksIncomplete("Checks are missing", fmt.Sprintf("%d check(s) of the order's work orders have no result; record them before the order closes.", len(missing)), missing)
}

// checkResultAnswers are a result's typed refusals.
func checkResultAnswers() answers[gen.RecordManufacturingCheckResultResponseObject] {
	return answers[gen.RecordManufacturingCheckResultResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.RecordManufacturingCheckResultResponseObject {
			return gen.RecordManufacturingCheckResult400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.RecordManufacturingCheckResultResponseObject {
			return gen.RecordManufacturingCheckResult403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.RecordManufacturingCheckResultResponseObject {
			return gen.RecordManufacturingCheckResult404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.RecordManufacturingCheckResultResponseObject {
			return gen.RecordManufacturingCheckResult409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.RecordManufacturingCheckResultResponseObject {
			return gen.RecordManufacturingCheckResult503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// checkNotOpen is the 409 check_not_open (M103).
func checkNotOpen(status string) error {
	return conflict(codeCheckNotOpen, "The work order is closed",
		fmt.Sprintf("The work order is %s; no result is recorded on it any more.", status))
}

// RecordManufacturingCheckResult records a check's result (M97) under the
// lock order: LockVariants on the order's variants, the order FOR UPDATE,
// the work order, the check FOR UPDATE — so two results recorded at once
// for one check serialise and the second supersedes the first — then the
// result, the deviation a fail opens (M99) and last the ledger's holds.
func (s *server) RecordManufacturingCheckResult(ctx context.Context, req gen.RecordManufacturingCheckResultRequestObject) (gen.RecordManufacturingCheckResultResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Recording a check"); err != nil {
		return answer(err, checkResultAnswers())
	}
	f := s.unknownProperties(ctx, checkResultInputSchema)
	in := *req.Body
	var note *string
	if in.Note != nil {
		if n := strings.TrimSpace(*in.Note); n != "" {
			if utf8.RuneCountInString(n) > maxResultNote {
				f.add("note", "At most 1000 characters")
			}
			note = &n
		}
	}
	var value *big.Rat
	if in.Value != nil {
		v, msg := parseMeasure(*in.Value)
		f.add("value", msg)
		value = v
	}
	if f.failed() {
		return answer(refuseInvalid(checksTitle, f), checkResultAnswers())
	}
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, checkResultAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withOrder(ctx, pre.OrderID, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		wos, err := lockedWorkOrders(ctx, txq, []int64{req.Id})
		if err != nil {
			return err
		}
		w, ok := wos[req.Id]
		if !ok || w.OrderID != o.ID {
			return errRelock
		}
		if w.Status == workOrderDone || w.Status == workOrderCancelled {
			return checkNotOpen(w.Status)
		}
		c, err := txq.LockWorkOrderCheck(ctx, store.LockWorkOrderCheckParams{ID: req.CheckId, WorkOrderID: w.ID})
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		result, err := judgeResult(c, in.Result, value, in.PhotoId)
		if err != nil {
			return err
		}
		// A value is a measure's reading: sent with any other kind it is
		// not kept.
		reading := value
		if c.Kind != checkMeasure {
			reading = nil
		}
		if in.PhotoId != nil {
			photo, err := txq.CheckPhotoForShare(ctx, *in.PhotoId)
			if noRows(err) || (err == nil && photo.WorkOrderID != w.ID) {
				return refuseField(checksTitle, "photoId", "", "No photo of this work order has this id")
			}
			if err != nil {
				return err
			}
		}
		now := s.deps.Clock()
		previous, err := txq.CurrentCheckResult(ctx, c.ID)
		hasPrevious := err == nil
		if err != nil && !noRows(err) {
			return err
		}
		// One result is in force per check: the one in force is marked
		// superseded (by itself) before the new one is inserted, then
		// pointed at its successor.
		if hasPrevious {
			if err := txq.SupersedeCheckResult(ctx, store.SupersedeCheckResultParams{ID: previous.ID, SupersededBy: &previous.ID}); err != nil {
				return err
			}
		}
		recorded, err := txq.InsertCheckResult(ctx, store.InsertCheckResultParams{WorkOrderCheckID: c.ID, Result: result,
			Value: measureNumeric(reading), Note: note, PhotoID: in.PhotoId, UserID: who.id, UserDisplay: who.display, Now: now})
		if err != nil {
			return err
		}
		if hasPrevious {
			if err := txq.SupersedeCheckResult(ctx, store.SupersedeCheckResultParams{ID: previous.ID, SupersededBy: &recorded.ID}); err != nil {
				return err
			}
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindCheckRecorded, who, now, map[string]any{
			"workOrderId": w.ID, "checkId": c.ID, "resultId": recorded.ID, "result": result,
		}); err != nil {
			return err
		}
		n := newNotices()
		n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		if result == resultFail {
			description := c.Title
			if reading != nil {
				description = fmt.Sprintf("%s: %s%s, limits %s–%s.", c.Title, text(reading, measurePlaces), unitText(c.Unit),
					limitText(c.MinValue), limitText(c.MaxValue))
			}
			if note != nil {
				description += "\n\n" + *note
			}
			// A fail recorded again while the check's deviation is open is
			// added to it — its reading, and the deviation pointing at the
			// latest result — rather than opening another (M99).
			open, err := txq.OpenDeviationOfCheck(ctx, c.ID)
			switch {
			case err == nil:
				if _, err := txq.AddResultToDeviation(ctx, store.AddResultToDeviationParams{ID: open.ID, CheckResultID: &recorded.ID,
					Addition: fmt.Sprintf("\n\nFailed again, %s, by %s: %s", now.UTC().Format(time.RFC3339), who.display, description)}); err != nil {
					return err
				}
				if err := notifyOrder(ctx, tx, o.ID); err != nil {
					return err
				}
				if err := notifyDeviation(ctx, tx, open.ID); err != nil {
					return err
				}
			case noRows(err):
				if _, err := s.openDeviation(ctx, tx, txq, deviationDraft{
					title: truncate("Check failed: "+c.Title, maxCheckTitle), description: description, orderID: &o.ID, workOrderID: &w.ID,
					checkResultID: &recorded.ID, quarantine: c.QuarantineOnFail,
				}, who, now); err != nil {
					return err
				}
			default:
				return err
			}
		}
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, checkResultAnswers())
	}
	view, err := s.workOrderView(ctx, req.Id)
	if err != nil {
		return answer(err, checkResultAnswers())
	}
	return gen.RecordManufacturingCheckResult200JSONResponse(view), nil
}

// judgeResult is a result as the check's kind takes it (M97): an instruction
// is done; a pass/fail check is pass or fail; a measure is judged by the
// server against its limits, unrounded; a photo needs its photo.
func judgeResult(c store.ManufacturingWorkOrderCheck, sent *gen.ManufacturingCheckResultValue, value *big.Rat, photoID *int64) (string, error) {
	switch c.Kind {
	case checkInstruction:
		if sent != nil && *sent != gen.ManufacturingCheckResultValueDone {
			return "", refuseField(checksTitle, "result", "", "An instruction is acknowledged: done")
		}
		return resultDone, nil
	case checkPassFail:
		if sent == nil || (*sent != gen.ManufacturingCheckResultValuePass && *sent != gen.ManufacturingCheckResultValueFail) {
			return "", refuseField(checksTitle, "result", "", "Pass or fail")
		}
		return string(*sent), nil
	case checkMeasure:
		if value == nil {
			return "", refuseField(checksTitle, "value", "", "A measure is recorded with its value")
		}
		if lo := ratOf(c.MinValue); lo != nil && value.Cmp(lo) < 0 {
			return resultFail, nil
		}
		if hi := ratOf(c.MaxValue); hi != nil && value.Cmp(hi) > 0 {
			return resultFail, nil
		}
		return resultPass, nil
	case checkPhoto:
		if photoID == nil {
			return "", refuse(withField(problem(http.StatusBadRequest, codePhotoRequired, checksTitle, "A photo check is recorded with its photo."), "photoId", "Take the photo first"))
		}
		return resultDone, nil
	}
	return "", fmt.Errorf("manufacturing: check %d has the kind %q", c.ID, c.Kind)
}

// withField is p naming one field.
func withField(p gen.ManufacturingProblem, field, message string) gen.ManufacturingProblem {
	errs := map[string][]string{field: {message}}
	p.Errors = &errs
	return p
}

// unitText is " unit", "" for none.
func unitText(unit *string) string {
	if unit == nil || *unit == "" {
		return ""
	}
	return " " + *unit
}

// limitText is a limit at display, "…" for none.
func limitText(n pgtype.Numeric) string {
	if r := ratOf(n); r != nil {
		return text(r, measurePlaces)
	}
	return "…"
}

// truncate is s cut to n characters.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
