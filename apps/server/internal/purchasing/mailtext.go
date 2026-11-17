package purchasing

import (
	"fmt"
	"strings"
)

// This file is the cover mail a purchase order's PDF travels with (P21):
// plain text in the order's language, for the order, its revision and its
// cancellation; the dialog's message is put before the closing.

// mailText is one cover mail's subject and body.
type mailText struct{ subject, body string }

// mailFacts are what a cover mail says of its order.
type mailFacts struct {
	kind     string // docOrder or docCancellation
	language string
	number   int64
	version  int32
	seller   string
	message  string
}

// coverMail is the mail text of a purchase order's PDF.
func coverMail(f mailFacts) mailText {
	en := f.language == "en"
	var subject, intro string
	switch {
	case f.kind == docCancellation && en:
		subject = fmt.Sprintf("Cancelled purchase order %d", f.number)
		intro = fmt.Sprintf("We cancel our purchase order %d. Please find the cancellation attached; the order is not to be delivered.", f.number)
	case f.kind == docCancellation:
		subject = fmt.Sprintf("Kansellert innkjøpsordre %d", f.number)
		intro = fmt.Sprintf("Vi kansellerer vår innkjøpsordre %d. Kanselleringen er vedlagt; ordren skal ikke leveres.", f.number)
	case f.version > 1 && en:
		subject = fmt.Sprintf("Revised purchase order %d", f.number)
		intro = fmt.Sprintf("Please find attached our revised purchase order %d (version %d). Please quote our order number on your invoice.", f.number, f.version)
	case f.version > 1:
		subject = fmt.Sprintf("Revidert innkjøpsordre %d", f.number)
		intro = fmt.Sprintf("Vedlagt følger vår reviderte innkjøpsordre %d (versjon %d). Oppgi vårt ordrenummer på fakturaen.", f.number, f.version)
	case en:
		subject = fmt.Sprintf("Purchase order %d", f.number)
		intro = fmt.Sprintf("Please find attached our purchase order %d. Please quote our order number on your invoice.", f.number)
	default:
		subject = fmt.Sprintf("Innkjøpsordre %d", f.number)
		intro = fmt.Sprintf("Vedlagt følger vår innkjøpsordre %d. Oppgi vårt ordrenummer på fakturaen.", f.number)
	}
	if f.seller != "" {
		if en {
			subject += " from " + f.seller
		} else {
			subject += " fra " + f.seller
		}
	}
	greeting, closing := "Hei,", "Med vennlig hilsen"
	if en {
		greeting, closing = "Hello,", "Kind regards"
	}
	body := greeting + "\n\n" + intro + "\n"
	if m := strings.TrimSpace(f.message); m != "" {
		body += "\n" + m + "\n"
	}
	body += "\n" + closing + "\n"
	if f.seller != "" {
		body += f.seller + "\n"
	}
	return mailText{subject: subject, body: body}
}
