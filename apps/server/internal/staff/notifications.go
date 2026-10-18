package staff

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the absence mails (design S12, plan Task 4, readings 4, 15
// and 26; delivery C, Task 5 and the owner's answer 3). They go after the
// write has committed, outside every transaction and lock, through the
// module's one SMTP accessor (smtpSend), one after the other under one
// budget of absenceMailTimeout for the whole notice, and never fail the
// write: with no SMTP driver, no address, a send that fails or a budget
// spent, a log line names the person's or the user's id, never an address.
//
// Who is mailed, in the recipient's language (en, else nb):
//
//   - a request or a registration, to the person's manager-deciders
//     (deciders.go, managers whose link names an active user) at the
//     address Staff holds for them — the work email, else the private one;
//   - a request whose person has no manager-decider, to every active holder
//     of staff:approve or staff:manage instead — Owners by the wildcard —
//     once each, never the person's own user, at the address identity holds
//     (holders.go; reading 18), in the language of the person the holder is
//     linked to, else the user's own;
//   - a request of an HR-only type, to no manager and no holder of either
//     (review H3), but to the other active holders of staff:personal-data,
//     its only deciders (reading 20), in a neutral mail that names neither
//     the type nor the days; an HR-only registration to nobody;
//   - a decision, a decider's cancel and HR's correction, to the person.
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

// absenceMailTimeout bounds one notice's sends, all of them together.
const absenceMailTimeout = 30 * time.Second

// The app's pages a mail links to: the person's own page, and the absence
// area where a holder by right decides.
const (
	mePage      = "/staff/me"
	absencePage = "/staff/absence"
)

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

// absenceMailTo is one mail of a notice: the address, the words, and the id
// a log line names its recipient by (logKey person_id or user_id).
type absenceMailTo struct {
	to, subject, body string
	logKey, logID     string
}

// notifyAbsence sends n's mails. It runs after the write committed; the
// request's cancellation does not reach the sends.
func (s *server) notifyAbsence(ctx context.Context, caller *callerContext, n absenceNotice) {
	logger := s.deps.Logger
	if !s.mailAvailable() {
		logger.InfoContext(ctx, "staff: absence mail skipped: the mail driver is not smtp", "person_id", n.personID, "notice", n.kind)
		return
	}
	ctx = context.WithoutCancel(ctx)
	var mails []absenceMailTo
	switch {
	case n.kind == noticeRequested && hrOnlyType(n.typ.Visibility):
		mails = s.personalDataMails(ctx, caller, n)
	case n.kind == noticeRequested || n.kind == noticeRegistered:
		if hrOnlyType(n.typ.Visibility) {
			return
		}
		managers, err := s.managerDeciders(ctx, n.personID, caller.Today)
		if err != nil {
			logger.WarnContext(ctx, "staff: absence mail skipped: the managers could not be read", "person_id", n.personID, "error", err.Error())
			return
		}
		switch {
		case len(managers) > 0:
			mails = s.peopleMails(ctx, n, managers)
		case n.kind == noticeRequested:
			mails = s.holderMails(ctx, caller, n)
		}
	default:
		mails = s.peopleMails(ctx, n, []int64{n.personID})
	}
	s.sendAbsenceMails(ctx, n, mails)
}

// appLink is page in the app, "" when the installation has no origin.
func (s *server) appLink(page string) string {
	if cfg := s.deps.Config; cfg != nil && cfg.AppOrigin != "" {
		return cfg.AppOrigin + cfg.BasePath + page
	}
	return ""
}

// peopleMails is n's mail to each of recipients, people of the register, at
// the address Staff holds for them.
func (s *server) peopleMails(ctx context.Context, n absenceNotice, recipients []int64) []absenceMailTo {
	logger := s.deps.Logger
	rows, err := store.New(s.deps.Pool).MailRecipients(ctx, append([]int64{n.personID}, recipients...))
	if err != nil {
		logger.WarnContext(ctx, "staff: absence mail skipped: the recipients could not be read", "person_id", n.personID, "error", err.Error())
		return nil
	}
	byID := map[int64]store.MailRecipientsRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	subjectName, link := byID[n.personID].DisplayName, s.appLink(mePage)
	var out []absenceMailTo
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
		out = append(out, absenceMailTo{to: *to, subject: subject, body: body, logKey: "person_id", logID: strconv.FormatInt(id, 10)})
	}
	return out
}

// mailHolder is a holder by right to mail: the address identity holds, and
// the name and the language to greet them in.
type mailHolder struct {
	id                 uuid.UUID
	to, name, language string
}

// holdersToMail is the active holders of keys, each once, leaving out the
// person's own user and anyone identity holds no reachable address for —
// each greeted by the person they are linked to, else as identity names
// them, in that person's language, else the user's, else nb. The read
// failing mails nobody (holders.go).
func (s *server) holdersToMail(ctx context.Context, caller *callerContext, n absenceNotice, keys ...string) ([]mailHolder, bool) {
	logger := s.deps.Logger
	q := store.New(s.deps.Pool)
	links, err := q.PeopleLinks(ctx, []int64{n.personID})
	if err != nil {
		logger.WarnContext(ctx, "staff: absence mail skipped: the person's link could not be read", "person_id", n.personID, "error", err.Error())
		return nil, false
	}
	seen := map[uuid.UUID]bool{}
	for _, l := range links {
		if l.ExternalID == nil {
			continue
		}
		if id, err := uuid.Parse(*l.ExternalID); err == nil {
			seen[id] = true
		}
	}
	var holders []contracts.Holder
	for _, key := range keys {
		got, err := s.holders(ctx, caller, key)
		if err != nil {
			logger.WarnContext(ctx, "staff: absence mail skipped: the holders could not be read", "person_id", n.personID, "notice", n.kind, "error", err.Error())
			return nil, false
		}
		for _, h := range got {
			if seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			if h.Email == "" {
				logger.InfoContext(ctx, "staff: absence mail skipped: the holder has no address a mail reaches", "user_id", h.ID.String(), "notice", n.kind)
				continue
			}
			holders = append(holders, h)
		}
	}
	ids := make([]string, 0, len(holders))
	for _, h := range holders {
		ids = append(ids, h.ID.String())
	}
	people := map[string]store.LinkedPeopleOfUsersRow{}
	if len(ids) > 0 {
		rows, err := q.LinkedPeopleOfUsers(ctx, ids)
		if err != nil {
			logger.WarnContext(ctx, "staff: absence mail skipped: the holders' people could not be read", "person_id", n.personID, "error", err.Error())
			return nil, false
		}
		for _, r := range rows {
			people[r.UserID] = r
		}
	}
	out := make([]mailHolder, 0, len(holders))
	for _, h := range holders {
		m := mailHolder{id: h.ID, to: h.Email, name: h.DisplayName, language: h.PreferredLanguage}
		if p, ok := people[h.ID.String()]; ok {
			m.name = p.DisplayName
			if p.PreferredLanguage != "" {
				m.language = p.PreferredLanguage
			}
		}
		out = append(out, m)
	}
	return out, true
}

// holderMails is a request's mail to the holders by right — staff:approve
// and staff:manage — when the person has no manager-decider (the owner's
// answer 3): the type named by B's rule, linking to the absence area.
func (s *server) holderMails(ctx context.Context, caller *callerContext, n absenceNotice) []absenceMailTo {
	holders, ok := s.holdersToMail(ctx, caller, n, byRightKeys...)
	if !ok || len(holders) == 0 {
		return nil
	}
	rows, err := store.New(s.deps.Pool).MailRecipients(ctx, []int64{n.personID})
	if err != nil || len(rows) == 0 {
		args := []any{"person_id", n.personID}
		if err != nil {
			args = append(args, "error", err.Error())
		}
		s.deps.Logger.WarnContext(ctx, "staff: absence mail skipped: the person could not be read", args...)
		return nil
	}
	link := s.appLink(absencePage)
	out := make([]absenceMailTo, 0, len(holders))
	for _, h := range holders {
		subject, body := absenceMail(n, h.language, h.name, rows[0].DisplayName, link)
		out = append(out, absenceMailTo{to: h.to, subject: subject, body: body, logKey: "user_id", logID: h.id.String()})
	}
	return out
}

// personalDataMails is an HR-only request's mail to the other holders of
// staff:personal-data, its only deciders (reading 20): neutral — no type, no
// days, no person — linking to the absence area.
func (s *server) personalDataMails(ctx context.Context, caller *callerContext, n absenceNotice) []absenceMailTo {
	holders, ok := s.holdersToMail(ctx, caller, n, permPersonalData)
	if !ok {
		return nil
	}
	link := s.appLink(absencePage)
	out := make([]absenceMailTo, 0, len(holders))
	for _, h := range holders {
		subject, body := neutralRequestMail(h.language, h.name, link)
		out = append(out, absenceMailTo{to: h.to, subject: subject, body: body, logKey: "user_id", logID: h.id.String()})
	}
	return out
}

// sendAbsenceMails sends mails one after the other under one budget of
// absenceMailTimeout; what the budget leaves unsent is logged, by count.
func (s *server) sendAbsenceMails(ctx context.Context, n absenceNotice, mails []absenceMailTo) {
	if len(mails) == 0 {
		return
	}
	logger := s.deps.Logger
	budget, cancel := context.WithTimeout(ctx, absenceMailTimeout)
	defer cancel()
	for i, m := range mails {
		if budget.Err() != nil {
			logger.WarnContext(ctx, "staff: absence mails not sent: the time for the notice ran out", "person_id", n.personID, "notice", n.kind, "unsent", len(mails)-i)
			return
		}
		err := s.smtpSend(budget, s.deps.Config.Mail, mail.Outbound{To: []string{m.to}, Subject: m.subject, TextBody: m.body})
		if err != nil {
			redact := regexp.MustCompile("(?i)" + regexp.QuoteMeta(m.to))
			logger.WarnContext(ctx, "staff: an absence mail could not be sent", m.logKey, m.logID, "notice", n.kind,
				"error", redact.ReplaceAllLiteralString(err.Error(), "<recipient>"))
		}
	}
}

// neutralRequestMail is the mail an HR-only request sends a holder of
// staff:personal-data, in language: that a request awaits their decision,
// and nothing about whose, of what or when.
func neutralRequestMail(language, recipient, link string) (string, string) {
	if language == "en" {
		body := fmt.Sprintf("Hi %s,\n\nAn absence request awaits your decision in Vantigo.\n", recipient)
		if link != "" {
			body += "\nOpen Vantigo: " + link + "\n"
		}
		return "An absence request awaits your decision", body
	}
	body := fmt.Sprintf("Hei %s,\n\nEn fraværssøknad venter på avgjørelsen din i Vantigo.\n", recipient)
	if link != "" {
		body += "\nÅpne Vantigo: " + link + "\n"
	}
	return "En fraværssøknad venter på avgjørelsen din", body
}

// mailLabel is how a mail names an absence of type t in language: the type's
// own name only when it is neither a sick kind nor HR-only (reading 15) —
// and never in a registration's notice to a manager (registered), where
// only the notification types arrive, so a doctor's visit named would tell
// the rest apart as sickness (whole-branch review).
func mailLabel(t store.StaffAbsenceType, language string, registered bool) string {
	en := language == "en"
	switch {
	case (healthKind(t.Kind) || registered) && en:
		return "an absence"
	case healthKind(t.Kind) || registered:
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
	label := mailLabel(n.typ, language, n.kind == noticeRegistered)
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
