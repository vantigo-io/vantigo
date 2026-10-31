package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestProjectsTemplates_AppliesAndIsIdempotent proves
// 00098_projects_templates.sql applies, rolls back and re-applies cleanly,
// creating the templates table.
func TestProjectsTemplates_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 98) // 00098_projects_templates.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Prove templates table exists with expected columns
	columns := func(table string) string {
		t.Helper()
		var got string
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable, ','
			                ORDER BY column_name COLLATE "C"), '')
			FROM information_schema.columns
			WHERE table_schema = 'projects' AND table_name = $1`, table).Scan(&got); err != nil {
			t.Fatalf("read columns for %s: %v", table, err)
		}
		return got
	}

	wantTemplates := "billing_type:character varying:YES,created_at:timestamp with time zone:NO,created_by_user_id:uuid:NO,dependencies:jsonb:NO,description:character varying:YES,id:integer:NO,milestones:jsonb:NO,name:character varying:NO,revision:integer:NO,tasks:jsonb:NO,updated_at:timestamp with time zone:NO"
	if got := columns("templates"); got != wantTemplates {
		t.Errorf("templates columns = %q, want %q", got, wantTemplates)
	}

	// Insert template
	adminID := uuid.New()
	var templateID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.templates (name, description, billing_type, milestones, tasks, dependencies, created_by_user_id, created_at, updated_at)
		VALUES ('Web Launch Blueprint', 'Blueprint description', 'time-and-materials', '[]'::jsonb, '[]'::jsonb, '[]'::jsonb, $1, now(), now())
		RETURNING id`, adminID).Scan(&templateID); err != nil {
		t.Fatalf("insert template: %v", err)
	}

	if templateID < 1001 {
		t.Errorf("templateID = %d, want >= 1001", templateID)
	}
}
