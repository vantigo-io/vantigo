package invoices

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the cover mail a sent document travels with (payments and
// delivery design D4): plain text in the document's language, the wording
// beside the PDF's labels and in the PDF's own formats — money as the totals
// print it, dates as the document prints them, the bank details as the
// snapshot holds them. No HTML, no template system.

// mailText is one cover mail's subject and body.
type mailText struct{ subject, body string }

// coverMail is the cover mail of an issued document. original is a credit
// note's original (its number is named); open is what an invoice has open at
// the send (D3), which decides its payment paragraph — the whole amount
// while open equals the gross, the open part while something is paid, and
// nothing once nothing is open — so a re-send never asks for money that is
// not owed. A credit note reads neither its open amount nor a payment.
func coverMail(inv store.InvoicesInvoice, original *store.InvoicesInvoice, open *big.Rat) (mailText, error) {
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

	var subject, greeting, intro, payment, closing string
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
			switch {
			case settled:
				payment = "The invoice has been settled. Nothing is due."
			case full:
				payment = fmt.Sprintf("Please pay %s, quoting invoice number %d.", to, number)
			default:
				payment = fmt.Sprintf("The outstanding amount is %s; please pay it %s, quoting invoice number %d.", amount(open), to, number)
			}
		} else {
			subject = fmt.Sprintf("Faktura %d fra %s", number, seller)
			intro = fmt.Sprintf("Vedlagt følger faktura %d fra %s på %s, med forfall %s.", number, seller, amount(gross), due)
			switch {
			case settled:
				payment = "Fakturaen er gjort opp. Det er ingenting å betale."
			case full:
				payment = fmt.Sprintf("Beløpet betales til kontonummer %s. Merk betalingen med fakturanummer %d.", account, number)
			default:
				payment = fmt.Sprintf("Utestående beløp er %s, som betales til kontonummer %s. Merk betalingen med fakturanummer %d.", amount(open), account, number)
			}
		}
	}

	var b strings.Builder
	b.WriteString(greeting + "\n\n" + intro + "\n")
	if payment != "" {
		b.WriteString(payment + "\n")
	}
	b.WriteString("\n" + closing + "\n" + seller + "\n")
	return mailText{subject: subject, body: b.String()}, nil
}
