package purchasing

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// This module's refusals follow the codebase's rule: 404 for what does not
// exist, 403 for what is not the caller's to do, 400 naming the field or the
// line, 409 for a rule about the state of things and 503 for a module or a
// channel that is not there — every one an RFC 9457 problem in one shape,
// gen.PurchasingProblem, carrying the rule's code (purchasing.yaml's
// PurchasingProblem names them). A client keys off the code, never the words.

// The refusal codes.
const (
	codeNotFound              = "not_found"
	codeSupplierNotFound      = "supplier_not_found"
	codeSiteNotFound          = "site_not_found"
	codeSiteArchived          = "site_archived"
	codeCurrencyInvalid       = "currency_invalid"
	codeSeriesStartTooLow     = "series_start_too_low"
	codeSendChannelUnknown    = "send_channel_unknown"
	codeRecipientMissing      = "recipient_missing"
	codeOrderEmpty            = "order_empty"
	codeImportInvalid         = "import_invalid"
	codeVersionConflict       = "version_conflict"
	codeOrderNotDraft         = "order_not_draft"
	codeOrderNotSent          = "order_not_sent"
	codeOrderNotClosed        = "order_not_closed"
	codeOrderHasReceipts      = "order_has_receipts"
	codeLineClosed            = "line_closed"
	codeLineKindChanged       = "line_kind_changed"
	codeCurrencyFixed         = "currency_fixed"
	codeSupplierInactive      = "supplier_inactive"
	codeSupplierChanged       = "supplier_changed"
	codeSupplierItemExists    = "supplier_item_exists"
	codeClientRequestReused   = "client_request_reused"
	codeDependencyUnavailable = "dependency_unavailable"
	codeSendingDisabled       = "sending_disabled"
	codeMailFailed            = "mail_failed"
	codeAmountOutOfRange      = "amount_out_of_range"

	// A line's codes (PurchasingProblemLine.code).
	codeTooManyLines          = "too_many_lines"
	codeLineInvalid           = "line_invalid"
	codeLineNotFound          = "line_not_found"
	codeVariantNotFound       = "variant_not_found"
	codeVariantDiscontinued   = "variant_discontinued"
	codePackagingNotFound     = "packaging_not_found"
	codeQuantityInvalid       = "quantity_invalid"
	codePriceInvalid          = "price_invalid"
	codeDiscountInvalid       = "discount_invalid"
	codeQuantityBelowReceived = "quantity_below_received"

	// Receipts' codes (delivery B).
	codeLineNotReceivable  = "line_not_receivable"
	codeOverReceipt        = "over_receipt"
	codeReceiptUndone      = "receipt_undone"
	codeReceiptInvoiced    = "receipt_invoiced"
	codeLineNotDeliverable = "line_not_deliverable"
)

// refusalCodes is every code above, which PurchasingProblem's description
// names (TestProblemDescription_NamesEveryCode).
var refusalCodes = []string{
	codeNotFound, codeSupplierNotFound, codeSiteNotFound, codeSiteArchived, codeCurrencyInvalid, codeSeriesStartTooLow,
	codeSendChannelUnknown, codeRecipientMissing, codeOrderEmpty, codeImportInvalid, codeVersionConflict,
	codeOrderNotDraft, codeOrderNotSent, codeOrderNotClosed, codeOrderHasReceipts, codeLineClosed,
	codeLineKindChanged, codeCurrencyFixed, codeSupplierInactive, codeSupplierChanged, codeSupplierItemExists,
	codeClientRequestReused, codeDependencyUnavailable, codeSendingDisabled, codeMailFailed, codeAmountOutOfRange,
	codeTooManyLines, codeLineInvalid, codeLineNotFound, codeVariantNotFound, codeVariantDiscontinued,
	codePackagingNotFound, codeQuantityInvalid, codePriceInvalid, codeDiscountInvalid, codeQuantityBelowReceived,
	codeLineNotReceivable, codeOverReceipt, codeReceiptUndone, codeReceiptInvoiced, codeLineNotDeliverable,
}

// The titles this module's validation problems carry.
const (
	invalidOrderTitle        = "Invalid purchase order"
	invalidSettingsTitle     = "Invalid settings"
	invalidSendTitle         = "Invalid send"
	invalidQueryTitle        = "Invalid query"
	invalidSupplierItemTitle = "Invalid supplier item"
	invalidImportTitle       = "Invalid import"
	invalidConfirmTitle      = "Invalid confirmation"
	invalidReceiptTitle      = "Invalid receipt"
)

// errDependencyUnavailable is every accessor's answer when the contract it
// reads is not composed: config makes that impossible for the register, the
// catalog and the stock in a running installation, and a handler answers it
// as the 503 dependency_unavailable.
var errDependencyUnavailable = errors.New("purchasing: a module this one reads is not composed")

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
func problem(status int, code, title, detail string) gen.PurchasingProblem {
	s := int32(status)
	p := gen.PurchasingProblem{Title: &title, Status: &s}
	if code != "" {
		p.Code = &code
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.PurchasingProblem {
	p := problem(http.StatusBadRequest, f.code, title, "")
	p.Errors = &f.errs
	return p
}

// lineFailure is one failing line of an order: every one is named at once.
type lineFailure struct {
	index  int
	code   string
	detail string
}

// linesProblem is an order's refusal naming every failing line, a 400 whose
// own code is the first line's.
func linesProblem(title string, failures []lineFailure) gen.PurchasingProblem {
	lines := make([]gen.PurchasingProblemLine, 0, len(failures))
	for _, l := range failures {
		pl := gen.PurchasingProblemLine{Index: int32(l.index), Code: l.code}
		if l.detail != "" {
			pl.Detail = ptr(l.detail)
		}
		lines = append(lines, pl)
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
	problem gen.PurchasingProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("purchasing: refused with %d", r.status) }

// notFound is the 404 of an unknown id.
func notFound(what string) error {
	return refuse(problem(http.StatusNotFound, codeNotFound, "Not found", fmt.Sprintf("No %s has this id.", what)))
}

// refuse is a refusal with p's status.
func refuse(p gen.PurchasingProblem) error {
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

// refuseLines is an order's refusal naming every failing line.
func refuseLines(title string, failures []lineFailure) error {
	return refuse(linesProblem(title, failures))
}

// conflict is a 409 with code.
func conflict(code, title, detail string) error {
	return refuse(problem(http.StatusConflict, code, title, detail))
}

// versionConflict is the 409 of a stale revision.
func versionConflict(what string, current, supplied int32) error {
	return conflict(codeVersionConflict, "Version conflict",
		fmt.Sprintf("The %s is at %d; the request was made from %d. Reload it and try again.", what, current, supplied))
}

// judgeFresh refuses a write made from a read of an order older than its
// last write: expected is the updatedAt the client read (version_conflict).
func judgeFresh(updatedAt, expected time.Time) error {
	if updatedAt.Equal(expected) {
		return nil
	}
	return conflict(codeVersionConflict, "Version conflict",
		fmt.Sprintf("The order was changed at %s; the request was made from what it was at %s. Reload it and try again.",
			updatedAt.UTC().Format(time.RFC3339Nano), expected.UTC().Format(time.RFC3339Nano)))
}

// dependencyUnavailable is the 503 an operation answers when a module it
// reads is not composed.
func dependencyUnavailable() gen.PurchasingProblem {
	return problem(http.StatusServiceUnavailable, codeDependencyUnavailable, "A module Purchasing reads is unavailable",
		"Purchasing reads the supplier register, the product catalog and the sites, and one of them is not running in this installation. Nothing was changed.")
}

// sendingDisabled is the 503 of a send when Communications is off or no
// channel to send from is chosen or able to send (P21).
func sendingDisabled() error {
	return refuse(problem(http.StatusServiceUnavailable, codeSendingDisabled, "Sending is disabled",
		"Communications is off, or the Purchasing settings choose no channel that can send. Download the PDF and mark the order as sent instead."))
}

// mailFailed is the answer of a send the mail provider did not take: the
// order is sent, and its mailing is queued for the purchasing.mail worker.
func mailFailed() error {
	return refuse(problem(http.StatusServiceUnavailable, codeMailFailed, "The e-mail could not be sent",
		"The e-mail did not go out now. The order stays as it now is, and its mailing is queued: it is tried again until it goes out."))
}

// answerOf is how a handler reads an error: a refusal's status and body,
// errDependencyUnavailable as the 503, and ok false for anything else, which
// the handler returns as the 500 it is.
func answerOf(err error) (int, gen.PurchasingProblem, bool) {
	var r *refusal
	if errors.As(err, &r) {
		return r.status, r.problem, true
	}
	if errors.Is(err, errDependencyUnavailable) {
		return http.StatusServiceUnavailable, dependencyUnavailable(), true
	}
	return 0, gen.PurchasingProblem{}, false
}

// answers are one operation's typed responses for a refusal, by status; a
// status an operation does not declare is nil, and a refusal with it is a
// 500 — a bug in the handler, never a silent mismatch with the contract.
type answers[R any] struct {
	badRequest  func(gen.PurchasingProblem) R
	notFound    func(gen.PurchasingProblem) R
	conflict    func(gen.PurchasingProblem) R
	unavailable func(gen.PurchasingProblem) R
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
		return a.notFound(p), nil
	case status == http.StatusConflict && a.conflict != nil:
		return a.conflict(p), nil
	case status == http.StatusServiceUnavailable && a.unavailable != nil:
		return a.unavailable(p), nil
	}
	return zero, fmt.Errorf("purchasing: a refusal with status %d this operation does not declare: %w", status, err)
}

// noRows reports whether err is a read that found no row.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// RefusalProblem is the problem a refusal of Operations carries, for the
// seed to report what was refused and why; false for any other error.
func RefusalProblem(err error) (gen.PurchasingProblem, bool) {
	_, p, ok := answerOf(err)
	return p, ok
}
