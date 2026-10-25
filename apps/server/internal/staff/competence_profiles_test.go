package staff_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The profiles catalogue's path.
const competenceProfilesPath = "/api/v1/staff/competence-profiles"

func competenceProfilePath(id int64) string { return rowPath(competenceProfilesPath, id) }

// profileItem is one item of a profile's request on competenceID, with the
// given fields set.
func profileItem(competenceID int64, kv ...any) map[string]any {
	return with(map[string]any{"competenceId": competenceID}, kv...)
}

// profileBody is a new profile keyed key with items.
func profileBody(key string, items ...map[string]any) map[string]any {
	list := make([]any, 0, len(items))
	for _, i := range items {
		list = append(list, i)
	}
	return map[string]any{"key": key, "names": map[string]any{"en": "Profile " + key, "nb": "Profil " + key}, "items": list}
}

// profileChange is p as a whole change request, with the given fields
// replaced.
func profileChange(p gen.StaffCompetenceProfile, kv ...any) map[string]any {
	items := make([]any, 0, len(p.Items))
	for _, i := range p.Items {
		item := map[string]any{"competenceId": i.CompetenceId, "mustBeValid": i.MustBeValid}
		if i.MinLevel != nil {
			item["minLevel"] = *i.MinLevel
		}
		items = append(items, item)
	}
	return with(map[string]any{
		"names": map[string]any{"en": p.Names.En, "nb": p.Names.Nb}, "archived": p.ArchivedAt != nil, "items": items,
	}, kv...)
}

// addProfile adds a profile as HR and fails the test unless it was created.
func addProfile(t *testing.T, hr *modtest.Client, body map[string]any) gen.StaffCompetenceProfile {
	t.Helper()
	var out gen.StaffCompetenceProfile
	do(t, hr, http.MethodPost, competenceProfilesPath, body, http.StatusCreated, &out)
	return out
}

// changeProfile changes p as HR with the given fields replaced and fails the
// test unless it answered 200.
func changeProfile(t *testing.T, hr *modtest.Client, p gen.StaffCompetenceProfile, kv ...any) gen.StaffCompetenceProfile {
	t.Helper()
	var out gen.StaffCompetenceProfile
	do(t, hr, http.MethodPut, competenceProfilePath(p.Id), profileChange(p, kv...), http.StatusOK, &out, ifMatch(p.Revision))
	return out
}

// itemOn is p's item on competenceID, nil when it has none.
func itemOn(p gen.StaffCompetenceProfile, competenceID int64) *gen.StaffCompetenceProfileItem {
	for i := range p.Items {
		if p.Items[i].CompetenceId == competenceID {
			return &p.Items[i]
		}
	}
	return nil
}

// installationFields is the fields of the newest installation event of kind
// about profile id, nil when there is none.
func installationFields(t *testing.T, h *harness, kind string, id int64) map[string]any {
	t.Helper()
	if h.Count(t, `SELECT count(*) FROM staff.events
		WHERE person_id IS NULL AND kind = $1 AND (payload->>'competenceProfileId')::bigint = $2`, kind, id) == 0 {
		return nil
	}
	payload := modtest.One[[]byte](t, h.Harness, `
		SELECT payload FROM staff.events
		WHERE person_id IS NULL AND kind = $1 AND (payload->>'competenceProfileId')::bigint = $2
		ORDER BY id DESC LIMIT 1`, kind, id)
	var out struct {
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	return out.Fields
}

// Every caller reads every profile, archived ones included with archivedAt
// (reading 36): the catalogue holds no personal data.
func TestCompetenceProfiles_EveryCallerReadsEveryProfile(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	op := addProfile(t, w.hr, profileBody("operator", profileItem(w.fse)))
	old := addProfile(t, w.hr, profileBody("old"))
	changeProfile(t, w.hr, old, "archived", true)
	for name, c := range map[string]*modtest.Client{"a viewer": w.viewer, "a caller with staff:access alone": w.nobody, "HR": w.hr} {
		var list []gen.StaffCompetenceProfile
		do(t, c, http.MethodGet, competenceProfilesPath, nil, http.StatusOK, &list)
		if len(list) != 2 || list[0].Key != "old" || list[0].ArchivedAt == nil || list[1].Id != op.Id || len(list[1].Items) != 1 ||
			list[1].Items[0].Competence.Key != "fse" {
			t.Errorf("%s reads %+v, want both by key, the archived one with archivedAt and the items", name, list)
		}
	}
}

// HR adds a profile: the key, the names and the items — a minimum level on a
// competence with a level, mustBeValid true when left out — and the creation
// is competence_profile_created with the key, the names and the items.
func TestCompetenceProfiles_Add(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	p := addProfile(t, w.hr, profileBody("developer", profileItem(w.golang, "minLevel", 2), profileItem(w.fse)))
	golang, fse := itemOn(p, w.golang), itemOn(p, w.fse)
	if p.Key != "developer" || p.Names.Nb != "Profil developer" || p.ArchivedAt != nil || p.Revision != 1 || len(p.Items) != 2 ||
		golang == nil || golang.MinLevel == nil || *golang.MinLevel != 2 || !golang.MustBeValid || golang.Competence.Key != "golang" ||
		fse == nil || fse.MinLevel != nil || !fse.MustBeValid || fse.Competence.Kind != "certificate" {
		t.Errorf("created = %+v, want the key, the names and both items", p)
	}
	fields := installationFields(t, h, "competence_profile_created", p.Id)
	if fields == nil {
		t.Fatal("no competence_profile_created event")
	}
	if fields["key"].(map[string]any)["to"] != "developer" || fields["names"] == nil || fields["archived"] != nil {
		t.Errorf("the created event's fields = %v, want the key, the names and the items", fields)
	}
	items, _ := fields["items"].(map[string]any)["to"].([]any)
	if len(items) != 2 {
		t.Fatalf("the created event's items = %v, want two", fields["items"])
	}
	for _, i := range items {
		item := i.(map[string]any)
		if len(item) > 3 || item["mustBeValid"] != true || item["competence"] != nil {
			t.Errorf("an event item = %v, want competenceId, minLevel and mustBeValid alone", item)
		}
	}
	empty := addProfile(t, w.hr, profileBody("nothing"))
	if len(empty.Items) != 0 {
		t.Errorf("an empty profile = %+v, want no items", empty)
	}

	if r := w.viewer.Do(http.MethodPost, competenceProfilesPath, profileBody("viewer")); r.Status != http.StatusForbidden {
		t.Errorf("a viewer's add = %d, want 403", r.Status)
	}

	cobol := addCompetence(t, w.hr, skillBody(t, h, "cobol")).Id
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), cobol)
	h.Exec(t, `UPDATE staff.competences SET hidden = true WHERE key = 'masseforflytning'`)
	hidden := competenceID(t, h, "masseforflytning")
	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = profileItem(int64(i + 1))
	}
	for _, tc := range []struct {
		name, code, field string
		body              map[string]any
	}{
		{"a key taken", "", "key", profileBody("developer")},
		{"a key with capitals", "", "key", profileBody("Developer")},
		{"no English name", "", "names.en", with(profileBody("x"), "names", map[string]any{"en": "", "nb": "X"})},
		{"a competence twice", "", "items[1].competenceId", profileBody("x", profileItem(w.golang), profileItem(w.golang, "minLevel", 1))},
		{"a level on a competence without one", "level_not_allowed", "items[1].minLevel", profileBody("x", profileItem(w.golang), profileItem(w.fse, "minLevel", 1))},
		{"a level of 5", "", "items[0].minLevel", profileBody("x", profileItem(w.golang, "minLevel", 5))},
		{"a hidden competence", "competence_unavailable", "items[0].competenceId", profileBody("x", profileItem(hidden))},
		{"an archived competence", "competence_unavailable", "items[1].competenceId", profileBody("x", profileItem(w.fse), profileItem(cobol))},
		{"an unknown competence", "competence_unavailable", "items[0].competenceId", profileBody("x", profileItem(9999999))},
		{"fifty-one items", "", "items", profileBody("x", many...)},
		{"a property an item does not name", "", "items[0].level", profileBody("x", profileItem(w.golang, "level", 2))},
		{"a property the profile does not name", "", "archived", with(profileBody("x"), "archived", true)},
		{"no items", "", "items", map[string]any{"key": "x", "names": map[string]any{"en": "X", "nb": "X"}}},
	} {
		p := refused(t, w.hr.Do(http.MethodPost, competenceProfilesPath, tc.body), http.StatusBadRequest, tc.code)
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.competence_profiles`); n != 2 {
		t.Errorf("%d profiles, want the two added", n)
	}
}

// HR changes a profile under its If-Match: the names, the archive and the
// items replaced whole — a kept item keeps its row, a changed one is
// changed, one left out removed — and the change is
// competence_profile_updated with the fields that changed. The key never
// changes; a stale revision is revision_conflict.
func TestCompetenceProfiles_Change(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	truck := competenceID(t, h, "truck")
	p := addProfile(t, w.hr, profileBody("developer", profileItem(w.golang, "minLevel", 2), profileItem(w.fse)))
	kept := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.competence_profile_items WHERE profile_id = $1 AND competence_id = $2`, p.Id, w.golang)

	changed := changeProfile(t, w.hr, p,
		"names", map[string]any{"en": "Developer", "nb": "Utvikler"},
		"items", []any{profileItem(w.golang, "minLevel", 3), profileItem(truck, "mustBeValid", false)})
	golang, fse, tr := itemOn(changed, w.golang), itemOn(changed, w.fse), itemOn(changed, truck)
	if changed.Key != "developer" || changed.Names.Nb != "Utvikler" || changed.Revision != p.Revision+1 || len(changed.Items) != 2 ||
		golang == nil || *golang.MinLevel != 3 || fse != nil || tr == nil || tr.MustBeValid {
		t.Errorf("changed = %+v, want the names, golang at 3, fse gone, truck not judged on validity", changed)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.competence_profile_items WHERE id = $1`, kept); n != 1 {
		t.Errorf("golang's item row is gone (%d), want it kept and changed", n)
	}
	fields := installationFields(t, h, "competence_profile_updated", p.Id)
	if fields == nil || fields["names"] == nil || fields["items"] == nil || fields["key"] != nil || fields["archived"] != nil {
		t.Errorf("the updated event's fields = %v, want the names and the items", fields)
	}
	if from, _ := fields["items"].(map[string]any)["from"].([]any); len(from) != 2 {
		t.Errorf("the items before = %v, want the two", fields["items"])
	}

	archived := changeProfile(t, w.hr, changed, "archived", true)
	if archived.ArchivedAt == nil {
		t.Errorf("archived = %+v, want archivedAt", archived)
	}
	if f := installationFields(t, h, "competence_profile_updated", p.Id); len(f) != 1 || f["archived"].(map[string]any)["to"] != true {
		t.Errorf("the archive's fields = %v, want archived alone", f)
	}
	restored := changeProfile(t, w.hr, archived, "archived", false)
	if restored.ArchivedAt != nil {
		t.Errorf("restored = %+v, want no archivedAt", restored)
	}

	refused(t, w.hr.Do(http.MethodPut, competenceProfilePath(p.Id), profileChange(restored), ifMatch(p.Revision)), http.StatusConflict, "revision_conflict")
	if r := w.hr.Do(http.MethodPut, competenceProfilePath(p.Id), profileChange(restored)); r.Status != http.StatusBadRequest {
		t.Errorf("no If-Match = %d, want 400", r.Status)
	}
	if r := w.hr.Do(http.MethodPut, competenceProfilePath(9999999), profileChange(restored), ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown profile = %d, want 404", r.Status)
	}
	pr := refused(t, w.hr.Do(http.MethodPut, competenceProfilePath(p.Id), profileChange(restored, "key", "other"), ifMatch(restored.Revision)),
		http.StatusBadRequest, "")
	if len(pr.Errors["key"]) == 0 {
		t.Errorf("a key sent: errors = %v, want one on key", pr.Errors)
	}
	if r := w.viewer.Do(http.MethodPut, competenceProfilePath(p.Id), profileChange(restored), ifMatch(restored.Revision)); r.Status != http.StatusForbidden {
		t.Errorf("a viewer's change = %d, want 403", r.Status)
	}
}

// An item unchanged on a competence archived or hidden since is kept (§4,
// reading 28); a new or changed one on it is competence_unavailable.
func TestCompetenceProfiles_AnUnchangedItemOnAnArchivedCompetenceIsKept(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	cobol := addCompetence(t, w.hr, skillBody(t, h, "cobol")).Id
	p := addProfile(t, w.hr, profileBody("legacy", profileItem(cobol, "minLevel", 1)))
	other := addProfile(t, w.hr, profileBody("other"))
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), cobol)

	renamed := changeProfile(t, w.hr, p, "names", map[string]any{"en": "Legacy", "nb": "Arv"})
	if item := itemOn(renamed, cobol); item == nil || !item.Competence.Archived || *item.MinLevel != 1 {
		t.Errorf("renamed = %+v, want the archived item kept", renamed)
	}
	refused(t, w.hr.Do(http.MethodPut, competenceProfilePath(p.Id), profileChange(renamed, "items", []any{profileItem(cobol, "minLevel", 2)}),
		ifMatch(renamed.Revision)), http.StatusBadRequest, "competence_unavailable")
	pr := refused(t, w.hr.Do(http.MethodPut, competenceProfilePath(other.Id), profileChange(other, "items", []any{profileItem(cobol, "minLevel", 1)}),
		ifMatch(other.Revision)), http.StatusBadRequest, "competence_unavailable")
	if len(pr.Errors["items[0].competenceId"]) == 0 {
		t.Errorf("a new item on an archived competence: errors = %v, want one on items[0].competenceId", pr.Errors)
	}
}

// A change that alters nothing bumps the revision and records no
// competence_profile_updated event (reading 27).
func TestCompetenceProfiles_AChangeThatAltersNothingRecordsNoEvent(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	p := addProfile(t, w.hr, profileBody("developer", profileItem(w.golang, "minLevel", 2), profileItem(w.fse, "mustBeValid", false)))

	same := changeProfile(t, w.hr, p)
	if same.Revision != p.Revision+1 || len(same.Items) != 2 || same.Names != p.Names || same.ArchivedAt != nil {
		t.Errorf("same = %+v, want the profile as it was at the next revision", same)
	}
	if f := installationFields(t, h, "competence_profile_updated", p.Id); f != nil {
		t.Errorf("an unchanged profile recorded competence_profile_updated with %v, want no event", f)
	}
}
