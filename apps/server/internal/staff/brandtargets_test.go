package staff_test

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// seedEntity writes a legal entity (archived when asked) with one business unit
// under it, and answers the entity's id as text and the unit's id.
func seedEntity(t *testing.T, h *harness, name string, archived bool) (string, int64) {
	t.Helper()
	var archivedAt *time.Time
	if archived {
		now := h.Now()
		archivedAt = &now
	}
	le := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.legal_entities (name, is_default, archived_at, created_at, updated_at)
		VALUES ($1, false, $2, $3, $3) RETURNING id`, name, archivedAt, h.Now())
	bu := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at)
		VALUES ($1, $2, false, $3, $3) RETURNING id`, le, name+" unit", h.Now())
	return strconv.FormatInt(le, 10), bu
}

// employ writes an employment of personID under unit from start to end (""
// for open-ended), both day offsets from today.
func employ(t *testing.T, h *harness, personID, unit int64, startOffset int, endOffset *int) {
	t.Helper()
	day := groupDay(t, today(t, h))
	start := day.AddDate(0, 0, startOffset)
	var end *time.Time
	if endOffset != nil {
		e := day.AddDate(0, 0, *endOffset)
		end = &e
	}
	h.Exec(t, `
		INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date, end_date, end_reason, created_at, updated_at)
		VALUES ($1, $2, 'ordinary', $3, $4::date, $5::date, CASE WHEN $5::date IS NULL THEN NULL ELSE 'contract_ended' END, $6, $6)`,
		personID, "BT-"+uuid.NewString()[:8], unit, start, end, h.Now())
}

func offset(n int) *int { return &n }

// Staff answers brand rules (brands design B9): the legal entities, archived
// ones marked, and the entities each linked user works under today — started and
// not ended, under a business unit of an entity that is not archived — and
// nothing for a user with no link or no employment in force.
func TestBrandTargets_LegalEntitiesAndTheEntitiesOfUsersToday(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	provider := staff.Module().BrandTargets(h.Deps())
	ctx := context.Background()

	alpha, alphaUnit := seedEntity(t, h, "Alpha AS", false)
	gamma, gammaUnit := seedEntity(t, h, "Gamma AS", false)
	_, goneUnit := seedEntity(t, h, "Gone AS", true)

	entities, err := provider.LegalEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	archived := map[string]bool{}
	for _, e := range entities {
		names = append(names, e.Name)
		archived[e.Name] = e.Archived
	}
	if !slices.Contains(names, "Alpha AS") || !archived["Gone AS"] || archived["Alpha AS"] {
		t.Errorf("legal entities = %+v", entities)
	}

	person := func(name string) (int64, uuid.UUID) {
		id := seedPerson(t, h, name, "Tester")
		user := uuid.New()
		link(t, h, id, user)
		return id, user
	}
	inForce, inForceUser := person("Inforce")
	employ(t, h, inForce, alphaUnit, -30, nil)
	endsToday, endsTodayUser := person("Endstoday")
	employ(t, h, endsToday, alphaUnit, -30, offset(0))
	ended, endedUser := person("Ended")
	employ(t, h, ended, alphaUnit, -30, offset(-1))
	future, futureUser := person("Future")
	employ(t, h, future, alphaUnit, 1, nil)
	two, twoUser := person("Two")
	employ(t, h, two, alphaUnit, -30, nil)
	employ(t, h, two, gammaUnit, -10, nil)
	employ(t, h, two, alphaUnit, -90, offset(-60)) // a past employment in the same entity adds nothing
	archivedEntity, archivedUser := person("Archived")
	employ(t, h, archivedEntity, goneUnit, -30, nil)
	noEmployment, noEmploymentUser := person("Nojob")
	_ = noEmployment
	unlinked := seedPerson(t, h, "Unlinked", "Tester")
	employ(t, h, unlinked, alphaUnit, -30, nil)
	unknownUser := uuid.New()

	got, err := provider.LegalEntitiesOf(ctx, []uuid.UUID{
		inForceUser, endsTodayUser, endedUser, futureUser, twoUser, archivedUser, noEmploymentUser, unknownUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID][]string{
		inForceUser:   {alpha},
		endsTodayUser: {alpha}, // an employment ending today is still in force today
		twoUser:       {alpha, gamma},
	}
	if len(got) != len(want) {
		t.Errorf("entities of users = %v, want %v", got, want)
	}
	for user, ids := range want {
		g := slices.Clone(got[user])
		slices.Sort(g)
		w := slices.Clone(ids)
		slices.Sort(w)
		if !slices.Equal(g, w) {
			t.Errorf("entities of %v = %v, want %v", user, g, w)
		}
	}
	if empty, err := provider.LegalEntitiesOf(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no users = %v, %v, want an empty map", empty, err)
	}
}
