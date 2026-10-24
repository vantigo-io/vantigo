package staff_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// competenceDetailFields are a row's detail fields (staff competence design
// §5's "Competence detail" row), and competenceGroupFields the Competence
// group's, as the wire names them.
var (
	competenceDetailFields = []string{"issuedOn", "issuer", "reference", "evidence", "source", "verifiedAt", "verifiedBy", "verifiedBasis", "verifiedNote"}
	competenceGroupFields  = []string{"id", "personId", "competence", "validFrom", "validTo", "verified", "superseded", "expiring", "expired", "revision", "declaredAt"}
)

// rawCompetences is a person's rows as c reads them, each a bare JSON
// object, for a test whose subject is whether a key is there at all; nil and
// the status when the read was not 200.
func rawCompetences(t *testing.T, c *modtest.Client, personID int64) ([]map[string]any, int) {
	t.Helper()
	r := c.Do(http.MethodGet, personCompetencesPath(personID)+"?history=true", nil)
	if r.Status != http.StatusOK {
		return nil, r.Status
	}
	var out []map[string]any
	r.JSON(&out)
	return out, r.Status
}

// Who reads what of a person's competences (design §5): everyone who sees
// the person reads the Competence group; the detail — the issuer, the
// reference, the evidence, the source and the verification, its note among
// them — is the person's, their manager's, the Approver's and HR's, absent
// from a colleague's answer, not null; a caller who does not see the person,
// and a colleague on a person not active today, get the 404 an unknown id
// gets.
func TestPersonCompetences_NoLeaksPerAudience(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	row := declare(t, w.kariC, w.kari.Id, map[string]any{
		"competenceId": w.fse, "issuedOn": "2026-03-01", "validTo": "2027-03-01", "issuer": "Kurs AS", "reference": "FSE-123",
	})
	addEvidence(t, w.kariC, row.Id)
	verify(t, w.bossC, row.Id, map[string]any{"basis": "evidence", "note": "Card seen"})

	for name, c := range map[string]*modtest.Client{
		"Kari": w.kariC, "her manager": w.bossC, "an Approver": w.approver, "HR": w.hr, "staff:personal-data": w.pd,
	} {
		rows, status := rawCompetences(t, c, w.kari.Id)
		if status != http.StatusOK || len(rows) != 1 {
			t.Fatalf("%s: status %d rows %v, want Kari's one row", name, status, rows)
		}
		for _, key := range append(append([]string{}, competenceGroupFields...), competenceDetailFields...) {
			if _, ok := rows[0][key]; !ok {
				t.Errorf("%s does not read %s: %v", name, key, rows[0])
			}
		}
	}

	rows, status := rawCompetences(t, w.viewer, w.kari.Id)
	if status != http.StatusOK || len(rows) != 1 {
		t.Fatalf("a colleague: status %d rows %v, want Kari's one row", status, rows)
	}
	for _, key := range competenceDetailFields {
		if _, ok := rows[0][key]; ok {
			t.Errorf("a colleague reads %s: %v", key, rows[0])
		}
	}
	for _, key := range []string{"competence", "validFrom", "verified"} {
		if _, ok := rows[0][key]; !ok {
			t.Errorf("a colleague does not read %s: %v", key, rows[0])
		}
	}

	if _, status := rawCompetences(t, w.nobody, w.kari.Id); status != http.StatusNotFound {
		t.Errorf("a stranger reads Kari's competences: %d, want 404", status)
	}
	if r := w.nobody.Do(http.MethodPut, personCompetencePath(row.Id), map[string]any{}, ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("a stranger's change of Kari's row = %d, want 404", r.Status)
	}
	hire := seedPerson(t, w.h, "Ny", "Ansatt")
	declare(t, w.hr, hire, map[string]any{"competenceId": w.fse})
	if _, status := rawCompetences(t, w.viewer, hire); status != http.StatusNotFound {
		t.Errorf("a colleague reads a person not active today: %d, want 404", status)
	}

	// The history: the person, the manager and HR read the events, a
	// colleague none; the note is in no event.
	if r := w.viewer.Do(http.MethodGet, eventsPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a colleague reads Kari's history: %d, want 404", r.Status)
	}
}
