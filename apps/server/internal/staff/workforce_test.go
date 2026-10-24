package staff_test

import (
	"context"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// contracts.Workforce (staff competence design S15, §4 "The contract"; plan
// Task 3b): Staff its own gatekeeper — staff:view, and the person active
// today, for the principal in ctx: a request's caller or the user AsUser
// names; nothing, never an error, for anyone else or a bare context. A
// declared certificate is not held; a declared skill is, unverified.

// workforceOf is the module's provider over h's Deps, as Compose builds it.
func workforceOf(h *harness) contracts.Workforce {
	return staff.Module().Workforce(h.Deps())
}

// heldKeys is each person's held competences' keys, in the catalogue's order.
func heldKeys(held map[int64][]contracts.HeldCompetence) map[int64][]string {
	out := map[int64][]string{}
	for id, list := range held {
		for _, c := range list {
			out[id] = append(out[id], c.Key)
		}
	}
	return out
}

func TestWorkforce_HeldCompetences(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	skill := companySkill(t, h, "sveising", "Sveising")
	two := int16(2)
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "hms-kort"), from: "2026-01-01", to: strPtr("2027-12-31"), verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "truck"), from: "2021-09-02", to: strPtr("2026-09-01"), verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "kran"), from: "2026-10-01", verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "fse"), from: "2026-01-01", to: strPtr("2026-12-31")})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: skill, level: &two, from: "2026-01-01"})
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, w.boss, "2024-01-01", dayPtr("2026-08-31"))
	rawPersonCompetence(t, h, competenceRow{person: leaver, competence: catalogueID(t, h, "hms-kort"), from: "2026-01-01", verified: true})

	viewCtx, viewUser := calendarCaller(t, h.Harness, "staff:access", "staff:view")
	ids := []int64{w.kari, leaver, w.boss}
	held, err := workforceOf(h).HeldCompetences(viewCtx, calDay(2026, 9, 12), ids)
	if err != nil {
		t.Fatalf("HeldCompetences: %v", err)
	}
	// On 12 September: HMS-kort and the declared skill; not the truck
	// certificate ended on 1 September, not the crane one from October,
	// not the declared FSE; the leaver absent; Bjørn holds nothing.
	got := heldKeys(held)
	if len(got) != 1 || !slices.Equal(got[w.kari], []string{"hms-kort", "sveising"}) {
		t.Fatalf("held on 12 September = %v", got)
	}
	hmsHeld, skillHeld := held[w.kari][0], held[w.kari][1]
	if !hmsHeld.Verified || hmsHeld.Kind != "certificate" || hmsHeld.Level != nil ||
		!hmsHeld.ValidFrom.Equal(calDay(2026, 1, 1)) || hmsHeld.ValidTo == nil || !hmsHeld.ValidTo.Equal(calDay(2027, 12, 31)) {
		t.Errorf("HMS-kort = %+v", hmsHeld)
	}
	if skillHeld.Verified || skillHeld.Kind != "skill" || skillHeld.Level == nil || *skillHeld.Level != 2 || skillHeld.ValidTo != nil {
		t.Errorf("the declared skill = %+v", skillHeld)
	}

	// The validity window is the date asked about's.
	held, err = workforceOf(h).HeldCompetences(viewCtx, calDay(2026, 8, 15), ids)
	if err != nil {
		t.Fatalf("HeldCompetences: %v", err)
	}
	if got := heldKeys(held); !slices.Equal(got[w.kari], []string{"hms-kort", "truck", "sveising"}) {
		t.Errorf("held on 15 August = %v", got)
	}
	held, err = workforceOf(h).HeldCompetences(viewCtx, calDay(2026, 10, 1), ids)
	if err != nil {
		t.Fatalf("HeldCompetences: %v", err)
	}
	if got := heldKeys(held); !slices.Equal(got[w.kari], []string{"hms-kort", "kran", "sveising"}) {
		t.Errorf("held on 1 October = %v", got)
	}

	// The same user through AsUser, with no request.
	held, err = workforceOf(h).HeldCompetences(contracts.AsUser(context.Background(), viewUser), calDay(2026, 9, 12), ids)
	if err != nil || !slices.Equal(heldKeys(held)[w.kari], []string{"hms-kort", "sveising"}) {
		t.Errorf("under AsUser = %v, %v", heldKeys(held), err)
	}

	// Without staff:view — staff:manage alone included — and with no
	// principal at all: nothing, and no error.
	noView, _ := calendarCaller(t, h.Harness, "staff:access")
	manageOnly, manageUser := calendarCaller(t, h.Harness, "staff:access", "staff:manage")
	for name, ctx := range map[string]context.Context{
		"a request without staff:view": noView,
		"a request with staff:manage":  manageOnly,
		"AsUser without staff:view":    contracts.AsUser(context.Background(), manageUser),
		"a bare context":               context.Background(),
	} {
		if held, err := workforceOf(h).HeldCompetences(ctx, calDay(2026, 9, 12), ids); err != nil || held != nil {
			t.Errorf("%s: %v, %v; want nothing", name, held, err)
		}
	}
}

func TestWorkforce_Competences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	skill := companySkill(t, h, "sveising", "Sveising")
	h.Exec(t, `UPDATE staff.competences SET hidden = true WHERE key = 'kran'`)
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE key = 'truck'`, h.Now())

	view, _ := calendarCaller(t, h.Harness, "staff:access", "staff:view")
	manage, _ := calendarCaller(t, h.Harness, "staff:access", "staff:manage")
	for name, ctx := range map[string]context.Context{"staff:view": view, "staff:manage": manage} {
		entries, err := workforceOf(h).Competences(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		byKey := map[string]contracts.CompetenceEntry{}
		for _, e := range entries {
			byKey[e.Key] = e
		}
		if len(entries) != 15 {
			t.Errorf("%s: %d entries, want the fifteen seeds less the archived one, and the skill", name, len(entries))
		}
		if _, ok := byKey["truck"]; ok {
			t.Errorf("%s: the archived competence is listed", name)
		}
		if e := byKey["kran"]; !e.Hidden {
			t.Errorf("%s: the hidden competence = %+v", name, e)
		}
		hms := byKey["hms-kort"]
		if hms.CategoryKey != "sertifikat" || hms.Kind != "certificate" || hms.HasLevel || hms.ValidityMonths == nil || *hms.ValidityMonths != 24 ||
			hms.NameEn != "HMS card" || hms.NameNb != "HMS-kort" {
			t.Errorf("%s: HMS-kort = %+v", name, hms)
		}
		if e := byKey["sveising"]; e.ID != skill || e.CategoryKey != "fag" || e.Kind != "skill" || !e.HasLevel || e.ValidityMonths != nil {
			t.Errorf("%s: the skill = %+v", name, e)
		}
	}

	none, _ := calendarCaller(t, h.Harness, "staff:access")
	for name, ctx := range map[string]context.Context{"staff:access alone": none, "a bare context": context.Background()} {
		if entries, err := workforceOf(h).Competences(ctx); err != nil || entries != nil {
			t.Errorf("%s: %v, %v; want nothing", name, entries, err)
		}
	}
}
