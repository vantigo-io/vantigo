package invoices

// The refusal codes of the payments port (invoices payments and reminders
// design D14–D17, D21's 4C half), declared together ahead of the tasks that
// answer them, so the contract's InvoicesConflictProblem lists them and the
// frontend words them once (plan reading 9). invoice_draft,
// credit_note_no_payments, invoice_settled and invoice_handed_off are reused
// from earlier phases.
const (
	// codePaymentsUnavailable: Vipps is switched off, has no credentials, or
	// stored credentials cannot be opened (409 or 503).
	codePaymentsUnavailable = "payments_unavailable"
	// codePaymentAttemptsActive: a credentials change of the sales unit or
	// keys, or their deletion, while an attempt is live (I20).
	codePaymentAttemptsActive = "payment_attempts_active"
	// codeAttemptNotAbandonable: the attempt is not capture_unknown, nor
	// capturing under a switch turned off or another sales unit.
	codeAttemptNotAbandonable = "attempt_not_abandonable"
	// codePublicURLMissing: pay links or a webhook without PUBLIC_BASE_URL.
	codePublicURLMissing = "public_url_missing"
	// codeProviderFailed: Vipps refused or failed a call definitively (502).
	codeProviderFailed = "provider_failed"
	// codePayLinksUnavailable: pay links are switched off or not set up.
	codePayLinksUnavailable = "pay_links_unavailable"
	// codePayLinkExists: the invoice has a live pay link already.
	codePayLinkExists = "pay_link_exists"
	// codeTooManyAttempts: the invoice has five live attempts already.
	codeTooManyAttempts = "too_many_attempts"
	// codeOnSitePaymentsUnavailable: on-site payment is not turned on.
	codeOnSitePaymentsUnavailable = "on_site_payments_unavailable"
	// codeBuyerIdentityMissing: the buyer's snapshot does not identify the
	// buyer as a kontantfaktura must (D17 rule 1).
	codeBuyerIdentityMissing = "buyer_identity_missing"
	// codePaymentRequestActive: the invoice has a live on-site request.
	codePaymentRequestActive = "payment_request_active"
	// codeAmountOutOfRange: the open amount is below 1.00 or above what Vipps
	// takes.
	codeAmountOutOfRange = "amount_out_of_range"
	// codeDayAlreadyReconciled: the day was reconciled before.
	codeDayAlreadyReconciled = "day_already_reconciled"
	// codeDayNotOver: the day is today or later, so it is not over.
	codeDayNotOver = "day_not_over"
	// codePaymentInProgress: live reservations leave less than 1.00 open — a
	// Vipps payment is being completed (Q3, plan reading 6).
	codePaymentInProgress = "payment_in_progress"
)
