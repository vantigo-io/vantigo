package projects_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

func projectsPersonalData(t *testing.T, h *modtest.Harness) contracts.CustomerPersonalData {
	t.Helper()
	build := projects.Module().CustomerPersonalData
	if build == nil {
		t.Fatal("the module declares no customer personal data")
	}
	return build(h.Deps())
}

// TestCustomerPersonalData_ExportsThePersonsProjects is projects' section of a
// private person's export (customers GDPR design D2): code, name, status and
// dates of every project billed to them, and none of anybody else's.
func TestCustomerPersonalData_ExportsThePersonsProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person, other := int32(customerAcme), int32(customerKraftVerket)
	id := insertProjectFor(t, h, "GDPR1000", &person)
	h.Exec(t, `UPDATE projects.projects SET name = 'Varmepumpe', start_date = '2026-03-01', end_date = '2026-04-15' WHERE id = $1`, id)
	insertProjectFor(t, h, "GDPR1001", &other)

	section, err := projectsPersonalData(t, h).ExportCustomerData(context.Background(), person)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, _ := json.Marshal(section)
	if want := `{"projects":[{"code":"GDPR1000","name":"Varmepumpe","status":"planned","startDate":"2026-03-01","endDate":"2026-04-15"}]}`; string(body) != want {
		t.Errorf("section = %s, want %s", body, want)
	}
	if none, err := projectsPersonalData(t, h).ExportCustomerData(context.Background(), int32(customerArchived)); err != nil || none != nil {
		t.Errorf("a customer with no project = %v, %v; want nil, nil", none, err)
	}
}

// Erasing keeps everything (design D2): invoiced work stays, and no customer
// name is stored here to blank — the project names the anonymised customer
// through the directory from then on. It still says it was asked.
func TestCustomerPersonalData_EraseKeepsTheProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := int32(customerAcme)
	id := insertProjectFor(t, h, "GDPR1002", &person)
	before := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, id)

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	erased, err := projectsPersonalData(t, h).EraseCustomerData(ctx, tx, person)
	if err != nil {
		t.Fatalf("EraseCustomerData: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if want := []contracts.ErasedData{{Kind: "projects.projects", Count: 0}}; !slices.Equal(erased, want) {
		t.Errorf("EraseCustomerData = %+v, want %+v", erased, want)
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1 AND customer_id = $2`, id, person); got != before {
		t.Errorf("the project's revision = %d (was %d): an erase that keeps everything wrote the row", got, before)
	}
}
