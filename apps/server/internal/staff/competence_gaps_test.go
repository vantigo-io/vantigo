package staff_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The paths of what a person's profiles ask.
func personProfileListPath(personID int64) string {
	return personPath(personID) + "/competence-profiles"
}
func competenceGapsPath(personID int64) string { return personPath(personID) + "/competence-gaps" }

// gapItemFor is one item of a profile written directly: the competence, the
// minimum level (nil: none) and whether the row must be valid.
type gapItemFor struct {
	competence  int64
	minLevel    *int16
	mustBeValid bool
}

// rawProfile writes a profile with items directly — the catalogue's
// endpoints are another lane's, and the gaps read the tables alone — and
// answers its id.
func rawProfile(t *testing.T, h *harness, key string, archived bool, items ...gapItemFor) int64 {
	t.Helper()
	id := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.competence_profiles (key, name_en, name_nb, archived_at, created_at, updated_at)
		VALUES ($1::text, 'Profile ' || $1::text, 'Profil ' || $1::text, CASE WHEN $2::boolean THEN $3::timestamptz END, $3, $3) RETURNING id`,
		key, archived, h.Now())
	for _, it := range items {
		h.Exec(t, `
			INSERT INTO staff.competence_profile_items (profile_id, competence_id, min_level, must_be_valid, created_at)
			VALUES ($1, $2, $3, $4, $5)`, id, it.competence, it.minLevel, it.mustBeValid, h.Now())
	}
	return id
}

// rawAssign assigns profileID to personID directly.
func rawAssign(t *testing.T, h *harness, personID, profileID int64) {
	t.Helper()
	h.Exec(t, `INSERT INTO staff.person_profiles (person_id, profile_id, created_at) VALUES ($1, $2, $3)`, personID, profileID, h.Now())
}

// rawDepartmentDefault adds a department code whose default is profileID and
// names it in every terms row of personID's, and answers its id.
func rawDepartmentDefault(t *testing.T, h *harness, code string, personID, profileID int64) int64 {
	t.Helper()
	id := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.departments (code, name, competence_profile_id, created_at, updated_at)
		VALUES ($1::text, 'Department ' || $1::text, $2, $3, $3) RETURNING id`, code, profileID, h.Now())
	h.Exec(t, `
		UPDATE staff.employment_terms SET department_id = $2
		WHERE employment_id IN (SELECT id FROM staff.employments WHERE person_id = $1)`, personID, id)
	return id
}

// getGaps reads personID's gap list as c and fails the test unless it
// answered 200.
func getGaps(t *testing.T, c *modtest.Client, personID int64) gen.StaffCompetenceGaps {
	t.Helper()
	var out gen.StaffCompetenceGaps
	do(t, c, http.MethodGet, competenceGapsPath(personID), nil, http.StatusOK, &out)
	return out
}

// getPersonProfiles reads the profiles that reach personID as c and fails
// the test unless it answered 200.
func getPersonProfiles(t *testing.T, c *modtest.Client, personID int64) []gen.StaffPersonProfile {
	t.Helper()
	var out gen.StaffPersonProfileList
	do(t, c, http.MethodGet, personProfileListPath(personID), nil, http.StatusOK, &out)
	return out.Profiles
}

func int16Ptr(n int16) *int16 { return &n }

// gapItemOf is the item on competenceID of the gap list's profile keyed key.
func gapItemOf(t *testing.T, gaps gen.StaffCompetenceGaps, key string, competenceID int64) gen.StaffCompetenceGapItem {
	t.Helper()
	for _, p := range gaps.Profiles {
		if p.Profile.Key != key {
			continue
		}
		for _, it := range p.Items {
			if it.CompetenceId == competenceID {
				return it
			}
		}
	}
	t.Fatalf("no item on %d in %s: %+v", competenceID, key, gaps)
	return gen.StaffCompetenceGapItem{}
}

// What the profiles ask (staff competence design S12, S13; plan readings
// 23–26): an assigned profile and the default of the department of Kari's
// place, each saying how it reaches her, judged item by item on her current
// rows; an archived profile is listed but asks nothing; an archived
// competence's item stays, reported and counted; the gap list follows her
// rows as they change.
func TestCompetenceGaps_WhatTheProfilesAsk(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	cobol := addCompetence(t, w.hr, skillBody(t, h, "cobol")).Id

	electrician := rawProfile(t, h, "electrician", false,
		gapItemFor{competence: w.fse, mustBeValid: true}, gapItemFor{competence: w.golang, minLevel: int16Ptr(3), mustBeValid: true},
		gapItemFor{competence: cobol, mustBeValid: true})
	operator := rawProfile(t, h, "operator", false, gapItemFor{competence: w.golang, minLevel: int16Ptr(2), mustBeValid: true})
	retired := rawProfile(t, h, "retired", true, gapItemFor{competence: w.fse, mustBeValid: true})
	rawAssign(t, h, w.kari.Id, electrician)
	rawAssign(t, h, w.kari.Id, retired)
	ops := rawDepartmentDefault(t, h, "OPS", w.kari.Id, operator)
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), cobol)

	declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 2})

	profiles := getPersonProfiles(t, w.kariC, w.kari.Id)
	if len(profiles) != 3 {
		t.Fatalf("Kari's profiles = %+v, want electrician, operator and the archived one", profiles)
	}
	byKey := map[string]gen.StaffPersonProfile{}
	for _, p := range profiles {
		byKey[p.Profile.Key] = p
	}
	if p := byKey["electrician"]; !p.Assigned || p.Department != nil || p.Profile.Archived || p.Profile.Id != electrician {
		t.Errorf("electrician = %+v, want assigned", p)
	}
	if p := byKey["operator"]; p.Assigned || p.Department == nil || p.Department.Id != ops || p.Department.Code != "OPS" {
		t.Errorf("operator = %+v, want OPS's default", p)
	}
	if p := byKey["retired"]; !p.Assigned || !p.Profile.Archived {
		t.Errorf("retired = %+v, want assigned and archived", p)
	}

	gaps := getGaps(t, w.kariC, w.kari.Id)
	if len(gaps.Profiles) != 2 || gaps.Profiles[0].Profile.Key != "electrician" || gaps.Profiles[1].Profile.Key != "operator" {
		t.Fatalf("gap profiles = %+v, want electrician and operator, not the archived one", gaps.Profiles)
	}
	if it := gapItemOf(t, gaps, "electrician", w.fse); it.Status != gen.GapMissing || it.Declared || it.Verified || !it.MustBeValid {
		t.Errorf("FSE = %+v, want missing", it)
	}
	if it := gapItemOf(t, gaps, "electrician", w.golang); it.Status != gen.GapBelowLevel || !it.Declared || it.Level == nil ||
		*it.Level != 2 || *it.MinLevel != 3 {
		t.Errorf("golang under electrician = %+v, want below_level, declared, 2 of 3", it)
	}
	if it := gapItemOf(t, gaps, "electrician", cobol); it.Status != gen.GapMissing || !it.Archived || !it.Competence.Archived {
		t.Errorf("cobol = %+v, want missing and archived", it)
	}
	if it := gapItemOf(t, gaps, "operator", w.golang); it.Status != gen.GapMet || !it.Declared {
		t.Errorf("golang under operator = %+v, want met, declared", it)
	}
	if gaps.Profiles[0].NotMet != 3 || gaps.Profiles[1].NotMet != 0 || gaps.NotMet != 3 {
		t.Errorf("not met = %d, %d, %d in all; want 3, 0, 3", gaps.Profiles[0].NotMet, gaps.Profiles[1].NotMet, gaps.NotMet)
	}

	// Her manager adds a valid FSE: met, verified, with its end.
	fse := declare(t, w.bossC, w.kari.Id, map[string]any{"competenceId": w.fse, "validTo": "2027-09-01"})
	gaps = getGaps(t, w.bossC, w.kari.Id)
	if it := gapItemOf(t, gaps, "electrician", w.fse); it.Status != gen.GapMet || !it.Verified || it.Declared ||
		it.ValidTo == nil || it.ValidTo.String() != "2027-09-01" {
		t.Errorf("FSE added = %+v, want met, verified, valid to 2027-09-01", it)
	}
	if gaps.NotMet != 2 {
		t.Errorf("not met = %d, want 2", gaps.NotMet)
	}
	// Its end moved within the lead of 30 days: expiring, not counted.
	h.Exec(t, `UPDATE staff.person_competences SET valid_to = '2026-09-30' WHERE id = $1`, fse.Id)
	gaps = getGaps(t, w.hr, w.kari.Id)
	if it := gapItemOf(t, gaps, "electrician", w.fse); it.Status != gen.GapExpiring {
		t.Errorf("FSE ending within its lead = %+v, want expiring", it)
	}
	if gaps.NotMet != 2 {
		t.Errorf("not met with FSE expiring = %d, want 2", gaps.NotMet)
	}

	// A person no profile reaches has an empty list.
	if got := getGaps(t, w.hr, w.boss.Id); len(got.Profiles) != 0 || got.NotMet != 0 {
		t.Errorf("Bjørn's gaps = %+v, want none", got)
	}
	if got := getPersonProfiles(t, w.hr, w.boss.Id); len(got) != 0 {
		t.Errorf("Bjørn's profiles = %+v, want none", got)
	}
}

// Who reads them (reading 32): the Profiles and gaps group — the person,
// their manager, the Approver, HR and staff:personal-data; a colleague and
// a caller who does not see the person get the bare 404, as an unknown id
// does.
func TestCompetenceGaps_PerAudience(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	rawAssign(t, w.h, w.kari.Id, rawProfile(t, w.h, "electrician", false, gapItemFor{competence: w.fse, mustBeValid: true}))

	for name, c := range map[string]*modtest.Client{
		"Kari": w.kariC, "her manager": w.bossC, "an Approver": w.approver, "HR": w.hr, "staff:personal-data": w.pd,
	} {
		if got := getPersonProfiles(t, c, w.kari.Id); len(got) != 1 {
			t.Errorf("%s reads Kari's profiles %+v, want one", name, got)
		}
		if got := getGaps(t, c, w.kari.Id); got.NotMet != 1 {
			t.Errorf("%s reads Kari's gaps %+v, want one not met", name, got)
		}
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "a stranger": w.nobody} {
		for _, path := range []string{personProfileListPath(w.kari.Id), competenceGapsPath(w.kari.Id)} {
			if r := c.Do(http.MethodGet, path, nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
				t.Errorf("%s reads %s: %d %s, want the bare 404", name, path, r.Status, r.Body)
			}
		}
	}
	for _, path := range []string{personProfileListPath(9999999), competenceGapsPath(9999999)} {
		if r := w.hr.Do(http.MethodGet, path, nil); r.Status != http.StatusNotFound {
			t.Errorf("HR reads %s: %d, want 404", path, r.Status)
		}
	}
}
