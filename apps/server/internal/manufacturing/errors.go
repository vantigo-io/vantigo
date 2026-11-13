package manufacturing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
)

// This module's refusals follow the codebase's rule: 404 for what does not
// exist (a bare body), 403 for what is not the caller's to do, 400 naming the
// field or the line, 409 for a rule about the state of things and 503 for a
// module that is not there — every one an RFC 9457 problem in one shape,
// gen.ManufacturingProblem, carrying the rule's code (manufacturing.yaml's
// ManufacturingProblem names them, with their statuses). A client keys off the
// code, never the words.

// The refusal codes (manufacturing design §3, §15).
const (
	codeRevisionConflict      = "revision_conflict"         // 409
	codeWorkCenterCodeTaken   = "work_center_code_taken"    // 409
	codeWorkCenterInUse       = "work_center_in_use"        // 409
	codeWorkCenterHasOpenWork = "work_center_has_open_work" // 409, from C
	codeClosuresTooMany       = "closures_too_many"         // 400
	codeSiteUnknown           = "site_unknown"              // 400
	codeSiteArchived          = "site_archived"             // 409
	codeCostsViewRequired     = "costs_view_required"       // 403
	codeBomCodeTaken          = "bom_code_taken"            // 409
	codeTooManyLines          = "too_many_lines"            // 400
	codeTooManyOperations     = "too_many_operations"       // 400
	codeWorkCenterUnknown     = "work_center_unknown"       // 400
	codeWorkCenterOtherSite   = "work_center_other_site"    // 400
	codeBomCycle              = "bom_cycle"                 // 400
	codeBomTooDeep            = "bom_too_deep"              // 400
	codeBomTooLarge           = "bom_too_large"             // 400
	codeBomNotActive          = "bom_not_active"            // 409
	codeDependencyUnavailable = "dependency_unavailable"    // 503

	// The manufacturing orders' codes (delivery B, §4, §15).
	codeBomRequired         = "bom_required"          // 400
	codeLocationUnknown     = "location_unknown"      // 400
	codeRequiredBelowUsed   = "required_below_used"   // 400
	codeOrderNotDraft       = "order_not_draft"       // 409
	codeOrderNotOpen        = "order_not_open"        // 409
	codeOrderHasProduction  = "order_has_production"  // 409
	codeConsumptionDiffers  = "consumption_differs"   // 409
	codeQuantityShort       = "quantity_short"        // 409
	codeClientRequestReused = "client_request_reused" // 409
	codeShopFloorRequired   = "shop_floor_required"   // 403

	// The shop floor's codes (delivery C, §5, M29a).
	codeWorkCenterBlocked = "work_center_blocked" // 409
	codeWorkOrderPending  = "work_order_pending"  // 409
	codeWorkOrderClosed   = "work_order_closed"   // 409
	codeLogConflict       = "log_conflict"        // 409
	codeLogLocked         = "log_locked"          // 409
	codeOrderClosed       = "order_closed"        // 409
	codeLogOverlaps       = "log_overlaps"        // 400

	// The reports' code (delivery D, §6, M42).
	codePeriodTooLong = "period_too_long" // 400

	// Make-to-order's and the sub-assemblies' codes (delivery E, §7).
	codeSalesOrderLineUnknown  = "sales_order_line_unknown"   // 400
	codeSalesOrderLineHasOrder = "sales_order_line_has_order" // 409
	codeComponentUnknown       = "component_unknown"          // 400
	codeNothingMissing         = "nothing_missing"            // 400

	// Lots (manufacturing phase 2 M80–M82).
	codeLotPatternInvalid = "lot_pattern_invalid" // 400
	codePicksMismatch     = "picks_mismatch"      // 400
	codeSerialQuantity    = "serial_quantity"     // 400
	codeLotNotAllowed     = "lot_not_allowed"     // 400

	// Routing (manufacturing phase 2 M112–M119).
	codeOperationCycle       = "operation_cycle"          // 400
	codeOperationUnknown     = "operation_unknown"        // 400
	codeWorkCenterNotInGroup = "work_center_not_in_group" // 400
	codeWorkOrderStarted     = "work_order_started"       // 409
	codeWorkOrderMemberFixed = "work_order_member_fixed"  // 409

	// A line's codes (ManufacturingProblemLine.code), each also a problem's
	// own code when it is the first failing line's or a field's.
	codeVariantNotGoods     = "variant_not_goods"
	codeVariantNotFound     = "variant_not_found"
	codeOperationKeyUnknown = "operation_key_unknown"
	codeQuantityInvalid     = "quantity_invalid"
)

// refusalCodes is every code above, for the test that holds the contract's
// ManufacturingProblem description to naming each one.
var refusalCodes = []string{
	codeRevisionConflict, codeWorkCenterCodeTaken, codeWorkCenterInUse, codeWorkCenterHasOpenWork,
	codeClosuresTooMany, codeSiteUnknown, codeSiteArchived, codeCostsViewRequired, codeBomCodeTaken,
	codeTooManyLines, codeTooManyOperations, codeWorkCenterUnknown, codeWorkCenterOtherSite, codeBomCycle,
	codeBomTooDeep, codeBomTooLarge, codeBomNotActive, codeDependencyUnavailable,
	codeVariantNotGoods, codeVariantNotFound, codeOperationKeyUnknown, codeQuantityInvalid,
	codeBomRequired, codeLocationUnknown, codeRequiredBelowUsed, codeOrderNotDraft, codeOrderNotOpen,
	codeOrderHasProduction, codeConsumptionDiffers, codeQuantityShort, codeClientRequestReused, codeShopFloorRequired,
	codeWorkCenterBlocked, codeWorkOrderPending, codeWorkOrderClosed, codeLogConflict, codeLogLocked, codeOrderClosed, codeLogOverlaps,
	codeNoCapacity, codePeriodTooLong,
	codeSalesOrderLineUnknown, codeSalesOrderLineHasOrder, codeComponentUnknown, codeNothingMissing,
	codeLotPatternInvalid, codePicksMismatch, codeSerialQuantity, codeLotNotAllowed,
	codeCheckLimitsInvalid, codePhotoRequired, codeChecksIncomplete, codeCheckNotOpen, codeDeviationClosed,
	codePhotoTooLarge, codePhotoType, codeStorageNotConfig,
	codeOperationCycle, codeOperationUnknown, codeWorkCenterNotInGroup, codeWorkOrderStarted, codeWorkOrderMemberFixed,
}

// errDependencyUnavailable is every accessor's answer when the contract it
// reads is not composed: config makes that impossible for the catalog and the
// stock in a running installation, and a handler answers it as the 503
// dependency_unavailable.
var errDependencyUnavailable = errors.New("manufacturing: a module this one reads is not composed")

// fieldErrors is a validation pass's outcome: the field errors and, when one
// of them is a rule the design names, its code. The first coded rule to fail
// gives the code; the field errors carry every failure.
type fieldErrors struct {
	errs map[string][]string
	code string
}

// add records message on field; "" records nothing.
func (f *fieldErrors) add(field, message string) {
	if message == "" {
		return
	}
	if f.errs == nil {
		f.errs = map[string][]string{}
	}
	f.errs[field] = append(f.errs[field], message)
}

// addCoded records message on field as the coded rule code.
func (f *fieldErrors) addCoded(field, code, message string) {
	if message == "" {
		return
	}
	f.add(field, message)
	if f.code == "" {
		f.code = code
	}
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// problem is a problem body with a status, a code, a title and a detail; an
// empty code or detail is left out.
func problem(status int, code, title, detail string) gen.ManufacturingProblem {
	s := int32(status)
	p := gen.ManufacturingProblem{Title: &title, Status: &s}
	if code != "" {
		p.Code = &code
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.ManufacturingProblem {
	p := problem(http.StatusBadRequest, f.code, title, "")
	p.Errors = &f.errs
	return p
}

// lineFailure is one failing line of a BoM: every one is named at once.
type lineFailure struct {
	index  int
	code   string
	detail string
}

// linesProblem is a BoM's refusal naming every failing line, a 400 whose own
// code is the first line's.
func linesProblem(title string, failures []lineFailure) gen.ManufacturingProblem {
	lines := make([]gen.ManufacturingProblemLine, 0, len(failures))
	for _, l := range failures {
		lines = append(lines, gen.ManufacturingProblemLine{Index: int32(l.index), Code: l.code, Detail: l.detail})
	}
	code := ""
	if len(failures) > 0 {
		code = failures[0].code
	}
	p := problem(http.StatusBadRequest, code, title, fmt.Sprintf("%d line(s) failed; nothing was saved.", len(failures)))
	p.Lines = &lines
	return p
}

// cycleProblem is the 400 bom_cycle, naming the cycle's path from the BoM's
// own variant (M14).
func cycleProblem(path []gen.ManufacturingProblemPathStep) gen.ManufacturingProblem {
	p := problem(http.StatusBadRequest, codeBomCycle, "The bill of materials makes a cycle",
		"A component, at some depth, is made by a BoM that needs this BoM's own variant. Nothing was saved.")
	p.Path = &path
	return p
}

// refusal is a write's answer that is not a success, returned from inside its
// transaction so everything the write did rolls back, and turned into the
// operation's own typed response by the handler (answer).
type refusal struct {
	status  int
	problem gen.ManufacturingProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("manufacturing: refused with %d", r.status) }

// notFound is the bare 404 of an unknown id.
func notFound() error { return &refusal{status: http.StatusNotFound} }

// refuse is a refusal with p's status.
func refuse(p gen.ManufacturingProblem) error {
	status := http.StatusBadRequest
	if p.Status != nil {
		status = int(*p.Status)
	}
	return &refusal{status: status, problem: p}
}

// refuseInvalid is a 400 carrying the field errors under title.
func refuseInvalid(title string, f fieldErrors) error {
	return &refusal{status: http.StatusBadRequest, problem: invalid(title, f)}
}

// refuseField is a 400 naming one field, under code when it has one.
func refuseField(title, field, code, message string) error {
	var f fieldErrors
	if code == "" {
		f.add(field, message)
	} else {
		f.addCoded(field, code, message)
	}
	return refuseInvalid(title, f)
}

// refuseLines is a BoM's refusal naming every failing line.
func refuseLines(title string, failures []lineFailure) error {
	return refuse(linesProblem(title, failures))
}

// conflict is a 409 with code.
func conflict(code, title, detail string) error {
	return refuse(problem(http.StatusConflict, code, title, detail))
}

// revisionConflict is the 409 of a stale revision, or of a race on a
// variant's default BoM (M14a).
func revisionConflict(what string, current, supplied int32) error {
	return conflict(codeRevisionConflict, "Revision conflict",
		fmt.Sprintf("The %s is at revision %d; the request was made from %d. Reload it and try again.", what, current, supplied))
}

// costsViewRequired is the 403 of a cost rate written by a caller without
// manufacturing:costs-view (M38).
func costsViewRequired() error {
	return refuse(problem(http.StatusForbidden, codeCostsViewRequired, "Costs are not yours to write",
		"Writing a work center's machine or labour cost needs manufacturing:costs-view. Leave both out to keep what is stored."))
}

// dependencyUnavailable is the 503 an operation answers when a module it
// reads is not composed.
func dependencyUnavailable() gen.ManufacturingProblem {
	return problem(http.StatusServiceUnavailable, codeDependencyUnavailable, "A module Manufacturing reads is unavailable",
		"Manufacturing reads the product catalog and Inventory's sites and stock, and one of them is not running in this installation. Nothing was changed.")
}

// answerOf is how a handler reads an error: a refusal's status and body,
// errDependencyUnavailable as the 503, and ok false for anything else, which
// the handler returns as the 500 it is.
func answerOf(err error) (int, gen.ManufacturingProblem, bool) {
	var r *refusal
	if errors.As(err, &r) {
		return r.status, r.problem, true
	}
	if errors.Is(err, errDependencyUnavailable) {
		return http.StatusServiceUnavailable, dependencyUnavailable(), true
	}
	return 0, gen.ManufacturingProblem{}, false
}

// answers are one operation's typed responses for a refusal, by status; a
// status an operation does not declare is nil, and a refusal with it is a
// 500 — a bug in the handler, never a silent mismatch with the contract.
type answers[R any] struct {
	badRequest  func(gen.ManufacturingProblem) R
	forbidden   func(gen.ManufacturingProblem) R
	notFound    func() R
	conflict    func(gen.ManufacturingProblem) R
	unavailable func(gen.ManufacturingProblem) R
}

// answer turns err into the operation's typed response, or returns it as the
// error it is.
func answer[R any](err error, a answers[R]) (R, error) {
	var zero R
	status, p, ok := answerOf(err)
	if !ok {
		return zero, err
	}
	switch {
	case status == http.StatusBadRequest && a.badRequest != nil:
		return a.badRequest(p), nil
	case status == http.StatusForbidden && a.forbidden != nil:
		return a.forbidden(p), nil
	case status == http.StatusNotFound && a.notFound != nil:
		return a.notFound(), nil
	case status == http.StatusConflict && a.conflict != nil:
		return a.conflict(p), nil
	case status == http.StatusServiceUnavailable && a.unavailable != nil:
		return a.unavailable(p), nil
	}
	return zero, fmt.Errorf("manufacturing: a refusal with status %d this operation does not declare: %w", status, err)
}

// noRows reports whether err is a read that found no row.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
