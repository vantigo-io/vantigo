package planning

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the person's mail (design S19, amendment 5): a plain notice
// composed here in English and Norwegian and delivered by identity in the
// person's own language through UserDirectory.Notify, to the user Staff's
// link names (PersonEntry.UserID), so this module never holds an address.
// The person is told when a row of theirs becomes confirmed — created
// confirmed, set confirmed by a PUT, or confirmed — when a confirmed row's
// period, load or project changes, when a confirmed row is deleted, and when
// a confirmed row is set back to tentative. A tentative row is the planners'
// draft and is never mailed about. The notice names the project's code and
// name, the dates and the load, and links to My planning — never the note,
// which is the planners' (S5), nor the title.
//
// Every notice goes after the commit, outside every transaction and lock,
// on a context the request's end does not cancel; a notice that fails is
// logged by the allocation's and the user's ids and never fails the write. A
// person with no linked user is never mailed, and that is not an error.
//
// A new wish (S21) is mailed the same way to the planners — every
// planning:manage holder, once, but the wisher (reading 6) — naming the
// person as Staff answered them to themself, the kind, the project and the
// date, and linking to the planners' Wishes page.

// allocationNoticeKind is what a notice tells the person.
type allocationNoticeKind int

const (
	noticePlanned allocationNoticeKind = iota
	noticeChanged
	noticeRemoved
	noticeWithdrawn
)

// noticeTimeout bounds one write's notices, all of them together.
const noticeTimeout = 30 * time.Second

// myPlanningPage is the page a notice links to.
const myPlanningPage = "/planning/me"

// wishesPage is the page a wish's notice to the planners links to.
const wishesPage = "/planning/wishes"

// allocationNotice is one notice to send after the commit: whom, what, and
// the row as the notice describes it — the row as written, or as it stood
// before a deletion.
type allocationNotice struct {
	kind    allocationNoticeKind
	userID  uuid.UUID
	row     store.PlanningAllocation
	project *contracts.ProjectEntry
}

// noticeTo is kind's notice about row to person, nil when there is nobody to
// tell: a person Staff did not answer, or one with no linked user.
func noticeTo(kind allocationNoticeKind, person *contracts.PersonEntry, row store.PlanningAllocation, project *contracts.ProjectEntry) *allocationNotice {
	if person == nil || person.UserID == nil {
		return nil
	}
	return &allocationNotice{kind: kind, userID: *person.UserID, row: row, project: project}
}

// sendNotices sends notices one after the other, after the write committed,
// under one budget of noticeTimeout; a failure is logged and goes no
// further. A nil notice is skipped.
func (s *server) sendNotices(ctx context.Context, notices ...*allocationNotice) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), noticeTimeout)
	defer cancel()
	link := s.appLink(myPlanningPage)
	for _, n := range notices {
		if n == nil {
			continue
		}
		if err := s.usersNotify(ctx, n.userID, contracts.Notice{Texts: noticeTexts(*n, link)}); err != nil {
			s.deps.Logger.WarnContext(ctx, "planning: a notice could not be sent",
				"allocation_id", n.row.ID, "user_id", n.userID.String(), "error", err.Error())
		}
	}
}

// appLink is page in the app, "" when the installation has no origin.
func (s *server) appLink(page string) string {
	if cfg := s.deps.Config; cfg != nil && cfg.AppOrigin != "" {
		return cfg.AppOrigin + cfg.BasePath + page
	}
	return ""
}

// noticeWords is one language's wording of the notices.
type noticeWords struct {
	lang                                 string
	plannedSubject, plannedBody          string
	changedSubject, changedBody          string
	removedSubject, removedBody          string
	withdrawnSubject, withdrawnBody      string
	openMyPlanning, project              string
	wishOff, wishNew, openWishes         string
	months                               [12]string
	dateFormat                           func(day int, month string, year int) string
	percent, hoursPerWeek, decimalSymbol string
}

// The notices' wording. Each subject takes the project; each body the
// project, the period and, where the person is planned, the load.
var (
	noticeEnglish = noticeWords{
		lang:             "en",
		plannedSubject:   "You are planned on %s",
		plannedBody:      "You are planned on %s from %s to %s (%s).",
		changedSubject:   "Your planning on %s has changed",
		changedBody:      "Your planning has changed: you are planned on %s from %s to %s (%s).",
		removedSubject:   "Your planning on %s is removed",
		removedBody:      "You are no longer planned on %s from %s to %s.",
		withdrawnSubject: "Your planning on %s is no longer confirmed",
		withdrawnBody:    "Your planning on %s from %s to %s is no longer confirmed.",
		openMyPlanning:   "Open My planning:",
		project:          "project %d",
		wishOff:          "%s wishes off %s from %s",
		wishNew:          "%s wishes for something new from %s",
		openWishes:       "Open the wishes:",
		months: [12]string{"January", "February", "March", "April", "May", "June", "July", "August",
			"September", "October", "November", "December"},
		dateFormat:    func(day int, month string, year int) string { return fmt.Sprintf("%d %s %d", day, month, year) },
		percent:       "%s%%",
		hoursPerWeek:  "%s hours a week",
		decimalSymbol: ".",
	}
	noticeNorwegian = noticeWords{
		lang:             "nb",
		plannedSubject:   "Du er planlagt på %s",
		plannedBody:      "Du er planlagt på %s fra %s til %s (%s).",
		changedSubject:   "Planen din på %s er endret",
		changedBody:      "Planen din er endret: du er planlagt på %s fra %s til %s (%s).",
		removedSubject:   "Planen din på %s er fjernet",
		removedBody:      "Du er ikke lenger planlagt på %s fra %s til %s.",
		withdrawnSubject: "Planen din på %s er ikke lenger bekreftet",
		withdrawnBody:    "Planen din på %s fra %s til %s er ikke lenger bekreftet.",
		openMyPlanning:   "Åpne Min bemanning:",
		project:          "prosjekt %d",
		wishOff:          "%s ønsker seg av %s fra %s",
		wishNew:          "%s ønsker noe nytt fra %s",
		openWishes:       "Åpne ønskene:",
		months: [12]string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august",
			"september", "oktober", "november", "desember"},
		dateFormat:    func(day int, month string, year int) string { return fmt.Sprintf("%d. %s %d", day, month, year) },
		percent:       "%s %%",
		hoursPerWeek:  "%s timer i uka",
		decimalSymbol: ",",
	}
)

// noticeTexts is n in both languages, the body ending in the link to My
// planning when the installation has an origin.
func noticeTexts(n allocationNotice, link string) map[string]contracts.NoticeText {
	texts := map[string]contracts.NoticeText{}
	for _, w := range []noticeWords{noticeEnglish, noticeNorwegian} {
		project := w.projectName(n)
		from, to := w.date(civil(n.row.FromDate)), w.date(civil(n.row.ToDate))
		var subject, sentence string
		switch n.kind {
		case noticePlanned:
			subject, sentence = fmt.Sprintf(w.plannedSubject, project), fmt.Sprintf(w.plannedBody, project, from, to, w.load(n.row))
		case noticeChanged:
			subject, sentence = fmt.Sprintf(w.changedSubject, project), fmt.Sprintf(w.changedBody, project, from, to, w.load(n.row))
		case noticeRemoved:
			subject, sentence = fmt.Sprintf(w.removedSubject, project), fmt.Sprintf(w.removedBody, project, from, to)
		case noticeWithdrawn:
			subject, sentence = fmt.Sprintf(w.withdrawnSubject, project), fmt.Sprintf(w.withdrawnBody, project, from, to)
		}
		body := sentence + "\n"
		if link != "" {
			body += "\n" + w.openMyPlanning + " " + link + "\n"
		}
		texts[w.lang] = contracts.NoticeText{Subject: subject, Body: body}
	}
	return texts
}

// projectName is the project as a notice names it: its code and name on one
// line, or its id when the directory did not resolve it (rule 4).
func (w noticeWords) projectName(n allocationNotice) string {
	return w.projectNamed(n.row.ProjectID, n.project)
}

// projectNamed is project id as the language names it: the entry's code and
// name on one line, or the id when the directory did not resolve it.
func (w noticeWords) projectNamed(id int32, project *contracts.ProjectEntry) string {
	if project == nil {
		return fmt.Sprintf(w.project, id)
	}
	return oneLine(project.Code + " " + project.Name)
}

// date is a civil date as the language writes it.
func (w noticeWords) date(d time.Time) string {
	return w.dateFormat(d.Day(), w.months[d.Month()-1], d.Year())
}

// load is a row's load as the language writes it: "50%" or "20 hours a
// week", the decimals as typed.
func (w noticeWords) load(a store.PlanningAllocation) string {
	v := strings.Replace(strconv.FormatFloat(loadOf(a.Load), 'f', -1, 64), ".", w.decimalSymbol, 1)
	if a.LoadUnit == loadUnitHours {
		return fmt.Sprintf(w.hoursPerWeek, v)
	}
	return fmt.Sprintf(w.percent, v)
}

// oneLine is s with every run of white space, line breaks included, as one
// space: a project's name never breaks a subject.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// wishNotice is a new wish as the planners are told of it: the wisher as
// Staff's Self answered them, the wish as written and its project, nil for
// a wish for something new.
type wishNotice struct {
	person  *contracts.PersonEntry
	wish    store.PlanningWish
	project *contracts.ProjectEntry
}

// sendWishNotices tells each of recipients of n, after the wish committed,
// under one budget of noticeTimeout; a failure is logged by the wish's and
// the user's ids and goes no further.
func (s *server) sendWishNotices(ctx context.Context, recipients []uuid.UUID, n wishNotice) {
	if len(recipients) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), noticeTimeout)
	defer cancel()
	texts := wishNoticeTexts(n, s.appLink(wishesPage))
	for _, user := range recipients {
		if err := s.usersNotify(ctx, user, contracts.Notice{Texts: texts}); err != nil {
			s.deps.Logger.WarnContext(ctx, "planning: a wish's notice could not be sent",
				"wish_id", n.wish.ID, "user_id", user.String(), "error", err.Error())
		}
	}
}

// wishNoticeTexts is n in both languages: "NAME wishes off CODE NAME from
// DATE" or "NAME wishes for something new from DATE" as the subject and the
// sentence, the body ending in the link to the Wishes page when the
// installation has an origin.
func wishNoticeTexts(n wishNotice, link string) map[string]contracts.NoticeText {
	texts := map[string]contracts.NoticeText{}
	for _, w := range []noticeWords{noticeEnglish, noticeNorwegian} {
		name, from := oneLine(n.person.DisplayName), w.date(civil(n.wish.FromDate))
		sentence := fmt.Sprintf(w.wishNew, name, from)
		if n.wish.ProjectID != nil {
			sentence = fmt.Sprintf(w.wishOff, name, w.projectNamed(*n.wish.ProjectID, n.project), from)
		}
		body := sentence + ".\n"
		if link != "" {
			body += "\n" + w.openWishes + " " + link + "\n"
		}
		texts[w.lang] = contracts.NoticeText{Subject: sentence, Body: body}
	}
	return texts
}
