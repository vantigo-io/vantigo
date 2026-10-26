package planning

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// TestNoticeTexts is S19's wording, in both languages, per kind: the
// project's code and name on one line, the dates and — where the person is
// planned — the load, the link to My planning when there is an origin, and
// never the note or the title.
func TestNoticeTexts(t *testing.T) {
	note, title := "Hemmelig notat", "Hemmelig tittel"
	row := storedRow(1001, 2001, "2026-10-05", "2026-10-30", statusConfirmed, 5000)
	row.Note, row.Title = &note, &title
	project := &contracts.ProjectEntry{ID: 2001, Code: "KVEM1000", Name: "Kraft-Verket\nmodernisering"}
	link := "https://vantigo.example/planning/me"

	for _, tc := range []struct {
		kind     allocationNoticeKind
		en, nb   string
		enS, nbS string
	}{
		{noticePlanned,
			"You are planned on KVEM1000 Kraft-Verket modernisering from 5 October 2026 to 30 October 2026 (50%).",
			"Du er planlagt på KVEM1000 Kraft-Verket modernisering fra 5. oktober 2026 til 30. oktober 2026 (50 %).",
			"You are planned on KVEM1000 Kraft-Verket modernisering", "Du er planlagt på KVEM1000 Kraft-Verket modernisering"},
		{noticeChanged,
			"Your planning has changed: you are planned on KVEM1000 Kraft-Verket modernisering from 5 October 2026 to 30 October 2026 (50%).",
			"Planen din er endret: du er planlagt på KVEM1000 Kraft-Verket modernisering fra 5. oktober 2026 til 30. oktober 2026 (50 %).",
			"Your planning on KVEM1000 Kraft-Verket modernisering has changed", "Planen din på KVEM1000 Kraft-Verket modernisering er endret"},
		{noticeRemoved,
			"You are no longer planned on KVEM1000 Kraft-Verket modernisering from 5 October 2026 to 30 October 2026.",
			"Du er ikke lenger planlagt på KVEM1000 Kraft-Verket modernisering fra 5. oktober 2026 til 30. oktober 2026.",
			"Your planning on KVEM1000 Kraft-Verket modernisering is removed", "Planen din på KVEM1000 Kraft-Verket modernisering er fjernet"},
		{noticeWithdrawn,
			"Your planning on KVEM1000 Kraft-Verket modernisering from 5 October 2026 to 30 October 2026 is no longer confirmed.",
			"Planen din på KVEM1000 Kraft-Verket modernisering fra 5. oktober 2026 til 30. oktober 2026 er ikke lenger bekreftet.",
			"Your planning on KVEM1000 Kraft-Verket modernisering is no longer confirmed", "Planen din på KVEM1000 Kraft-Verket modernisering er ikke lenger bekreftet"},
	} {
		texts := noticeTexts(allocationNotice{kind: tc.kind, userID: uuid.New(), row: row, project: project}, link)
		en, nb := texts["en"], texts["nb"]
		if en.Body != tc.en+"\n\nOpen My planning: "+link+"\n" || en.Subject != tc.enS {
			t.Errorf("en %d = %+v", tc.kind, en)
		}
		if nb.Body != tc.nb+"\n\nÅpne Min bemanning: "+link+"\n" || nb.Subject != tc.nbS {
			t.Errorf("nb %d = %+v", tc.kind, nb)
		}
		for _, text := range texts {
			if strings.Contains(text.Subject+text.Body, "Hemmelig") {
				t.Errorf("the note or the title leaked: %+v", text)
			}
		}
	}

	hours := row
	hours.LoadUnit, hours.Load = loadUnitHours, numericOfHundredths(2250)
	texts := noticeTexts(allocationNotice{kind: noticePlanned, row: hours}, "")
	if texts["en"].Body != "You are planned on project 2001 from 5 October 2026 to 30 October 2026 (22.5 hours a week).\n" {
		t.Errorf("en without an origin or a project = %q", texts["en"].Body)
	}
	if texts["nb"].Body != "Du er planlagt på prosjekt 2001 fra 5. oktober 2026 til 30. oktober 2026 (22,5 timer i uka).\n" {
		t.Errorf("nb without an origin or a project = %q", texts["nb"].Body)
	}
}

// TestNoticeTo is S19's rule on whom to tell: a person with a linked user,
// never one without or one Staff did not answer.
func TestNoticeTo(t *testing.T) {
	user := uuid.New()
	row := storedRow(1001, 2001, "2026-10-05", "2026-10-30", statusConfirmed, 5000)
	if n := noticeTo(noticePlanned, &contracts.PersonEntry{ID: 1001, UserID: &user}, row, nil); n == nil || n.userID != user {
		t.Errorf("a linked person = %+v, want their user", n)
	}
	if n := noticeTo(noticePlanned, &contracts.PersonEntry{ID: 1001}, row, nil); n != nil {
		t.Errorf("an unlinked person = %+v, want nobody", n)
	}
	if n := noticeTo(noticePlanned, nil, row, nil); n != nil {
		t.Errorf("a person Staff did not answer = %+v, want nobody", n)
	}
}

// TestWishNoticeTexts is S21's mail to the planners, in both languages: the
// wisher's name, the kind, the project's code and name on one line — or its
// id — and the date, the link to the Wishes page when there is an origin.
func TestWishNoticeTexts(t *testing.T) {
	project := int32(2001)
	person := &contracts.PersonEntry{ID: 1001, DisplayName: "Kari Nordmann"}
	off := store.PlanningWish{ID: 1001, PersonID: 1001, Kind: "off", ProjectID: &project, FromDate: dayParam(day("2026-10-01"))}
	entry := &contracts.ProjectEntry{ID: 2001, Code: "KVEM1000", Name: "Kraft-Verket\nmodernisering"}
	link := "https://vantigo.example/planning/wishes"

	texts := wishNoticeTexts(wishNotice{person: person, wish: off, project: entry}, link)
	if en := texts["en"]; en.Subject != "Kari Nordmann wishes off KVEM1000 Kraft-Verket modernisering from 1 October 2026" ||
		en.Body != en.Subject+".\n\nOpen the wishes: "+link+"\n" {
		t.Errorf("en = %+v", en)
	}
	if nb := texts["nb"]; nb.Subject != "Kari Nordmann ønsker seg av KVEM1000 Kraft-Verket modernisering fra 1. oktober 2026" ||
		nb.Body != nb.Subject+".\n\nÅpne ønskene: "+link+"\n" {
		t.Errorf("nb = %+v", nb)
	}

	texts = wishNoticeTexts(wishNotice{person: person, wish: off}, "")
	if en := texts["en"]; en.Body != "Kari Nordmann wishes off project 2001 from 1 October 2026.\n" {
		t.Errorf("en without the project or an origin = %q", en.Body)
	}
	fresh := off
	fresh.Kind, fresh.ProjectID = "new", nil
	texts = wishNoticeTexts(wishNotice{person: person, wish: fresh}, "")
	if texts["en"].Subject != "Kari Nordmann wishes for something new from 1 October 2026" ||
		texts["nb"].Subject != "Kari Nordmann ønsker noe nytt fra 1. oktober 2026" {
		t.Errorf("a new wish = %+v", texts)
	}
}

// TestWishRecipients is reading 6: every holder once, by id, but the caller
// and the user the wisher's person is linked to.
func TestWishRecipients(t *testing.T) {
	caller, linked, a, b := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	holders := []contracts.Holder{{ID: a}, {ID: caller}, {ID: b}, {ID: a}, {ID: linked}}
	got := wishRecipients(holders, caller, &contracts.PersonEntry{ID: 1001, UserID: &linked})
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("recipients = %v, want %v and %v once each", got, a, b)
	}
	if got := wishRecipients(nil, caller, &contracts.PersonEntry{ID: 1001}); len(got) != 0 {
		t.Errorf("no holders = %v", got)
	}
}
