package invoices

import "time"

// This file is send all's channel rule (invoices phase 6 design I35, as plan
// E3 and its readings E3 and E5 make it concrete): a pure function of the
// customer's billing profile, the document, today and what the installation
// can send by, judged when a send job runs, never at enqueue. It names the
// channels to try in order; the worker tries them through the send's own
// cores, and only on the path without a preference does a refusal that means
// "not by this way" — not found, not receivable — move on to the next.

// channelPrint is a send job left for printing; its other channels are a
// reminder letter's e-mail (channelEmail) and a transmission's (channelEhf,
// channelEfaktura).
const channelPrint = "print"

// The codes of send all's own skips and refusals.
const (
	codeAlreadyDelivered      = "already_delivered"
	codeSendInProgress        = "send_in_progress"
	codeSendBatchEmpty        = "send_batch_empty"
	codeSendBatchTooLarge     = "send_batch_too_large"
	codeBillingRunNotFinished = "billing_run_not_finished"
	codeSendUncertain         = "send_uncertain"
	// codeSendUncertainElsewhere is a document another send job may have
	// mailed — uncertain, or failed mail_failed — which send all never sends
	// again (plan reading E4).
	codeSendUncertainElsewhere = "send_uncertain_elsewhere"
	codeSendError              = "send_error"
	codeRequesterNotAllowed    = "requester_not_allowed"
)

// channelInput is what the rule judges by: the customer's type ("business",
// "person", or "" when the directory knows none) and delivery preference
// (ehf, efaktura, email, paper or ""), whether it has an invoice e-mail;
// whether the document is a credit note and whether its buyer is a Norwegian
// business (an organisation number on the snapshot); today's business day;
// and what this installation can send by now.
type channelInput struct {
	customerType      string
	preference        string
	hasInvoiceEmail   bool
	creditNote        bool
	norwegianBusiness bool
	today             time.Time
	mailAvailable     bool
	ehfAvailable      bool
	efakturaAvailable bool
}

// channelPlan is the rule's answer. channels are tried in order. preferred
// says the customer's preference chose them: a refusal then fails the job
// with its code, and nothing else is tried (no fallback, plan E3 rule 1).
// When the channels run out — none at all, or every one refused as "not by
// this way" — the job ends as endState with endCode: skipped or failed. A
// plan that tries nothing has only its end.
type channelPlan struct {
	channels  []string
	preferred bool
	endState  string
	endCode   string
}

// b2bDutyApplies is whether the B2B e-invoicing duty covers a Norwegian
// business on today (send.go's b2bDutyFrom): from it, send all never e-mails
// one (reading E5).
func b2bDutyApplies(in channelInput) bool {
	return in.norwegianBusiness && !in.today.Before(b2bDutyFrom)
}

// chooseSendChannel is I35 made concrete.
func chooseSendChannel(in channelInput) channelPlan {
	switch in.preference {
	case "ehf":
		if !in.ehfAvailable {
			return channelPlan{preferred: true, endState: jobFailed, endCode: codeEhfUnavailable}
		}
		return channelPlan{channels: []string{channelEhf}, preferred: true}
	case "efaktura":
		// A credit note is never eFaktura (I31): it takes the person's next
		// channels, as without a preference.
		if in.creditNote {
			return personFallback(in)
		}
		if !in.efakturaAvailable {
			return channelPlan{preferred: true, endState: jobFailed, endCode: codeEfakturaUnavailable}
		}
		return channelPlan{channels: []string{channelEfaktura}, preferred: true}
	case "email":
		if b2bDutyApplies(in) {
			return channelPlan{preferred: true, endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}
		}
		if !in.mailAvailable {
			return channelPlan{preferred: true, endState: jobFailed, endCode: codeMailUnavailable}
		}
		return channelPlan{channels: []string{channelEmail}, preferred: true}
	case "paper":
		return channelPlan{channels: []string{channelPrint}, preferred: true}
	}
	business := in.customerType == "business" || (in.customerType == "" && in.norwegianBusiness)
	if !business {
		plan := personFallback(in)
		if in.efakturaAvailable && !in.creditNote {
			plan.channels = append([]string{channelEfaktura}, plan.channels...)
		}
		return plan
	}
	var plan channelPlan
	if in.ehfAvailable {
		plan.channels = append(plan.channels, channelEhf)
	}
	switch {
	case b2bDutyApplies(in):
		plan.endState, plan.endCode = jobSkipped, warningBuyerNorwegianBusinessRequired
	case !in.mailAvailable:
		plan.endState, plan.endCode = jobFailed, codeMailUnavailable
	case !in.hasInvoiceEmail:
		plan.endState, plan.endCode = jobFailed, codeNoInvoiceEmail
	default:
		plan.channels = append(plan.channels, channelEmail)
	}
	return plan
}

// personFallback is a person's channels after eFaktura: e-mail when there is
// an invoice e-mail and mail can go, then print, which always can. A buyer
// with an organisation number on the snapshot is a Norwegian business
// whatever the directory calls it: from the duty's day it is never e-mailed,
// nor left for print in place of the e-invoice, and is skipped as the
// business path skips it (reading E5).
func personFallback(in channelInput) channelPlan {
	if b2bDutyApplies(in) {
		return channelPlan{endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}
	}
	var plan channelPlan
	if in.mailAvailable && in.hasInvoiceEmail {
		plan.channels = append(plan.channels, channelEmail)
	}
	plan.channels = append(plan.channels, channelPrint)
	return plan
}

// fallsThrough is whether a channel's refusal means "not by this way" on the
// path without a preference: eFaktura's not found and its own conditions
// (rule 6), and EHF's receiver without an address or not receiving.
func fallsThrough(channel, code string) bool {
	switch channel {
	case channelEfaktura:
		switch code {
		case codeEfakturaNotFound, codeEfakturaDueTooSoon, codeEfakturaDueTooFar, codeCurrencyNotNOK, codeKidMissing:
			return true
		}
	case channelEhf:
		switch code {
		case codeNoPeppolID, codePeppolNotReceivable:
			return true
		}
	}
	return false
}
