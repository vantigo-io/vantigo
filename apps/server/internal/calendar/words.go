package calendar

import (
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is what the module writes to people in their language (calendar
// invitations design D3, D4): the invitation mail in the organiser's, and the
// colleague notices in both — identity picks the reader's. English and
// Norwegian bokmål, as the app.

// The two languages.
const (
	langEnglish   = "en"
	langNorwegian = "nb"
)

// languageOf is the language a preference writes in: Norwegian for nb,
// English for anything else, none included (D3).
func languageOf(preference string) string {
	if preference == langNorwegian {
		return langNorwegian
	}
	return langEnglish
}

// words is one language's sentences. A %s in one is the organiser's name,
// or for the notices the sentence's parts in order.
type words struct {
	lang string

	weekdays [7]string // Sunday first, time.Weekday's order
	months   [12]string

	// The invitation mail.
	invitation, updated, cancelled            string // the subject's prefix
	invites, changed, cancelledIt, removedYou string // the opening line
	when, where, organiser                    string
	repeating, theOrganiser                   string
	rsvpSentence, behalf                      string

	// The colleague notices: who, the title, when; and the owner's notice of
	// an answer: who, the title.
	noticeInvited, noticeChanged, noticeCancelled, noticeRemoved string
	answerAccepted, answerDeclined, answerTentative              string
	anInvitee, aContact, openInVantigo                           string

	// untitled is a subscribed event's title when it has no SUMMARY
	// (calendar feed design D8).
	untitled string
}

var english = words{
	lang:     langEnglish,
	weekdays: [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
	months:   [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},

	invitation: "Invitation", updated: "Updated", cancelled: "Cancelled",
	invites:     "%s invites you to an appointment.",
	changed:     "%s has changed an appointment you are invited to.",
	cancelledIt: "%s has cancelled this appointment.",
	removedYou:  "%s has removed you from this appointment.",
	when:        "When", where: "Where", organiser: "Organiser",
	repeating:    "repeating",
	theOrganiser: "The organiser",
	rsvpSentence: "Answer with the link below so the organiser sees it in Vantigo:",
	behalf:       "Vantigo sends this on behalf of %s. A reply to this mail is not read.",

	noticeInvited:   "%s invited you: %s, %s",
	noticeChanged:   "%s changed: %s, %s",
	noticeCancelled: "%s cancelled: %s, %s",
	noticeRemoved:   "%s removed you: %s, %s",
	answerAccepted:  "%s accepted: %s",
	answerDeclined:  "%s declined: %s",
	answerTentative: "%s tentatively accepted: %s",
	anInvitee:       "An invitee",
	aContact:        "A contact",
	openInVantigo:   "Open it in Vantigo:",

	untitled: "(untitled)",
}

var norwegian = words{
	lang:     langNorwegian,
	weekdays: [7]string{"søn.", "man.", "tir.", "ons.", "tor.", "fre.", "lør."},
	months:   [12]string{"jan.", "feb.", "mar.", "apr.", "mai", "jun.", "jul.", "aug.", "sep.", "okt.", "nov.", "des."},

	invitation: "Invitasjon", updated: "Oppdatert", cancelled: "Avlyst",
	invites:     "%s inviterer deg til en avtale.",
	changed:     "%s har endret en avtale du er invitert til.",
	cancelledIt: "%s har avlyst denne avtalen.",
	removedYou:  "%s har fjernet deg fra denne avtalen.",
	when:        "Når", where: "Hvor", organiser: "Arrangør",
	repeating:    "gjentas",
	theOrganiser: "Arrangøren",
	rsvpSentence: "Svar med lenken nedenfor, så ser arrangøren svaret i Vantigo:",
	behalf:       "Vantigo sender dette på vegne av %s. Svar på denne e-posten blir ikke lest.",

	noticeInvited:   "%s inviterte deg: %s, %s",
	noticeChanged:   "%s endret: %s, %s",
	noticeCancelled: "%s avlyste: %s, %s",
	noticeRemoved:   "%s fjernet deg: %s, %s",
	answerAccepted:  "%s takket ja: %s",
	answerDeclined:  "%s takket nei: %s",
	answerTentative: "%s svarte kanskje: %s",
	anInvitee:       "En invitert",
	aContact:        "En kontakt",
	openInVantigo:   "Åpne den i Vantigo:",

	untitled: "(uten tittel)",
}

// wordsFor is lang's sentences, English for anything but nb.
func wordsFor(lang string) words {
	if lang == langNorwegian {
		return norwegian
	}
	return english
}

// day is a date as w writes it: "Tue 3 Nov", "tir. 3. nov.".
func (w words) day(d time.Time) string {
	if w.lang == langNorwegian {
		return fmt.Sprintf("%s %d. %s", w.weekdays[d.Weekday()], d.Day(), w.months[d.Month()-1])
	}
	return fmt.Sprintf("%s %d %s", w.weekdays[d.Weekday()], d.Day(), w.months[d.Month()-1])
}

// zoneOf is the appointment's zone, UTC if it could not be loaded.
func zoneOf(a store.CalendarAppointment) *time.Location {
	if loc, err := time.LoadLocation(a.TimeZone); err == nil {
		return loc
	}
	return time.UTC
}

// startDay is the day the appointment starts on, in its zone.
func startDay(a store.CalendarAppointment) time.Time {
	if a.AllDay {
		d, _ := fromPgDate(a.StartDate)
		return d
	}
	if a.StartsAt == nil {
		return time.Time{}
	}
	return civil(floating(*a.StartsAt, zoneOf(a)))
}

// dayOf is the day the appointment starts on, as the subject names it.
func dayOf(w words, a store.CalendarAppointment) string { return w.day(startDay(a)) }

// whenOf is the appointment's time in its zone, its first occurrence's for a
// series: "Tue 3 Nov 2026, 09:00–10:00", a timed one ending on another day
// with both ends written whole, an all-day one by its days.
func whenOf(w words, a store.CalendarAppointment) string {
	if a.AllDay {
		first, _ := fromPgDate(a.StartDate)
		last, _ := fromPgDate(a.EndDate)
		if first.Equal(last) {
			return fmt.Sprintf("%s %d", w.day(first), first.Year())
		}
		return fmt.Sprintf("%s – %s %d", w.day(first), w.day(last), last.Year())
	}
	if a.StartsAt == nil || a.EndsAt == nil {
		return ""
	}
	loc := zoneOf(a)
	start, end := a.StartsAt.In(loc), a.EndsAt.In(loc)
	if civil(floating(start, loc)).Equal(civil(floating(end, loc))) {
		return fmt.Sprintf("%s %d, %s–%s", w.day(start), start.Year(), start.Format("15:04"), end.Format("15:04"))
	}
	return fmt.Sprintf("%s %d %s – %s %d %s", w.day(start), start.Year(), start.Format("15:04"), w.day(end), end.Year(), end.Format("15:04"))
}

// noticeWhenOf is the appointment's start as a notice names it: "Tue 3 Nov
// 09:00", or the day alone for an all-day one.
func noticeWhenOf(w words, a store.CalendarAppointment) string {
	if a.AllDay || a.StartsAt == nil {
		return dayOf(w, a)
	}
	return dayOf(w, a) + " " + a.StartsAt.In(zoneOf(a)).Format("15:04")
}
