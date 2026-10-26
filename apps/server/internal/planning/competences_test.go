package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// TestCompetences_Catalogue is GET /competences (plan reading 6): Staff's
// catalogue as the caller may see it, hidden ones flagged, in Staff's order;
// empty, not an error, when Staff answers nothing; planning:view's.
func TestCompetences_Catalogue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	const path = "/api/v1/planning/competences"

	var list gen.PlanningCompetenceList
	getJSON(t, c, path, http.StatusOK, &list)
	if list.Competences == nil || len(list.Competences) != 0 {
		t.Errorf("no catalogue = %+v, want an empty list", list)
	}

	seedCatalogue(h)
	getJSON(t, c, path, http.StatusOK, &list)
	if len(list.Competences) != 3 {
		t.Fatalf("catalogue = %+v, want three", list.Competences)
	}
	hms, java, cobol := list.Competences[0], list.Competences[1], list.Competences[2]
	if hms != (gen.PlanningCompetence{Id: catalogueHMSCard.ID, Key: "hms-kort", NameEn: "HSE card", NameNb: "HMS-kort",
		CategoryKey: "sertifikat", Kind: gen.PlanningCompetenceKindCertificate}) {
		t.Errorf("HMS = %+v", hms)
	}
	if !java.HasLevel || java.Hidden || java.Kind != gen.PlanningCompetenceKindSkill {
		t.Errorf("Java = %+v, want a skill with a level", java)
	}
	if !cobol.Hidden {
		t.Errorf("COBOL = %+v, want hidden", cobol)
	}

	h.workforce.setVisible(false)
	getJSON(t, c, path, http.StatusOK, &list)
	if len(list.Competences) != 0 {
		t.Errorf("unseen by Staff the catalogue = %+v, want empty", list.Competences)
	}
	getJSON(t, h.SignIn(t, "planning:access"), path, http.StatusForbidden, nil)
}
