package orders

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// This file is the cover mail a sent quote or order confirmation travels
// with (O25): plain text in the document's language, per document kind, the
// money and the dates in the PDF's own formats — Invoices' mailtext.go's
// shape. The settings may override a kind's subject and body per language;
// an override may name {number}, {seller}, {total} and {validUntil}, which
// are filled in, and the dialog's message is put before the closing.

// mailText is one cover mail's subject and body.
type mailText struct{ subject, body string }

// mailFacts are what a cover mail says of its document.
type mailFacts struct {
	kind       string // docQuote or docConfirmation
	language   string
	number     int64
	version    int32
	revised    bool
	seller     string
	currency   string
	gross      *big.Rat
	validUntil *time.Time
	message    string
}

// coverMail is the mail text of a document, the override's when the
// settings hold one for its kind and language.
func coverMail(f mailFacts, override *mailTextOverride) mailText {
	lang := "nb"
	if f.language == "en" {
		lang = "en"
	}
	total := f.currency + " " + money(orZero(f.gross), lang)
	valid := ""
	if f.validUntil != nil {
		valid = formatDate(*f.validUntil, lang)
	}
	seller := f.seller
	var subject, intro string
	switch {
	case f.kind == docQuote && lang == "en":
		subject = fmt.Sprintf("Quote %d", f.number)
		intro = fmt.Sprintf("Please find attached quote %d for %s", f.number, total)
		if valid != "" {
			intro += ", valid until " + valid
		}
		intro += "."
	case f.kind == docQuote:
		subject = fmt.Sprintf("Tilbud %d", f.number)
		intro = fmt.Sprintf("Vedlagt følger tilbud %d på %s", f.number, total)
		if valid != "" {
			intro += ", gyldig til " + valid
		}
		intro += "."
	case lang == "en" && f.revised:
		subject = fmt.Sprintf("Revised order confirmation %d", f.number)
		intro = fmt.Sprintf("Please find attached the revised confirmation of order %d (version %d), for %s.", f.number, f.version, total)
	case lang == "en":
		subject = fmt.Sprintf("Order confirmation %d", f.number)
		intro = fmt.Sprintf("Please find attached the confirmation of order %d, for %s.", f.number, total)
	case f.revised:
		subject = fmt.Sprintf("Revidert ordrebekreftelse %d", f.number)
		intro = fmt.Sprintf("Vedlagt følger revidert ordrebekreftelse for ordre %d (versjon %d) på %s.", f.number, f.version, total)
	default:
		subject = fmt.Sprintf("Ordrebekreftelse %d", f.number)
		intro = fmt.Sprintf("Vedlagt følger ordrebekreftelse for ordre %d på %s.", f.number, total)
	}
	if seller != "" {
		if lang == "en" {
			subject += " from " + seller
		} else {
			subject += " fra " + seller
		}
	}
	greeting, closing := "Hei,", "Med vennlig hilsen"
	if lang == "en" {
		greeting, closing = "Hello,", "Kind regards"
	}
	body := greeting + "\n\n" + intro + "\n"
	if override != nil {
		fill := strings.NewReplacer("{number}", fmt.Sprint(f.number), "{seller}", seller, "{total}", total, "{validUntil}", valid)
		if s := strings.TrimSpace(override.Subject); s != "" {
			subject = fill.Replace(s)
		}
		if b := strings.TrimSpace(override.Body); b != "" {
			body = fill.Replace(b) + "\n"
		}
	}
	if m := strings.TrimSpace(f.message); m != "" {
		body += "\n" + m + "\n"
	}
	body += "\n" + closing + "\n"
	if seller != "" {
		body += seller + "\n"
	}
	return mailText{subject: subject, body: body}
}
