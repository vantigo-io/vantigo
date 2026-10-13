package staff_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// A fresh installation's self-certification policy is the statutory minimum
// — 3 days an occurrence, 4 occurrences a year, after 2 months — and
// automatic carry-over is off, never turned on.
func TestSettings_TheAbsencePolicyDefaults(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	r := hr.Do(http.MethodGet, settingsPath, nil)
	var s gen.StaffSettings
	r.JSON(&s)
	if s.SelfCertPolicy != gen.Minimum || s.SelfCertDaysPerOccurrence != 3 || s.SelfCertOccurrencesPerYear != 4 ||
		s.SelfCertDaysPerYear != 24 || s.SelfCertAfterMonths != 2 || s.AutoCarryOver {
		t.Errorf("settings = %+v, want minimum 3/4/24 after 2 months, no carry-over", s)
	}
	if _, ok := raw(t, r)["autoCarryOverSince"]; ok {
		t.Error("autoCarryOverSince is present before carry-over was ever turned on")
	}
}

// A preset writes its own numbers whatever the request carries — minimum 3
// days and 4 occurrences, extended 8 days and 24 days a year — and custom
// takes them as sent, within their bounds; a number left out keeps its
// value, as a body that names none of them keeps the policy as it is.
func TestSettings_TheSelfCertificationPolicy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	put := func(fields map[string]any) gen.StaffSettings {
		t.Helper()
		var out gen.StaffSettings
		do(t, hr, http.MethodPut, settingsPath, settingsBody(fields), http.StatusOK, &out, ifMatch(getSettings(t, hr).Revision))
		return out
	}

	s := put(map[string]any{"selfCertPolicy": "extended", "selfCertDaysPerOccurrence": 2, "selfCertDaysPerYear": 5})
	if s.SelfCertPolicy != gen.Extended || s.SelfCertDaysPerOccurrence != 8 || s.SelfCertDaysPerYear != 24 {
		t.Errorf("extended = %+v, want 8 days an occurrence and 24 a year whatever was sent", s)
	}
	s = put(map[string]any{"selfCertPolicy": "custom", "selfCertDaysPerOccurrence": 5, "selfCertOccurrencesPerYear": 6,
		"selfCertDaysPerYear": 30, "selfCertAfterMonths": 0})
	if s.SelfCertPolicy != gen.Custom || s.SelfCertDaysPerOccurrence != 5 || s.SelfCertOccurrencesPerYear != 6 ||
		s.SelfCertDaysPerYear != 30 || s.SelfCertAfterMonths != 0 {
		t.Errorf("custom = %+v, want 5/6/30 after 0 months, as sent", s)
	}
	s = put(nil)
	if s.SelfCertPolicy != gen.Custom || s.SelfCertDaysPerOccurrence != 5 || s.SelfCertAfterMonths != 0 {
		t.Errorf("a body naming no policy field = %+v, want the custom policy kept", s)
	}
	s = put(map[string]any{"selfCertPolicy": "minimum"})
	if s.SelfCertPolicy != gen.Minimum || s.SelfCertDaysPerOccurrence != 3 || s.SelfCertOccurrencesPerYear != 4 ||
		s.SelfCertAfterMonths != 0 {
		t.Errorf("minimum = %+v, want 3 days and 4 occurrences, the months kept", s)
	}

	for _, tc := range []struct {
		field string
		value any
	}{
		{"selfCertDaysPerOccurrence", 0}, {"selfCertDaysPerOccurrence", 17},
		{"selfCertOccurrencesPerYear", 0}, {"selfCertOccurrencesPerYear", 53},
		{"selfCertDaysPerYear", 0}, {"selfCertDaysPerYear", 366},
		{"selfCertAfterMonths", -1}, {"selfCertAfterMonths", 3},
	} {
		p := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"selfCertPolicy": "custom", tc.field: tc.value}),
			ifMatch(getSettings(t, hr).Revision)), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s %v: errors = %v, want it named", tc.field, tc.value, p.Errors)
		}
	}
	if r := hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"selfCertPolicy": "generous"}), ifMatch(getSettings(t, hr).Revision)); r.Status != http.StatusBadRequest {
		t.Errorf("an unknown policy: status %d, want 400", r.Status)
	}
}

// Turning automatic carry-over on records today as autoCarryOverSince
// (pre-flight M6); turning it off keeps the day, and turning it on again
// moves it to the new today. A body that leaves it out keeps it.
func TestSettings_AutoCarryOverSince(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	put := func(fields map[string]any) gen.StaffSettings {
		t.Helper()
		var out gen.StaffSettings
		do(t, hr, http.MethodPut, settingsPath, settingsBody(fields), http.StatusOK, &out, ifMatch(getSettings(t, hr).Revision))
		return out
	}
	since := func(s gen.StaffSettings) string {
		if s.AutoCarryOverSince == nil {
			return ""
		}
		return s.AutoCarryOverSince.Format(time.DateOnly)
	}

	first := today(t, h)
	if s := put(map[string]any{"autoCarryOver": true}); !s.AutoCarryOver || since(s) != first {
		t.Errorf("turned on = %v since %q, want on since %s", s.AutoCarryOver, since(s), first)
	}
	setClock(t, h, h.Now().Add(72*time.Hour))
	hr, _ = signIn(t, h, "staff:manage") // the session did not outlive the three days
	if s := put(nil); !s.AutoCarryOver || since(s) != first {
		t.Errorf("left out = %v since %q, want on since %s still", s.AutoCarryOver, since(s), first)
	}
	if s := put(map[string]any{"autoCarryOver": true}); since(s) != first {
		t.Errorf("on again while on: since %q, want %s — it was not turned on today", since(s), first)
	}
	if s := put(map[string]any{"autoCarryOver": false}); s.AutoCarryOver || since(s) != first {
		t.Errorf("turned off = %v since %q, want off, the day kept", s.AutoCarryOver, since(s))
	}
	second := today(t, h)
	if s := put(map[string]any{"autoCarryOver": true}); since(s) != second {
		t.Errorf("turned on again: since %q, want %s", since(s), second)
	}
}

// The care-day fields (design S11, §5's care-day row) are written by HR on
// the person and read by the person, staff:manage and staff:personal-data —
// never by a colleague or a manager; a change is a person_updated event of
// class D, which a manager reads as "changed". PUT /me does not take them: a
// person declares them to HR, who types them.
func TestPeople_TheCareDayFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	manager := createPerson(t, hr, newPerson("Mona", "Manager"))
	createEmployment(t, hr, manager.Id, newEmployment("2020-01-01", terms()))
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "childrenUnder12", 2, "soleCarer", true, "chronicallyIllChildren", 1))
	if p.ChildrenUnder12 == nil || *p.ChildrenUnder12 != 2 || p.SoleCarer == nil || !*p.SoleCarer || p.ChronicallyIllChildren == nil || *p.ChronicallyIllChildren != 1 {
		t.Errorf("created = %+v, want 2 children under 12, a sole carer, 1 chronically ill", p)
	}
	createEmployment(t, hr, p.Id, newEmployment("2020-01-01", terms("managerPersonId", manager.Id)))

	var changed gen.StaffPerson
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Nordmann"), "childrenUnder12", 3, "soleCarer", false),
		http.StatusOK, &changed, ifMatch(personRevision(t, h, p.Id)))
	if changed.ChildrenUnder12 == nil || *changed.ChildrenUnder12 != 3 || changed.SoleCarer == nil || *changed.SoleCarer || changed.ChronicallyIllChildren != nil {
		t.Errorf("changed = %+v, want 3, not a sole carer, and the chronically ill children cleared as left out", changed)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'person_updated'
		AND payload->'fields' ?& array['childrenUnder12', 'soleCarer', 'chronicallyIllChildren'] AND payload_classes = '{D}'`, p.Id); n != 1 {
		t.Errorf("person_updated events naming the three care-day fields, class D = %d, want 1", n)
	}

	self, selfUser := signInNamed(t, h, "Kari the user")
	linkThroughAPI(t, hr, p.Id, selfUser)
	boss, bossUser := signInNamed(t, h, "Mona the user")
	linkThroughAPI(t, hr, manager.Id, bossUser)
	colleague, _ := signIn(t, h, "staff:view")
	personalData, _ := signIn(t, h, "staff:personal-data")
	for _, c := range []struct {
		name  string
		reads bool
		get   func() map[string]any
	}{
		{"the person", true, func() map[string]any { return raw(t, self.Do(http.MethodGet, personPath(p.Id), nil)) }},
		{"staff:manage", true, func() map[string]any { return raw(t, hr.Do(http.MethodGet, personPath(p.Id), nil)) }},
		{"staff:personal-data", true, func() map[string]any { return raw(t, personalData.Do(http.MethodGet, personPath(p.Id), nil)) }},
		{"the manager", false, func() map[string]any { return raw(t, boss.Do(http.MethodGet, personPath(p.Id), nil)) }},
		{"a colleague", false, func() map[string]any { return raw(t, colleague.Do(http.MethodGet, personPath(p.Id), nil)) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := c.get()
			_, children := got["childrenUnder12"]
			_, sole := got["soleCarer"]
			if children != c.reads || sole != c.reads {
				t.Errorf("childrenUnder12 present %v, soleCarer present %v, want %v", children, sole, c.reads)
			}
		})
	}

	var events gen.PaginatedResponseOfStaffEvent
	do(t, boss, http.MethodGet, eventsPath(p.Id), nil, http.StatusOK, &events)
	for _, e := range events.Data {
		if e.Kind != "person_updated" {
			continue
		}
		fields, _ := e.Payload["fields"].(map[string]any)
		if v, ok := fields["childrenUnder12"]; ok && v != "changed" {
			t.Errorf("the manager reads childrenUnder12 in the history as %v, want \"changed\"", v)
		}
	}

	me := self.Do(http.MethodGet, mePath, nil)
	var mine gen.StaffPerson
	me.JSON(&mine)
	for _, property := range []string{"childrenUnder12", "soleCarer", "chronicallyIllChildren"} {
		pr := refused(t, self.Do(http.MethodPut, mePath, map[string]any{"nextOfKin": []any{}, property: 1}, ifMatch(mine.Revision)), http.StatusBadRequest, "")
		if len(pr.Errors[property]) == 0 {
			t.Errorf("PUT /me with %s: errors = %v, want it refused by name", property, pr.Errors)
		}
	}

	for _, tc := range []struct {
		field string
		value any
	}{{"childrenUnder12", -1}, {"childrenUnder12", 21}, {"chronicallyIllChildren", -1}, {"chronicallyIllChildren", 21}} {
		pr := refused(t, hr.Do(http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Nordmann"), tc.field, tc.value),
			ifMatch(personRevision(t, h, p.Id))), http.StatusBadRequest, "")
		if len(pr.Errors[tc.field]) == 0 {
			t.Errorf("%s %v: errors = %v, want it named", tc.field, tc.value, pr.Errors)
		}
	}
}

// GET /meta answers the absence types every caller may pick — the active
// ones, names in both languages, the hidden ones never — the holiday schemes,
// and the two capabilities delivery B adds: canDecide (staff:approve,
// staff:manage, or managing anyone today) and managesAnyone (the caller's
// person manages anyone today by the one manager rule, so a manager who has
// left manages nobody).
func TestMeta_AbsenceTypesSchemesAndTheDecider(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)

	plain, _ := signIn(t, h)
	meta := getMeta(t, plain)
	var keys []string
	for _, a := range meta.AbsenceTypes {
		keys = append(keys, a.Key)
		if a.Hidden {
			t.Errorf("meta answers the hidden type %s", a.Key)
		}
	}
	if len(keys) != 22 || keys[0] != "ferie" || slices.Contains(keys, "absent") {
		t.Errorf("meta's types = %v, want the twenty-two visible seeds from ferie", keys)
	}
	if len(meta.AbsenceTypes) == 0 {
		t.Fatal("meta answers no absence types")
	}
	if ferie := meta.AbsenceTypes[0]; ferie.Names.Nb != "Ferie" || ferie.Unit != gen.StaffAbsenceUnitDays || !ferie.DrawsHoliday {
		t.Errorf("meta's ferie = %+v", ferie)
	}
	if len(meta.HolidaySchemes) != 1 || !meta.HolidaySchemes[0].IsDefault || meta.HolidaySchemes[0].Converted.Entitlement != 21 {
		t.Errorf("meta's schemes = %+v, want the statutory default, 21 days", meta.HolidaySchemes)
	}
	if meta.Capabilities.CanDecide || meta.Capabilities.ManagesAnyone {
		t.Errorf("staff:access alone = %+v, want neither canDecide nor managesAnyone", meta.Capabilities)
	}

	boss := createPerson(t, hr, newPerson("Mona", "Manager"))
	bossEmployment := createEmployment(t, hr, boss.Id, newEmployment("2020-01-01", terms()))
	report := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	createEmployment(t, hr, report.Id, newEmployment("2020-01-01", terms("managerPersonId", boss.Id)))
	bossClient, bossUser := signInNamed(t, h, "Mona the user")
	linkThroughAPI(t, hr, boss.Id, bossUser)
	reportClient, reportUser := signInNamed(t, h, "Kari the user")
	linkThroughAPI(t, hr, report.Id, reportUser)

	if c := getMeta(t, bossClient).Capabilities; !c.CanDecide || !c.ManagesAnyone {
		t.Errorf("the manager = %+v, want canDecide and managesAnyone", c)
	}
	if c := getMeta(t, reportClient).Capabilities; c.CanDecide || c.ManagesAnyone {
		t.Errorf("the report = %+v, want neither", c)
	}

	do(t, hr, http.MethodPost, endPath(bossEmployment.Id), map[string]any{"endDate": "2021-06-30", "endReason": "employee_resigned"}, http.StatusOK, nil)
	if c := getMeta(t, bossClient).Capabilities; c.CanDecide || c.ManagesAnyone {
		t.Errorf("a manager who has left = %+v, want neither: they manage nobody", c)
	}
}
