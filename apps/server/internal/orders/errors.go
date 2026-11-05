package orders

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// This module's refusals follow the codebase's rule: 404 for what does not
// exist (a bare body), 403 for what is not the caller's to do, 400 naming the
// field or the line, 409 for a rule about the state of things and 503 for a
// module or a channel that is not there — every one an RFC 9457 problem in one
// shape, gen.OrdersProblem, carrying the rule's code (orders.yaml's
// OrdersProblem names them). A client keys off the code, never the words.

// The refusal codes.
const (
	codeTooManyLines            = "too_many_lines"
	codeOptionalNotAllowed      = "optional_not_allowed"
	codeLineUnpriced            = "line_unpriced"
	codeVATCodeUnmapped         = "vat_code_unmapped"
	codeOrderEmpty              = "order_empty"
	codeCustomerDisabled        = "customer_disabled"
	codeProjectCustomerMismatch = "project_customer_mismatch"
	codeVersionConflict         = "version_conflict"
	codeQuoteNotDraft           = "quote_not_draft"
	codeQuoteNotSent            = "quote_not_sent"
	codeQuoteAlreadyAccepted    = "quote_already_accepted"
	codeClientRequestReused     = "client_request_reused"
	codeDependencyUnavailable   = "dependency_unavailable"
	codeSendingDisabled         = "sending_disabled"
	codeMailFailed              = "mail_failed"
	codeSeriesStartTooLow       = "series_start_too_low"
	codeSendChannelUnknown      = "send_channel_unknown"
	codeTemplateNameTaken       = "template_name_taken"
	codeOrderNotDraft           = "order_not_draft"
	codeOrderNotConfirmed       = "order_not_confirmed"
	codeOrderLocked             = "order_locked"
	codeOrderHasProgress        = "order_has_progress"
	codeLineInvoiced            = "line_invoiced"
	codeLineHasProgress         = "line_has_progress"
	codeLineKindChanged         = "line_kind_changed"
	codeOrderOnHold             = "order_on_hold"
	codeLineInDraftInvoice      = "line_in_draft_invoice"
	codeOrderInDraftInvoice     = "order_in_draft_invoice"
	codeBlockedByWarning        = "blocked_by_warning"

	// A line's codes (OrdersProblemLine.code).
	codeVariantNotFound   = "variant_not_found"
	codePackagingNotFound = "packaging_not_found"
	codeQuantityInvalid   = "quantity_invalid"
	codePriceInvalid      = "price_invalid"
	codeDiscountInvalid   = "discount_invalid"
	codeVATCodeUnknown    = "vat_code_unknown"
	codeLineInvalid       = "line_invalid"

	// An order line's progress codes (O26, O32).
	codeQuantityBelowProgress  = "quantity_below_progress"
	codeOverDelivery           = "over_delivery"
	codeUndoExceedsDelivered   = "undo_exceeds_delivered"
	codeDeliveredBelowInvoiced = "delivered_below_invoiced"
	codeLineNotFound           = "line_not_found"
)

// The titles this module's validation problems carry.
const (
	invalidQuoteTitle    = "Invalid quote"
	invalidOrderTitle    = "Invalid order"
	invalidTemplateTitle = "Invalid template"
	invalidSettingsTitle = "Invalid settings"
	invalidSendTitle     = "Invalid send"
	invalidQueryTitle    = "Invalid query"
	invalidAcceptTitle   = "Invalid accept"
	invalidConfirmTitle  = "The order cannot be confirmed"
	invalidDeliveryTitle = "Invalid delivery"
	invalidHoldTitle     = "Invalid hold"
	invalidWarningTitle  = "Invalid warning"
	invalidAnalysisTitle = "Invalid analysis"
)

// errDependencyUnavailable is every accessor's answer when the contract it
// reads is not composed: config makes that impossible for the catalog, the
// directory and the sales tax in a running installation, and a handler
// answers it as the 503 dependency_unavailable.
var errDependencyUnavailable = errors.New("orders: a module this one reads is not composed")

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
func problem(status int, code, title, detail string) gen.OrdersProblem {
	s := int32(status)
	p := gen.OrdersProblem{Title: &title, Status: &s}
	if code != "" {
		p.Code = &code
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.OrdersProblem {
	p := problem(http.StatusBadRequest, f.code, title, "")
	p.Errors = &f.errs
	return p
}

// lineFailure is one failing line of a document: every one is named at once.
type lineFailure struct {
	index  int
	code   string
	detail string
}

// linesProblem is a document's refusal naming every failing line, a 400 whose
// own code is the first line's.
func linesProblem(title string, failures []lineFailure) gen.OrdersProblem {
	lines := make([]gen.OrdersProblemLine, 0, len(failures))
	for _, l := range failures {
		lines = append(lines, gen.OrdersProblemLine{Index: int32(l.index), Code: l.code, Detail: l.detail})
	}
	code := ""
	if len(failures) > 0 {
		code = failures[0].code
	}
	p := problem(http.StatusBadRequest, code, title, fmt.Sprintf("%d line(s) failed; nothing was saved.", len(failures)))
	p.Lines = &lines
	return p
}

// refusal is a write's answer that is not a success, returned from inside its
// transaction so everything the write did rolls back, and turned into the
// operation's own typed response by the handler (answer).
type refusal struct {
	status  int
	problem gen.OrdersProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("orders: refused with %d", r.status) }

// notFound is the bare 404 of an unknown id.
func notFound() error { return &refusal{status: http.StatusNotFound} }

// refuse is a refusal with p's status.
func refuse(p gen.OrdersProblem) error {
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

// refuseLines is a document's refusal naming every failing line.
func refuseLines(title string, failures []lineFailure) error {
	return refuse(linesProblem(title, failures))
}

// conflict is a 409 with code.
func conflict(code, title, detail string) error {
	return refuse(problem(http.StatusConflict, code, title, detail))
}

// versionConflict is the 409 of a stale revision or version.
func versionConflict(what string, current, supplied int32) error {
	return conflict(codeVersionConflict, "Version conflict",
		fmt.Sprintf("The %s is at %d; the request was made from %d. Reload it and try again.", what, current, supplied))
}

// judgeFresh refuses a write made from a read of a document older than its
// last write: expected is the updatedAt the client read, the concurrency
// token every write of a quote or an order sends back (version_conflict).
// Every write moves updated_at, so a hold, a lock or a delivery marking — which
// leave the version — still make an older read stale.
func judgeFresh(what string, updatedAt, expected time.Time) error {
	if updatedAt.Equal(expected) {
		return nil
	}
	return conflict(codeVersionConflict, "Version conflict",
		fmt.Sprintf("The %s was changed at %s; the request was made from what it was at %s. Reload it and try again.",
			what, updatedAt.UTC().Format(time.RFC3339Nano), expected.UTC().Format(time.RFC3339Nano)))
}

// dependencyUnavailable is the 503 an operation answers when a module it
// reads is not composed.
func dependencyUnavailable() gen.OrdersProblem {
	return problem(http.StatusServiceUnavailable, codeDependencyUnavailable, "A module Orders reads is unavailable",
		"Orders reads the customers, the product catalog and the VAT codes, and one of them is not running in this installation. Nothing was changed.")
}

// sendingDisabled is the 503 of a send when Communications is off or no
// channel to send from is chosen or able to send (O25).
func sendingDisabled() error {
	return refuse(problem(http.StatusServiceUnavailable, codeSendingDisabled, "Sending is disabled",
		"Communications is off, or Orders settings choose no channel that can send. Download the PDF and mark the document as sent instead."))
}

// answerOf is how a handler reads an error: a refusal's status and body,
// errDependencyUnavailable as the 503, and ok false for anything else, which
// the handler returns as the 500 it is.
func answerOf(err error) (int, gen.OrdersProblem, bool) {
	var r *refusal
	if errors.As(err, &r) {
		return r.status, r.problem, true
	}
	if errors.Is(err, errDependencyUnavailable) {
		return http.StatusServiceUnavailable, dependencyUnavailable(), true
	}
	return 0, gen.OrdersProblem{}, false
}

// answers are one operation's typed responses for a refusal, by status; a
// status an operation does not declare is nil, and a refusal with it is a
// 500 — a bug in the handler, never a silent mismatch with the contract.
type answers[R any] struct {
	badRequest    func(gen.OrdersProblem) R
	notFound      func() R
	conflict      func(gen.OrdersProblem) R
	unprocessable func(gen.OrdersProblem) R
	unavailable   func(gen.OrdersProblem) R
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
	case status == http.StatusNotFound && a.notFound != nil:
		return a.notFound(), nil
	case status == http.StatusConflict && a.conflict != nil:
		return a.conflict(p), nil
	case status == http.StatusUnprocessableEntity && a.unprocessable != nil:
		return a.unprocessable(p), nil
	case status == http.StatusServiceUnavailable && a.unavailable != nil:
		return a.unavailable(p), nil
	}
	return zero, fmt.Errorf("orders: a refusal with status %d this operation does not declare: %w", status, err)
}

// noRows reports whether err is a read that found no row.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
