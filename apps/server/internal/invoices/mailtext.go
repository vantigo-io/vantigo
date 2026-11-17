package invoices

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"math/big"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the cover mail a sent document travels with (payments and
// delivery design D4): text in the document's language, the wording beside
// the PDF's labels and in the PDF's own formats — money as the totals print
// it, dates as the document prints them, the bank details as the snapshot
// holds them. Every mail is HTML too (invoices phase 6 design I5): one small
// html/template, embedded, says the same words as the text body — the
// paragraphs, the pay link as a link, and the seller's logo as an inline part
// (cid:logo) when the document has one. Inline styles only; no external
// image, no tracking and no link but the pay link. A reminder letter's mail
// (reminder_worker.go) takes the same template without a message.

// mailText is one cover mail's subject, text body and HTML body.
type mailText struct{ subject, body, html string }

//go:embed mailtext.html.tmpl
var mailHTMLSource string

// mailHTMLTemplate is the HTML body of every mail this module sends.
var mailHTMLTemplate = template.Must(template.New("mail").Parse(mailHTMLSource))

// mailHTML is what the HTML body says, in the order it says it: the
// greeting, the personal message (I6) split at its line breaks, the
// paragraphs, the pay link's lead and URL, the closing and the seller, whose
// name is the logo's alt text. Logo is whether the mail carries the logo as
// its inline part named "logo".
type mailHTML struct {
	Lang, Subject, Greeting string
	Message                 []string
	Paragraphs              []string
	PayLead, PayURL         string
	Closing, Seller         string
	Logo                    bool
}

// render is the HTML body: every value escaped by html/template.
func (m mailHTML) render() (string, error) {
	var b bytes.Buffer
	if err := mailHTMLTemplate.Execute(&b, m); err != nil {
		return "", fmt.Errorf("invoices: render the mail's HTML: %w", err)
	}
	return b.String(), nil
}

// messageLines is a personal message as the HTML body keeps it: its lines,
// in order, each written with a line break before it but the first; none for
// no message.
func messageLines(message string) []string {
	if message == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
}

// coverMail is the cover mail of an issued document. original is a credit
// note's original (its number is named); open is what an invoice has open at
// the send (D3), which decides its payment paragraph — the whole amount
// while open equals the gross, the open part while something is paid, and
// nothing once nothing is open — so a re-send never asks for money that is
// not owed. A credit note reads neither its open amount nor a payment.
// payURL is the invoice's live pay link (D15), "" for none: printed after the
// payment paragraph while something is open, as another way to pay — the
// e-mail reaches the customer remotely, a credit sale (D17). message is the
// sender's personal message (invoices phase 6 design I6), "" for none: after
// the greeting, above the standard text, in both bodies. logo is whether the
// mail carries the document's logo inline.
func coverMail(inv store.InvoicesInvoice, original *store.InvoicesInvoice, open *big.Rat, payURL, message string, logo bool) (mailText, error) {
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	lang := "nb"
	if str(inv.BuyerLanguage) == "en" {
		lang = "en"
	}
	currency := inv.Currency
	if currency == "" {
		currency = "NOK"
	}
	amount := func(v *big.Rat) string { return currency + " " + money(v, lang) }
	gross, err := ratFromNumeric(inv.GrossTotal)
	if err != nil {
		return mailText{}, err
	}
	number, seller := *inv.Number, str(inv.SellerLegalName)

	var subject, greeting, intro, payment, payLead, closing string
	if lang == "en" {
		greeting, closing = "Hello,", "Kind regards"
	} else {
		greeting, closing = "Hei,", "Med vennlig hilsen"
	}
	if inv.Kind == kindCreditNote {
		// A credit note names the invoice it credits; without that number
		// there is no true sentence to send, never "faktura 0".
		if original == nil || original.Number == nil {
			return mailText{}, fmt.Errorf("invoices: credit note %d's cover mail needs its original's number", inv.ID)
		}
		credited := *original.Number
		if lang == "en" {
			subject = fmt.Sprintf("Credit note %d from %s", number, seller)
			intro = fmt.Sprintf("Please find attached credit note %d from %s for %s, crediting invoice %d.", number, seller, amount(gross), credited)
		} else {
			subject = fmt.Sprintf("Kreditnota %d fra %s", number, seller)
			intro = fmt.Sprintf("Vedlagt følger kreditnota %d fra %s på %s, som krediterer faktura %d.", number, seller, amount(gross), credited)
		}
	} else {
		due := ""
		if inv.DueDate.Valid {
			due = formatDate(inv.DueDate.Time, lang)
		}
		account, iban, bic := str(inv.SellerBankAccount), str(inv.SellerIban), str(inv.SellerBic)
		settled, full := open.Sign() <= 0, open.Cmp(gross) >= 0
		// The payment is marked with the KID when the invoice has one (EHF
		// and KID design D3), and with the invoice number only without.
		quoteEN, quoteNB := fmt.Sprintf("quoting invoice number %d", number), fmt.Sprintf("Merk betalingen med fakturanummer %d", number)
		if inv.Kid != nil {
			quoteEN, quoteNB = "quoting KID "+*inv.Kid, "Merk betalingen med KID "+*inv.Kid
		}
		if lang == "en" {
			subject = fmt.Sprintf("Invoice %d from %s", number, seller)
			intro = fmt.Sprintf("Please find attached invoice %d from %s for %s, due %s.", number, seller, amount(gross), due)
			// An English buyer is usually abroad and cannot pay a domestic
			// account: the IBAN when the snapshot has one.
			to := "to account " + account
			if iban != "" {
				to = "to IBAN " + iban
				if bic != "" {
					to += " (BIC " + bic + ")"
				}
			}
			if !settled && payURL != "" {
				payLead = "You can also pay with Vipps:"
			}
			switch {
			case settled:
				payment = "The invoice has been settled. Nothing is due."
			case full:
				payment = fmt.Sprintf("Please pay %s, %s.", to, quoteEN)
			default:
				payment = fmt.Sprintf("The outstanding amount is %s; please pay it %s, %s.", amount(open), to, quoteEN)
			}
		} else {
			subject = fmt.Sprintf("Faktura %d fra %s", number, seller)
			intro = fmt.Sprintf("Vedlagt følger faktura %d fra %s på %s, med forfall %s.", number, seller, amount(gross), due)
			if !settled && payURL != "" {
				payLead = "Du kan også betale med Vipps:"
			}
			switch {
			case settled:
				payment = "Fakturaen er gjort opp. Det er ingenting å betale."
			case full:
				payment = fmt.Sprintf("Beløpet betales til kontonummer %s. %s.", account, quoteNB)
			default:
				payment = fmt.Sprintf("Utestående beløp er %s, som betales til kontonummer %s. %s.", amount(open), account, quoteNB)
			}
		}
	}

	var b strings.Builder
	b.WriteString(greeting + "\n\n")
	if message != "" {
		b.WriteString(message + "\n\n")
	}
	b.WriteString(intro + "\n")
	paragraphs := []string{intro}
	if payment != "" {
		b.WriteString(payment + "\n")
		paragraphs = append(paragraphs, payment)
	}
	if payLead != "" {
		b.WriteString(payLead + " " + payURL + "\n")
	} else {
		payURL = ""
	}
	b.WriteString("\n" + closing + "\n" + seller + "\n")
	html, err := mailHTML{
		Lang: lang, Subject: subject, Greeting: greeting, Message: messageLines(message), Paragraphs: paragraphs,
		PayLead: payLead, PayURL: payURL, Closing: closing, Seller: seller, Logo: logo,
	}.render()
	if err != nil {
		return mailText{}, err
	}
	return mailText{subject: subject, body: b.String(), html: html}, nil
}
