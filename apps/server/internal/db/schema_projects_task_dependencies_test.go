package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestProjectsTaskDependencies_AppliesAndIsIdempotent proves
// 00097_projects_task_dependencies.sql applies, rolls back and re-applies cleanly,
// creating the task_dependencies table.
func TestProjectsTaskDependencies_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 97) // 00097_projects_task_dependencies.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Prove task_dependencies table exists with expected columns
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

	wantDeps := "created_at:timestamp with time zone:NO,created_by_user_id:uuid:NO,id:integer:NO,predecessor_task_id:integer:NO,project_id:integer:NO,successor_task_id:integer:NO"
	if got := columns("task_dependencies"); got != wantDeps {
		t.Errorf("task_dependencies columns = %q, want %q", got, wantDeps)
	}

	// Insert project and two tasks, then add dependency
	var projectID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.projects (code, name, billing_type, status, created_by_user_id, created_at, updated_at)
		VALUES ('TDEP1', 'Dep Project', 'time-and-materials', 'active', $1, now(), now())
		RETURNING id`, uuid.New()).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	var task1ID, task2ID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.tasks (project_id, title, status, position, revision, created_by_user_id, created_at, updated_at)
		VALUES ($1, 'Task A', 'todo', 1, 1, $2, now(), now())
		RETURNING id`, projectID, uuid.New()).Scan(&task1ID); err != nil {
		t.Fatalf("insert task 1: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.tasks (project_id, title, status, position, revision, created_by_user_id, created_at, updated_at)
		VALUES ($1, 'Task B', 'todo', 2, 1, $2, now(), now())
		RETURNING id`, projectID, uuid.New()).Scan(&task2ID); err != nil {
		t.Fatalf("insert task 2: %v", err)
	}

	var depID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.task_dependencies (project_id, predecessor_task_id, successor_task_id, created_by_user_id, created_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING id`, projectID, task1ID, task2ID, uuid.New()).Scan(&depID); err != nil {
		t.Fatalf("insert dependency: %v", err)
	}

	// Prove self-dependency is rejected by check constraint
	if _, err := pool.Exec(ctx, `
		INSERT INTO projects.task_dependencies (project_id, predecessor_task_id, successor_task_id, created_by_user_id, created_at)
		VALUES ($1, $2, $2, $3, now())`, projectID, task1ID, uuid.New()); err == nil {
		t.Errorf("expected self-dependency check failure, got nil")
	}

	// Prove duplicate dependency is rejected by unique constraint
	if _, err := pool.Exec(ctx, `
		INSERT INTO projects.task_dependencies (project_id, predecessor_task_id, successor_task_id, created_by_user_id, created_at)
		VALUES ($1, $2, $3, $4, now())`, projectID, task1ID, task2ID, uuid.New()); err == nil {
		t.Errorf("expected duplicate dependency unique failure, got nil")
	}

	// Prove deleting a task cascades to task_dependencies
	if _, err := pool.Exec(ctx, `DELETE FROM projects.tasks WHERE id = $1`, task1ID); err != nil {
		t.Fatalf("delete task 1: %v", err)
	}

	var remainingCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects.task_dependencies WHERE id = $1`, depID).Scan(&remainingCount); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remainingCount != 0 {
		t.Errorf("remainingCount = %d, want 0 after task deletion", remainingCount)
	}
}
