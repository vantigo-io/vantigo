package invoices

import "github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"

// This file is a reminder letter's fixed words (invoices payments and
// reminders design D10), in the letter's language — Norwegian, or English
// when the buyer snapshot's is (plan reading 14): the heading, the labels,
// the sentences the law asks for, the mail's subject and its short cover.
// What each sentence is for:
//
//   - every letter says that a payment made meanwhile makes it moot, and
//     how to object before the deadline (FinKN 2025-240);
//   - an inkassovarsel says, clearly, that the claim will be sent to
//     collection if it is not paid by the deadline — at least 14 days after
//     the letter is sent — and that this may add costs, never that it will
//     (inkassoloven § 9; the deadline is the engine's, reminderrules.LetterDeadline);
//   - a reminder announcing the hand-off, under the 2026 act, says the claim
//     will be handed to a collection agency if unpaid by the deadline
//     (Prop. 3 L (2025–2026) 12.5.5).
//
// Each %-verb is filled by reminderpdf.go; nothing here formats a value.

// reminderWords is one letter's fixed words.
type reminderWords struct {
	heading, subject, fileStem string
	// The meta block's labels.
	date, invoiceNumber, invoiceDate, dueDate, deadline string
	// intro: the invoice's number and due date.
	intro string
	// The claim's amounts, each apart (R13; inkassoloven § 10 c, d).
	amountHeader, invoiceTotal, credited, paid, principalOpen string
	chargesEarlier, chargesCredit, fee, compensation          string
	// interestFrom: the from-date; interestSegment: the rate, the days and
	// the base.
	interestFrom, interestSegment, interestWaived, interestPaid, total string
	// payBy: the amount and the deadline.
	payBy                       string
	account, kid, iban, bic     string
	quoteKID, quoteNumber       string
	paidMeanwhile, objection    string
	notice, noticeCosts         string
	announcement                string
	greeting, coverIntro, cover string
	coverMoot, closing          string
}

// reminderText is the words of a letter of level in lang ("en", else
// Norwegian); announces is a reminder that announces the hand-off (the 2026
// regime), which reads as a reminder with the announcement added.
func reminderText(lang string, level reminderrules.Level, announces bool) reminderWords {
	notice := level == reminderrules.LevelNotice
	if lang == "en" {
		w := reminderWords{
			heading: "Payment reminder", subject: "Reminder: invoice %d", fileStem: "reminder",
			date: "Date", invoiceNumber: "Invoice number", invoiceDate: "Invoice date", dueDate: "Due date",
			deadline:     "Payment deadline",
			intro:        "We have not registered payment of invoice %d, which fell due on %s. This is the claim as it stands today.",
			amountHeader: "Amount (%s)", invoiceTotal: "Invoice total", credited: "Credited", paid: "Paid",
			principalOpen: "Principal outstanding", chargesEarlier: "Earlier fees and compensation outstanding",
			chargesCredit: "Credit from earlier payments of charges", fee: "Reminder fee",
			compensation:    "Compensation for recovery costs",
			interestFrom:    "Late payment interest from %s",
			interestSegment: "%s %% a year from %s to %s on %s",
			interestWaived:  "Interest waived", interestPaid: "Interest already paid", total: "Amount due",
			payBy:   "Please pay %s by %s.",
			account: "Account number", kid: "KID", iban: "IBAN", bic: "BIC",
			quoteKID: "Please quote KID %s with your payment.", quoteNumber: "Please quote invoice number %d with your payment.",
			paidMeanwhile: "If you have paid in the meantime, please disregard this letter.",
			objection:     "If you dispute the claim, let us know before the deadline.",
			notice:        "The claim will be sent to debt collection if it is not paid by %s.",
			noticeCosts:   "Debt collection may add further costs.",
			announcement:  "The claim will be handed over to a debt collection agency if it is not paid by %s.",
			greeting:      "Hello,", coverIntro: "Please find attached a payment reminder for invoice %d from %s.",
			cover:     "The amount due is %s, to be paid by %s.",
			coverMoot: "If you have paid in the meantime, please disregard this e-mail.", closing: "Kind regards",
		}
		if notice {
			w.heading, w.subject, w.fileStem = "Debt collection notice", "Debt collection notice: invoice %d", "collection-notice"
			w.coverIntro = "Please find attached a debt collection notice for invoice %d from %s."
		}
		if !announces || notice {
			w.announcement = ""
		}
		return w
	}
	w := reminderWords{
		heading: "Purring", subject: "Purring: faktura %d", fileStem: "purring",
		date: "Dato", invoiceNumber: "Fakturanummer", invoiceDate: "Fakturadato", dueDate: "Forfallsdato",
		deadline:     "Betalingsfrist",
		intro:        "Vi har ikke registrert betaling av faktura %d, som forfalt %s. Slik står kravet i dag.",
		amountHeader: "Beløp (%s)", invoiceTotal: "Fakturabeløp", credited: "Kreditert", paid: "Betalt",
		principalOpen: "Utestående hovedstol", chargesEarlier: "Tidligere gebyrer og kompensasjon som står ute",
		chargesCredit: "Til gode fra tidligere innbetalinger av gebyrer", fee: "Purregebyr",
		compensation:    "Kompensasjon for inndrivingskostnader",
		interestFrom:    "Forsinkelsesrente fra %s",
		interestSegment: "%s %% per år fra %s til %s av %s",
		interestWaived:  "Frafalt rente", interestPaid: "Rente som allerede er betalt", total: "Å betale",
		payBy:   "Betal %s innen %s.",
		account: "Kontonummer", kid: "KID", iban: "IBAN", bic: "BIC",
		quoteKID: "Merk betalingen med KID %s.", quoteNumber: "Merk betalingen med fakturanummer %d.",
		paidMeanwhile: "Har du betalt i mellomtiden, kan du se bort fra dette brevet.",
		objection:     "Har du innsigelser mot kravet, gi oss beskjed før fristen.",
		notice:        "Kravet vil bli sendt til inkasso dersom det ikke er betalt innen %s.",
		noticeCosts:   "Inkasso kan føre til at det påløper ytterligere kostnader.",
		announcement:  "Kravet vil bli oversendt til et inkassoforetak dersom det ikke er betalt innen %s.",
		greeting:      "Hei,", coverIntro: "Vedlagt følger en purring på faktura %d fra %s.",
		cover:     "Å betale er %s, innen %s.",
		coverMoot: "Har du betalt i mellomtiden, kan du se bort fra denne e-posten.", closing: "Med vennlig hilsen",
	}
	if notice {
		w.heading, w.subject, w.fileStem = "Inkassovarsel", "Inkassovarsel: faktura %d", "inkassovarsel"
		w.coverIntro = "Vedlagt følger et inkassovarsel på faktura %d fra %s."
	}
	if !announces || notice {
		w.announcement = ""
	}
	return w
}
