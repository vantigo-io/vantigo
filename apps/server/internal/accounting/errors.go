package accounting

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This module's refusals follow the codebase's rule: 404 for what does not
// exist (a bare body), 400 naming the field or the line, 409 for a rule about
// the state of the books and 503 for a store that is not there — every one an
// RFC 9457 problem in one shape, gen.AccountingProblem, carrying the rule's
// code (accounting.yaml's AccountingProblem names them, with their statuses).
//
// A handler refuses by returning a *refusal as its error; the strict
// handler's response-error hook (writeRefusal) answers it, so every refusal
// is written the one way, from inside or outside a transaction, and the
// contract recorder holds the status to the operation's declared responses.

// The refusal codes (accounting design §2, §13).
const (
	codeBooksNotSetUp        = "books_not_set_up"
	codeBooksAlreadySetUp    = "books_already_set_up"
	codeRevisionConflict     = "revision_conflict"
	codeAccountNumberTaken   = "account_number_taken"
	codeAccountHasPostings   = "account_has_postings"
	codeAccountHasRole       = "account_has_role"
	codeAccountInactive      = "account_inactive"
	codeAccountUnknown       = "account_unknown"
	codeTaxCodeUnknown       = "tax_code_unknown"
	codeTaxCodeNoRate        = "tax_code_no_rate"
	codeRatePeriodInvalid    = "rate_period_invalid"
	codePartyRequired        = "party_required"
	codePartyNotAllowed      = "party_not_allowed"
	codeOpenItemUnknown      = "open_item_unknown"
	codeVoucherUnbalanced    = "voucher_unbalanced"
	codeVoucherEmpty         = "voucher_empty"
	codeAmountInvalid        = "amount_invalid"
	codeDateBeforeBooks      = "date_before_books_start"
	codePeriodLocked         = "period_locked"
	codeOwnedBySource        = "voucher_owned_by_source"
	codeAlreadyReversed      = "voucher_already_reversed"
	codeOpeningExists        = "opening_exists"
	codeOpeningLocked        = "opening_locked"
	codeLockNotMonthEnd      = "lock_not_month_end"
	codeLockReasonRequired   = "lock_reason_required"
	codeLockBeforeBooksStart = "lock_before_books_start"
	codeSettlementSourceDoc  = "settlement_source_document"
	codeSettlementExceeds    = "settlement_exceeds_balance"
	codeSettlementSettled    = "settlement_settled"
	codeBankAccountInvalid   = "bank_account_invalid"
	codeAttachmentTooLarge   = "attachment_too_large"
	codeStorageNotConfigured = "storage_not_configured"
	codeReportAccountNeeded  = "report_account_required"
	codePeriodInvalid        = "period_invalid"
	// After the review of delivery A.
	codeOpenItemSourceDocument = "open_item_source_document"
	codeReportTooLarge         = "report_too_large"
	codeLockInFuture           = "lock_in_future"
	codeBooksStartTooEarly     = "books_start_too_early"
	codeAccountKindMismatch    = "account_kind_mismatch"
	codeReverseChargeGross     = "reverse_charge_gross"
	// Delivery C, purchases.
	codeSupplierOrgNumberTaken  = "supplier_organisation_number_taken"
	codeSupplierBankAccount     = "supplier_bank_account_invalid"
	codeSupplierHasPostings     = "supplier_has_postings"
	codeSupplierInactive        = "supplier_inactive"
	codeDuplicateSupplierInv    = "duplicate_supplier_invoice"
	codeSupplierInvNotDraft     = "supplier_invoice_not_draft"
	codeSupplierInvNotPosted    = "supplier_invoice_not_posted"
	codeSupplierInvFileRequired = "supplier_invoice_file_required"
	codeSupplierInvTotal        = "supplier_invoice_total_mismatch"
	codeTaxCodeNotPurchase      = "tax_code_not_purchase"
	codeExchangeRateRequired    = "exchange_rate_required"
	codeSupplierInvSettled      = "supplier_invoice_settled"
	codePaymentExceedsBalance   = "payment_exceeds_balance"
	codePaymentRemoved          = "payment_removed"
	codeSettlementSupplierInv   = "settlement_supplier_invoice"
	codeUnpostedWrongReason     = "unposted_wrong_reason"
	codeBrregUnavailable        = "brreg_unavailable"
	codeSupplierInvHasPayments  = "supplier_invoice_has_payments"
	// After the review of delivery C.
	codeTaxCodeNotForSupplierInv = "tax_code_not_for_supplier_invoice"
	codeExchangeRateMismatch     = "exchange_rate_mismatch"
	codeCreditNoteNotPosted      = "credit_note_invoice_not_posted"
	codeCreditNoteCurrency       = "credit_note_currency_mismatch"
	codeCreditNoteExceeds        = "credit_note_exceeds_balance"
	codeSupplierInvItemInUse     = "supplier_invoice_item_in_use"
	codeSupplierRetained         = "supplier_retained"
	codeSupplierAnonymised       = "supplier_anonymised"
	codeExpenseAlreadyInvoiced   = "expense_already_invoiced"
	// Delivery D, VAT.
	codeVatNotRegistered    = "vat_not_registered"
	codeVatTermClosed       = "vat_term_closed"
	codeVatTermNotEnded     = "vat_term_not_ended"
	codeVatTermOutOfOrder   = "vat_term_out_of_order"
	codeVatTermAttention    = "vat_term_attention"
	codeVatTermLockAfterEnd = "vat_term_lock_after_end"
	codeVatTermUnreconciled = "vat_term_unreconciled"
	codeVatTermNotClosed    = "vat_term_not_closed"
	codeVatTermFiled        = "vat_term_filed"
	codeVatTermNotLatest    = "vat_term_not_latest"
	codeVatTermNotFiled     = "vat_term_not_filed"
	codeLockBehindFiled     = "lock_behind_filed_term"
	// After the review of delivery D.
	codeLockBehindClosed    = "lock_behind_closed_term"
	codeVatTermStale        = "vat_term_stale"
	codeOpeningVatAccount   = "opening_vat_account"
	codeTaxCodeInUse        = "tax_code_in_use"
	codeVatTermInconsistent = "vat_term_inconsistent"
	codeVatTermPartial      = "vat_term_partial"
	codeVatTermRateMissing  = "vat_term_rate_missing"
)

// refusalCodes is every code above, for the test that holds the contract's
// AccountingProblem description to naming each one.
var refusalCodes = []string{
	codeBooksNotSetUp, codeBooksAlreadySetUp, codeRevisionConflict, codeAccountNumberTaken, codeAccountHasPostings,
	codeAccountHasRole, codeAccountInactive, codeAccountUnknown, codeTaxCodeUnknown, codeTaxCodeNoRate,
	codeRatePeriodInvalid, codePartyRequired, codePartyNotAllowed, codeOpenItemUnknown, codeVoucherUnbalanced,
	codeVoucherEmpty, codeAmountInvalid, codeDateBeforeBooks, codePeriodLocked, codeOwnedBySource,
	codeAlreadyReversed, codeOpeningExists, codeOpeningLocked, codeLockNotMonthEnd, codeLockReasonRequired,
	codeLockBeforeBooksStart, codeSettlementSourceDoc, codeSettlementExceeds, codeSettlementSettled,
	codeBankAccountInvalid, codeAttachmentTooLarge, codeStorageNotConfigured, codeReportAccountNeeded,
	codePeriodInvalid, codeOpenItemSourceDocument, codeReportTooLarge, codeLockInFuture, codeBooksStartTooEarly,
	codeAccountKindMismatch, codeReverseChargeGross, codeRuleNameTaken, codeRuleMatchTaken, codeRuleDefault,
	codeUnpostedPosted, codeSupplierOrgNumberTaken, codeSupplierBankAccount, codeSupplierHasPostings, codeSupplierInactive,
	codeDuplicateSupplierInv, codeSupplierInvNotDraft, codeSupplierInvNotPosted, codeSupplierInvFileRequired,
	codeSupplierInvTotal, codeTaxCodeNotPurchase, codeExchangeRateRequired, codeSupplierInvSettled,
	codePaymentExceedsBalance, codePaymentRemoved, codeSettlementSupplierInv, codeUnpostedWrongReason,
	codeBrregUnavailable, codeSupplierInvHasPayments, codeTaxCodeNotForSupplierInv, codeExchangeRateMismatch,
	codeCreditNoteNotPosted, codeCreditNoteCurrency, codeCreditNoteExceeds, codeSupplierInvItemInUse,
	codeSupplierRetained, codeSupplierAnonymised, codeExpenseAlreadyInvoiced, codeVatNotRegistered, codeVatTermClosed,
	codeVatTermNotEnded, codeVatTermOutOfOrder, codeVatTermAttention, codeVatTermLockAfterEnd, codeVatTermUnreconciled,
	codeVatTermNotClosed, codeVatTermFiled, codeVatTermNotLatest, codeVatTermNotFiled, codeLockBehindFiled,
	codeLockBehindClosed, codeVatTermStale, codeOpeningVatAccount, codeTaxCodeInUse, codeVatTermInconsistent,
	codeVatTermPartial, codeVatTermRateMissing,
}

// refusal is a request's answer that is not a success. Returned from inside a
// transaction, it rolls back everything the request did.
type refusal struct {
	status  int
	problem gen.AccountingProblem
}

func (r *refusal) Error() string {
	code := ""
	if r.problem.Code != nil {
		code = " " + *r.problem.Code
	}
	return fmt.Sprintf("accounting: refused with %d%s", r.status, code)
}

// problem is a problem body with a status, a code, a title and a detail; an
// empty code or detail is left out.
func problem(status int, code, title, detail string) gen.AccountingProblem {
	s := int32(status)
	p := gen.AccountingProblem{Title: &title, Status: &s}
	if code != "" {
		p.Code = &code
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// refuse is a refusal with status, code, title and detail.
func refuse(status int, code, title, detail string) error {
	return &refusal{status: status, problem: problem(status, code, title, detail)}
}

// conflict is a 409 refusal.
func conflict(code, title, detail string) error {
	return refuse(http.StatusConflict, code, title, detail)
}

// notFound is the bare 404 of an unknown id.
func notFound() error { return &refusal{status: http.StatusNotFound} }

// fieldErrors is a validation pass's outcome: the field errors and, when one
// of them is a rule the design names, its code. The first coded rule to fail
// gives the code.
type fieldErrors struct {
	errs map[string][]string
	code string
}

// add records message on field.
func (f *fieldErrors) add(field, message string) {
	if f.errs == nil {
		f.errs = map[string][]string{}
	}
	f.errs[field] = append(f.errs[field], message)
}

// addCoded records message on field as the coded rule code.
func (f *fieldErrors) addCoded(field, code, message string) {
	f.add(field, message)
	if f.code == "" {
		f.code = code
	}
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// refusal is the 400 of the failed rules under title, nil when none failed.
func (f *fieldErrors) refusal(title string) error {
	if !f.failed() {
		return nil
	}
	p := problem(http.StatusBadRequest, f.code, title, "")
	p.Errors = &f.errs
	return &refusal{status: http.StatusBadRequest, problem: p}
}

// invalidField is a 400 naming one field, under code when it has one.
func invalidField(title, field, code, message string) error {
	var f fieldErrors
	if code == "" {
		f.add(field, message)
	} else {
		f.addCoded(field, code, message)
	}
	return f.refusal(title)
}

// writeRefusal is the strict handler's response-error hook: a *refusal is
// written as its problem (a 404 bare), anything else goes the platform's way.
func writeRefusal() func(http.ResponseWriter, *http.Request, error) {
	platform := module.ResponseError()
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var ref *refusal
		if !errors.As(err, &ref) {
			platform(w, r, err)
			return
		}
		if ref.status == http.StatusNotFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(ref.status)
		_ = json.NewEncoder(w).Encode(ref.problem)
	}
}

// noRows reports whether err is a read that found no row.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
