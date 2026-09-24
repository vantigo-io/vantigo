package projects_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// repointProjectsCustomer runs the module's own holder, built from the
// harness's dependencies exactly as module.Compose builds it, inside a
// transaction the test owns — the customers merge's position — and commits it
// or rolls it back as told.
func repointProjectsCustomer(t *testing.T, h *modtest.Harness, from, into int32, commit bool) []contracts.RepointedReferences {
	t.Helper()
	build := projects.Module().CustomerReferences
	if build == nil {
		t.Fatal("the module declares no customer reference holder")
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := build(h.Deps()).RepointCustomer(ctx, tx, from, into)
	if err != nil {
		t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return moved
}

// insertProjectFor writes a project row directly: the holder's subject is the
// column, and the create endpoint's own rules (a customer the directory knows,
// a suggested code) are not what this file tests. customerID nil is an
// internal project.
func insertProjectFor(t *testing.T, h *modtest.Harness, code string, customerID *int32) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO projects.projects (code, name, customer_id, billing_type, created_by_user_id, created_at, updated_at)
		VALUES ($1, $1, $2, 'time-and-materials', $3, $4, $4)
		RETURNING id`, code, customerID, uuid.New(), h.Now())
}

// TestCustomerReferences_RepointsEveryProjectOfTheAbsorbedCustomer is this
// module's half of a customer merge (customers merge design D1): every project
// billed to the absorbed customer bills to the survivor, each moved row's
// revision advances as any change to it does, and nothing else moves — not the
// survivor's own project, not another customer's, not an internal one.
func TestCustomerReferences_RepointsEveryProjectOfTheAbsorbedCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	absorbed, survivor, other := int32(customerKraftVerket), int32(customerAcme), int32(customerArchived)
	first := insertProjectFor(t, h, "MRG1000", &absorbed)
	second := insertProjectFor(t, h, "MRG1001", &absorbed)
	own := insertProjectFor(t, h, "MRG1002", &survivor)
	elsewhere := insertProjectFor(t, h, "MRG1003", &other)
	internal := insertProjectFor(t, h, "MRG1004", nil)
	h.Advance(time.Hour)

	moved := repointProjectsCustomer(t, h, absorbed, survivor, true)

	if want := []contracts.RepointedReferences{{Kind: "projects.projects", Count: 2}}; !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer = %+v, want %+v", moved, want)
	}
	for _, id := range []int32{first, second, own} {
		if got := modtest.One[int32](t, h, `SELECT customer_id FROM projects.projects WHERE id = $1`, id); got != survivor {
			t.Errorf("project %d customer_id = %d, want the survivor %d", id, got, survivor)
		}
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM projects.projects WHERE id = $1`, elsewhere); got != other {
		t.Errorf("another customer's project moved to %d", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.projects WHERE id = $1 AND customer_id IS NULL`, internal); n != 1 {
		t.Error("the internal project gained a customer")
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, first); got != 2 {
		t.Errorf("a moved project's revision = %d, want 2", got)
	}
	if got := modtest.One[time.Time](t, h, `SELECT updated_at FROM projects.projects WHERE id = $1`, first); !got.Equal(h.Now()) {
		t.Errorf("a moved project's updated_at = %v, want the merge's %v", got, h.Now())
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, own); got != 1 {
		t.Errorf("the survivor's own project was written: revision %d", got)
	}
}

// The holder writes only through the transaction it is handed: rolled back,
// nothing moved — which is what lets a merge's later failure undo it. A
// customer with no projects is a zero, not an absent kind.
func TestCustomerReferences_WritesOnlyInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	absorbed, survivor := int32(customerKraftVerket), int32(customerAcme)
	id := insertProjectFor(t, h, "MRG2000", &absorbed)

	if moved := repointProjectsCustomer(t, h, absorbed, survivor, false); len(moved) != 1 || moved[0].Count != 1 {
		t.Fatalf("RepointCustomer = %+v, want one project", moved)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM projects.projects WHERE id = $1`, id); got != absorbed {
		t.Errorf("after a rollback customer_id = %d, want %d still", got, absorbed)
	}

	want := []contracts.RepointedReferences{{Kind: "projects.projects", Count: 0}}
	if moved := repointProjectsCustomer(t, h, customerUnknown, survivor, true); !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer for a customer with no projects = %+v, want %+v", moved, want)
	}
}

// A customer merged into itself moves nothing (the holder's own guard; the
// merge refuses merge_self before it gets here): no project is written, so no
// revision advances, and the kind is still reported, as a zero.
func TestCustomerReferences_RepointingACustomerOntoItselfWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	customer := int32(customerAcme)
	id := insertProjectFor(t, h, "MRG3000", &customer)

	want := []contracts.RepointedReferences{{Kind: "projects.projects", Count: 0}}
	if moved := repointProjectsCustomer(t, h, customer, customer, true); !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer(%d, %d) = %+v, want %+v", customer, customer, moved, want)
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, id); got != 1 {
		t.Errorf("the project's revision = %d, want 1: it was written", got)
	}
}

// Compose puts projects' holder on every module's Deps once projects is
// composed — the capture module is TestModule_ComposesProjectDirectoryOntoDeps'
// own, named "customers" so a real embedded contract loads for it. The one
// holder is run, on a transaction rolled back after, to prove it is projects'
// own and not merely one: it answers projects.projects.
func TestModule_ComposesItsCustomerReferenceHolderOntoDeps(t *testing.T) {
	t.Parallel()
	var got []contracts.CustomerReferenceHolder
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d.CustomerReferenceHolders
			return http.NotFoundHandler(), nil
		},
	}

	h := newHarness(t, modtest.WithModule(capture))

	if len(got) != 1 {
		t.Fatalf("Deps.CustomerReferenceHolders = %v, want projects' one holder", got)
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := got[0].RepointCustomer(ctx, tx, customerKraftVerket, customerAcme)
	if err != nil {
		t.Fatalf("RepointCustomer: %v", err)
	}
	if len(moved) != 1 || moved[0].Kind != "projects.projects" {
		t.Errorf("the composed holder answered %+v, want projects' projects.projects", moved)
	}
}
