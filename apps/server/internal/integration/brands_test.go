package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// Brands are assigned by rules whose targets are another module's: with Staff
// composed beside identity, a person who works under a legal entity sees the
// brand its rule names, a member of an access group the group's, and everyone
// else the default (brands design B-B). The legal entity reaches identity only
// through contracts.BrandTargets, which Staff provides and Compose wires.
func TestBrands_RulesAssignByLegalEntityAndAccessGroupAcrossIdentityAndStaff(t *testing.T) {
	t.Parallel()
	h := modtest.New(t,
		modtest.WithRecorder(contracttest.NewForModule("identity")),
		modtest.WithModule(staff.Module()),
	)
	admin, _ := h.SignInUser(t, "identity:brands")
	workerClient, worker := h.SignInUser(t)
	memberClient, member := h.SignInUser(t)
	otherClient, _ := h.SignInUser(t)

	// A legal entity, a unit, a person employed there and linked to the worker's account.
	entity := modtest.One[int64](t, h, `
		INSERT INTO staff.legal_entities (name, is_default, created_at, updated_at) VALUES ('Nordlys AS', false, $1, $1) RETURNING id`, h.Now())
	unit := modtest.One[int64](t, h, `
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at) VALUES ($1, 'Nordlys Oslo', false, $2, $2) RETURNING id`, entity, h.Now())
	person := modtest.One[int64](t, h, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('B1', 'Ola', 'Nordmann', 'Ola Nordmann', $1, $1) RETURNING id`, h.Now())
	h.Exec(t, `INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date, created_at, updated_at)
	           VALUES ($1, 'B1-1', 'ordinary', $2, '2020-01-01', $3, $3)`, person, unit, h.Now())
	h.Exec(t, `INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
	           VALUES ($1, 'identity', $2, 'linked', false, $3, $3)`, person, worker.String(), h.Now())

	// An access group with the second person in it.
	group := uuid.New()
	h.Exec(t, `INSERT INTO identity.access_groups (id, display_name, source, is_active, version, created_at, updated_at)
	           VALUES ($1, 'Fjord team', 'local', true, $2, $3, $3)`, group, uuid.New(), h.Now())
	h.Exec(t, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
	           VALUES ($1, $2, 'local', true, NULL)`, group, member)

	// Two brands, published.
	brandIDs := map[string]string{}
	for name, primary := range map[string]string{"Nordlys": "#0f766e", "Fjordkraft": "#b45309"} {
		var created struct {
			ID       string `json:"id"`
			Revision int    `json:"revision"`
		}
		okJSON(t, admin, http.MethodPost, "/api/v1/identity/brands", map[string]any{"name": name, "title": name, "primary": primary, "radius": "md"}, &created)
		r := admin.Do(http.MethodPost, fmt.Sprintf("/api/v1/identity/brands/%s/publish", created.ID), nil, modtest.Header("If-Match", fmt.Sprint(created.Revision)))
		if r.Status != http.StatusOK {
			t.Fatalf("publish %s: %d %s", name, r.Status, r.Body)
		}
		brandIDs[name] = created.ID
	}

	// The rule editor offers Staff's legal entity, and the rules are saved.
	var targets struct {
		StaffEnabled  bool `json:"staffEnabled"`
		LegalEntities []struct {
			ID, Name string
		} `json:"legalEntities"`
	}
	okJSON(t, admin, http.MethodGet, "/api/v1/identity/brand-rule-targets", nil, &targets)
	if !targets.StaffEnabled || len(targets.LegalEntities) != 1 || targets.LegalEntities[0].Name != "Nordlys AS" {
		t.Fatalf("targets = %+v", targets)
	}
	var list struct {
		Revision int `json:"revision"`
	}
	okJSON(t, admin, http.MethodGet, "/api/v1/identity/brand-rules", nil, &list)
	put := admin.Do(http.MethodPut, "/api/v1/identity/brand-rules", map[string]any{"items": []map[string]string{
		{"brandId": brandIDs["Nordlys"], "kind": "legal_entity", "targetRef": targets.LegalEntities[0].ID},
		{"brandId": brandIDs["Fjordkraft"], "kind": "access_group", "targetRef": group.String()},
	}}, modtest.Header("If-Match", fmt.Sprint(list.Revision)))
	if put.Status != http.StatusOK {
		t.Fatalf("PUT brand-rules: %d %s", put.Status, put.Body)
	}

	seen := func(c *modtest.Client) string {
		var b struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		okJSON(t, c, http.MethodGet, "/api/v1/identity/brand", nil, &b)
		return b.Name + "/" + b.Source
	}
	if got := seen(workerClient); got != "Nordlys/rule" {
		t.Errorf("a person employed under the legal entity sees %q, want Nordlys/rule", got)
	}
	if got := seen(memberClient); got != "Fjordkraft/rule" {
		t.Errorf("a member of the group sees %q, want Fjordkraft/rule", got)
	}
	if got := seen(otherClient); got != "Vantigo/default" {
		t.Errorf("a person in no rule sees %q, want Vantigo/default", got)
	}
}
