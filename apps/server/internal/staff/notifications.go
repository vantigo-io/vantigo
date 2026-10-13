package staff

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the absence mails (design S12, plan Task 4, readings 4, 15
// and 26). They go after the write has committed, outside every transaction
// and lock, through the module's one SMTP accessor (smtpSend), and never
// fail the write: with no SMTP driver, no address or a send that fails, a
// log line names the person's id and not their address.
//
// Staff mails only addresses it holds itself — a person's work email, else
// their private one — in the recipient person's language (en or nb): on a
// request and on a registration to the person's manager-deciders (deciders.go,
// managers whose link names an active user), on a decision, a decider's
// cancel and HR's correction to the person. Holders of staff:approve and
// staff:manage get the inbox and the attention list instead: the user
// directory names no address (reading 4).
//
// A mail names the type only when it is neither a sick kind nor HR-only — an
// HR-only one is "leave", a sick or child-sick one "an absence" — and never
// a degree, an hour count, a note or a decision's reason: a mail leaves the
// installation, and what it names is read by whoever reads the inbox.

// The notices an absence write sends.
const (
	noticeRequested  = "requested"
	noticeRegistered = "registered"
	noticeApproved   = "approved"
	noticeRejected   = "rejected"
	noticeCancelled  = "cancelled"
	noticeCorrected  = "corrected"
)

// absenceMailTimeout bounds one absence mail's send.
const absenceMailTimeout = 30 * time.Second

// absenceNotice is what one notice says: what happened to whose absence of
// which type, over which days.
type absenceNotice struct {
	kind        string
	personID    int64
	first, last time.Time
	typ         store.StaffAbsenceType
}

// noticeOf is the notice of kind about row of type t.
func noticeOf(kind string, row store.StaffAbsence, t store.StaffAbsenceType) absenceNotice {
	return absenceNotice{kind: kind, personID: row.PersonID, first: civil(row.FirstDay), last: civil(row.LastDay), typ: t}
}

// notifyAbsence sends n's mails. It runs after the write committed; the
// request's cancellation does not reach the sends.
func (s *server) notifyAbsence(ctx context.Context, today time.Time, n absenceNotice) {
	logger := s.deps.Logger
	if !s.mailAvailable() {
		logger.InfoContext(ctx, "staff: absence mail skipped: the mail driver is not smtp", "person_id", n.personID, "notice", n.kind)
		return
	}
	ctx = context.WithoutCancel(ctx)
	recipients := []int64{n.personID}
	if n.kind == noticeRequested || n.kind == noticeRegistered {
		managers, err := s.managerDeciders(ctx, n.personID, today)
		if err != nil {
			logger.WarnContext(ctx, "staff: absence mail skipped: the managers could not be read", "person_id", n.personID, "error", err.Error())
			return
		}
		recipients = managers
	}
	if len(recipients) == 0 {
		return
	}
	rows, err := store.New(s.deps.Pool).MailRecipients(ctx, append([]int64{n.personID}, recipients...))
	if err != nil {
		logger.WarnContext(ctx, "staff: absence mail skipped: the recipients could not be read", "person_id", n.personID, "error", err.Error())
		return
	}
	byID := map[int64]store.MailRecipientsRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	subjectName := byID[n.personID].DisplayName
	link := ""
	if cfg := s.deps.Config; cfg != nil && cfg.AppOrigin != "" {
		link = cfg.AppOrigin + cfg.BasePath + "/staff/me"
	}
	for _, id := range recipients {
		r, ok := byID[id]
		to := r.WorkEmail
		if to == nil {
			to = r.PrivateEmail
		}
		if !ok || to == nil {
			logger.InfoContext(ctx, "staff: absence mail skipped: the recipient has no email", "person_id", id, "notice", n.kind)
			continue
		}
		subject, body := absenceMail(n, r.PreferredLanguage, r.DisplayName, subjectName, link)
		sendCtx, cancel := context.WithTimeout(ctx, absenceMailTimeout)
		err := s.smtpSend(sendCtx, s.deps.Config.Mail, mail.Outbound{To: []string{*to}, Subject: subject, TextBody: body})
		cancel()
		if err != nil {
			redact := regexp.MustCompile("(?i)" + regexp.QuoteMeta(*to))
			logger.WarnContext(ctx, "staff: an absence mail could not be sent", "person_id", id, "notice", n.kind,
				"error", redact.ReplaceAllLiteralString(err.Error(), "<recipient>"))
		}
	}
}

// mailLabel is how a mail names an absence of type t in language: the type's
// own name only when it is neither a sick kind nor HR-only (reading 15).
func mailLabel(t store.StaffAbsenceType, language string) string {
	en := language == "en"
	switch {
	case healthKind(t.Kind) && en:
		return "an absence"
	case healthKind(t.Kind):
		return "fravær"
	case t.Visibility != visibilityAbsent && en:
		return "leave"
	case t.Visibility != visibilityAbsent:
		return "permisjon"
	case en:
		return t.NameEn
	}
	return t.NameNb
}

// absenceMail is n's subject and text to recipient in language — en, or nb
// for anything else — about person's absence, linking to the app when link
// is set.
func absenceMail(n absenceNotice, language, recipient, person, link string) (string, string) {
	label := mailLabel(n.typ, language)
	from, to := n.first.Format(time.DateOnly), n.last.Format(time.DateOnly)
	if language == "en" {
		var subject, line string
		switch n.kind {
		case noticeRequested:
			subject, line = "Absence request from "+person, fmt.Sprintf("%s has requested %s from %s to %s. You decide it in Vantigo.", person, label, from, to)
		case noticeRegistered:
			subject, line = "Absence registered for "+person, fmt.Sprintf("%s has registered %s from %s to %s.", person, label, from, to)
		case noticeApproved:
			subject, line = "Your absence request was approved", fmt.Sprintf("Your request for %s from %s to %s was approved.", label, from, to)
		case noticeRejected:
			subject, line = "Your absence request was rejected", fmt.Sprintf("Your request for %s from %s to %s was rejected.", label, from, to)
		case noticeCancelled:
			subject, line = "Your absence was cancelled", fmt.Sprintf("Your %s from %s to %s was cancelled.", label, from, to)
		default:
			subject, line = "Your absence was corrected", fmt.Sprintf("HR corrected your absence: it is now %s from %s to %s.", label, from, to)
		}
		body := fmt.Sprintf("Hi %s,\n\n%s\n", recipient, line)
		if link != "" {
			body += "\nOpen Vantigo: " + link + "\n"
		}
		return subject, body
	}
	var subject, line string
	switch n.kind {
	case noticeRequested:
		subject, line = "Fraværssøknad fra "+person, fmt.Sprintf("%s har søkt om %s fra %s til %s. Du avgjør søknaden i Vantigo.", person, label, from, to)
	case noticeRegistered:
		subject, line = "Fravær registrert for "+person, fmt.Sprintf("%s har registrert %s fra %s til %s.", person, label, from, to)
	case noticeApproved:
		subject, line = "Fraværssøknaden din er godkjent", fmt.Sprintf("Søknaden din om %s fra %s til %s er godkjent.", label, from, to)
	case noticeRejected:
		subject, line = "Fraværssøknaden din er avslått", fmt.Sprintf("Søknaden din om %s fra %s til %s er avslått.", label, from, to)
	case noticeCancelled:
		subject, line = "Fraværet ditt er kansellert", fmt.Sprintf("Fraværet ditt (%s) fra %s til %s er kansellert.", label, from, to)
	default:
		subject, line = "Fraværet ditt er rettet", fmt.Sprintf("HR har rettet fraværet ditt: det er nå %s fra %s til %s.", label, from, to)
	}
	body := fmt.Sprintf("Hei %s,\n\n%s\n", recipient, line)
	if link != "" {
		body += "\nÅpne Vantigo: " + link + "\n"
	}
	return subject, body
}
