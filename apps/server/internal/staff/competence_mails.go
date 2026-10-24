package staff

import (
	"fmt"
	"time"
)

// This file is the staff-competence worker's mails (staff competence design
// S7, reading 5): one to the person and one to each of their
// manager-deciders when a current row comes within its competence's lead of
// its end (expiring), and again when it has ended (expired). Each is plain
// text in the recipient's language — en, else nb, as the absence mails
// (notifications.go) — naming the competence in that language and the last
// day it is valid; the manager's names the person too. Neither names the
// issuer, the reference, the level or the evidence.

// The two notices the worker sends.
const (
	competenceExpiringNotice = "expiring"
	competenceExpiredNotice  = "expired"
)

// competenceMail is the subject and text of notice to recipient in language
// about the competence named en or nb, valid to validTo. person is "" for
// the person's own mail, else the person's name in their manager's. The
// link, when set, closes the text.
func competenceMail(notice, language, recipient, person, nameEn, nameNb string, validTo time.Time, link string) (string, string) {
	day := validTo.Format(time.DateOnly)
	if language == "en" {
		var subject, line string
		switch {
		case notice == competenceExpiringNotice && person == "":
			subject = fmt.Sprintf("Your competence “%s” expires soon", nameEn)
			line = fmt.Sprintf("Your competence “%s” is valid until %s. When it is renewed, add the new one on your page in Vantigo.", nameEn, day)
		case notice == competenceExpiringNotice:
			subject = fmt.Sprintf("The competence “%s” of %s expires soon", nameEn, person)
			line = fmt.Sprintf("The competence “%s” of %s is valid until %s.", nameEn, person, day)
		case person == "":
			subject = fmt.Sprintf("Your competence “%s” has expired", nameEn)
			line = fmt.Sprintf("Your competence “%s” was valid until %s and has expired. When it is renewed, add the new one on your page in Vantigo.", nameEn, day)
		default:
			subject = fmt.Sprintf("The competence “%s” of %s has expired", nameEn, person)
			line = fmt.Sprintf("The competence “%s” of %s was valid until %s and has expired.", nameEn, person, day)
		}
		body := fmt.Sprintf("Hi %s,\n\n%s\n", recipient, line)
		if link != "" {
			body += "\nOpen Vantigo: " + link + "\n"
		}
		return subject, body
	}
	var subject, line string
	switch {
	case notice == competenceExpiringNotice && person == "":
		subject = fmt.Sprintf("Kompetansen din «%s» utløper snart", nameNb)
		line = fmt.Sprintf("Kompetansen din «%s» er gyldig til og med %s. Når den er fornyet, legger du inn den nye på siden din i Vantigo.", nameNb, day)
	case notice == competenceExpiringNotice:
		subject = fmt.Sprintf("Kompetansen «%s» for %s utløper snart", nameNb, person)
		line = fmt.Sprintf("Kompetansen «%s» for %s er gyldig til og med %s.", nameNb, person, day)
	case person == "":
		subject = fmt.Sprintf("Kompetansen din «%s» er utløpt", nameNb)
		line = fmt.Sprintf("Kompetansen din «%s» var gyldig til og med %s og er utløpt. Når den er fornyet, legger du inn den nye på siden din i Vantigo.", nameNb, day)
	default:
		subject = fmt.Sprintf("Kompetansen «%s» for %s er utløpt", nameNb, person)
		line = fmt.Sprintf("Kompetansen «%s» for %s var gyldig til og med %s og er utløpt.", nameNb, person, day)
	}
	body := fmt.Sprintf("Hei %s,\n\n%s\n", recipient, line)
	if link != "" {
		body += "\nÅpne Vantigo: " + link + "\n"
	}
	return subject, body
}
