package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestProjectsDeliveryMilestones_AppliesAndIsIdempotent proves
// 00096_projects_delivery_milestones.sql applies, rolls back and re-applies cleanly,
// creating the delivery_milestones table and adding delivery_milestone_id to tasks.
func TestProjectsDeliveryMilestones_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 96) // 00096_projects_delivery_milestones.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Prove delivery_milestones table exists with expected columns
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

	taskCol := func() string {
		t.Helper()
		var got string
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable, ','
			                ORDER BY column_name COLLATE "C"), '')
			FROM information_schema.columns
			WHERE table_schema = 'projects' AND table_name = 'tasks'
			  AND column_name = 'delivery_milestone_id'`).Scan(&got); err != nil {
			t.Fatalf("read tasks delivery_milestone_id: %v", err)
		}
		return got
	}

	if got, want := taskCol(), "delivery_milestone_id:integer:YES"; got != want {
		t.Errorf("tasks delivery_milestone_id column = %q, want %q", got, want)
	}

	dmCols := columns("delivery_milestones")
	if dmCols == "" {
		t.Fatalf("expected delivery_milestones table to exist")
	}

	// Insert project, delivery milestone, and task linked to it
	authorID := uuid.New()
	var projectID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.projects (code, name, billing_type, currency, created_by_user_id, created_at, updated_at)
		VALUES ('DLV1', 'Delivery Milestones Test', 'time-and-materials', 'NOK', $1, now(), now())
		RETURNING id`, authorID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	var milestoneID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.delivery_milestones (project_id, name, position, created_by_user_id, created_at, updated_at)
		VALUES ($1, 'Alpha Release', 1, $2, now(), now())
		RETURNING id`, projectID, authorID).Scan(&milestoneID); err != nil {
		t.Fatalf("insert delivery milestone: %v", err)
	}

	var taskID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.tasks (project_id, title, position, delivery_milestone_id, created_by_user_id, created_at, updated_at)
		VALUES ($1, 'Build prototype', 1, $2, $3, now(), now())
		RETURNING id`, projectID, milestoneID, authorID).Scan(&taskID); err != nil {
		t.Fatalf("insert task linked to delivery milestone: %v", err)
	}

	// Deleting delivery milestone sets task.delivery_milestone_id to NULL
	if _, err := pool.Exec(ctx, `DELETE FROM projects.delivery_milestones WHERE id = $1`, milestoneID); err != nil {
		t.Fatalf("delete delivery milestone: %v", err)
	}

	var linkedMilestone *int32
	if err := pool.QueryRow(ctx, `SELECT delivery_milestone_id FROM projects.tasks WHERE id = $1`, taskID).Scan(&linkedMilestone); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if linkedMilestone != nil {
		t.Errorf("task delivery_milestone_id = %v, want nil (ON DELETE SET NULL)", *linkedMilestone)
	}
}
