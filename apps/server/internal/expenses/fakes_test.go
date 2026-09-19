package expenses_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The fake project directory is what every project-aware test of this module
// will be written against — depguard forbids importing the real one — so what
// it answers has to be what projects answers, and that is worth pinning on its
// own rather than discovering through a test about something else.
func TestFakeProjectDirectory_AnswersWhatProjectsWould(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFakeProjects()
	member, manager, viewer := uuid.New(), uuid.New(), uuid.New()
	f.addRole(projectKraftVerket, member, roleMember)
	f.addRole(projectKraftVerket, manager, roleManager)
	f.addRole(projectKraftVerket, viewer, roleViewer)

	project, err := f.Project(ctx, projectKraftVerket)
	if err != nil || project == nil || project.Code != projectKraftVerketCode || project.Name != projectKraftVerketName {
		t.Fatalf("Project(%d) = %v, %v, want the Kraft-Verket project", projectKraftVerket, project, err)
	}
	if byCode, err := f.ProjectByCode(ctx, projectKraftVerketCode); err != nil || byCode == nil || byCode.ID != projectKraftVerket {
		t.Errorf("ProjectByCode(%q) = %v, %v, want the same project", projectKraftVerketCode, byCode, err)
	}
	if unknown, err := f.Project(ctx, projectUnknown); err != nil || unknown != nil {
		t.Errorf("Project(%d) = %v, %v, want (nil, nil) for an id nobody knows", projectUnknown, unknown, err)
	}

	// A member and a manager may book; a viewer and a completed project may
	// not — projects' own CanLogTime rule, which booking an expense needs
	// (decision X9).
	for _, tc := range []struct {
		name      string
		projectID int32
		userID    uuid.UUID
		want      bool
	}{
		{"a member on an open project", projectKraftVerket, member, true},
		{"a manager on an open project", projectKraftVerket, manager, true},
		{"a viewer on an open project", projectKraftVerket, viewer, false},
		{"a stranger on an open project", projectKraftVerket, uuid.New(), false},
		{"a project nobody knows", projectUnknown, member, false},
		{"a completed project", projectCompleted, member, false},
	} {
		got, err := f.CanLogTime(ctx, tc.projectID, tc.userID)
		if err != nil || got != tc.want {
			t.Errorf("CanLogTime, %s = %v, %v, want %v", tc.name, got, err, tc.want)
		}
	}

	// setCanLogTime overrides the rule, so a test can drive "projects says no"
	// without arranging a status for it.
	f.setCanLogTime(projectKraftVerket, false)
	if got, err := f.CanLogTime(ctx, projectKraftVerket, member); err != nil || got {
		t.Errorf("CanLogTime after the override = %v, %v, want false", got, err)
	}

	if role, err := f.Role(ctx, projectKraftVerket, manager); err != nil || role != roleManager {
		t.Errorf("Role = %q, %v, want %q", role, err, roleManager)
	}
	if role, err := f.Role(ctx, projectInternal, manager); err != nil || role != "" {
		t.Errorf("Role on a project they hold none on = %q, %v, want \"\"", role, err)
	}

	line, err := f.BillingLine(ctx, projectKraftVerket, lineFixed)
	if err != nil || line == nil || line.PricingMode != "fixed" {
		t.Errorf("BillingLine(%d, %d) = %v, %v, want the fixed line", projectKraftVerket, lineFixed, line, err)
	}
	if other, err := f.BillingLine(ctx, projectKraftVerket, lineEuro); err != nil || other != nil {
		t.Errorf("BillingLine of another project's line = %v, %v, want (nil, nil)", other, err)
	}
	lines, err := f.BillingLines(ctx, projectKraftVerket)
	if err != nil || len(lines) != 2 || lines[0].Code != "OLD" || lines[1].Code != "PM" {
		t.Errorf("BillingLines(%d) = %v, %v, want both lines by code", projectKraftVerket, lines, err)
	}

	forUser, err := f.ProjectsForUser(ctx, member)
	if err != nil || len(forUser) != 1 || forUser[0].ID != projectKraftVerket {
		t.Errorf("ProjectsForUser = %v, %v, want the one project they hold a role on", forUser, err)
	}
	several, err := f.Projects(ctx, []int32{projectEuro, projectUnknown, projectInternal})
	if err != nil || len(several) != 2 {
		t.Errorf("Projects = %v, %v, want the two that exist and no hole for the one that does not", several, err)
	}

	// A project projects has lost is simply gone from here on.
	f.removeProject(projectEuro)
	if gone, err := f.Project(ctx, projectEuro); err != nil || gone != nil {
		t.Errorf("Project after removeProject = %v, %v, want (nil, nil)", gone, err)
	}
}

func TestFakeObjectStore_RoundTripsAndForgets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newFakeObjectStore()

	if exists, err := s.Exists(ctx, "receipts/1"); err != nil || exists {
		t.Errorf("Exists before Put = %v, %v, want false", exists, err)
	}
	if _, err := s.Get(ctx, "receipts/1"); err == nil {
		t.Error("Get before Put succeeded, want the not-exist error")
	}
	if err := s.Put(ctx, "receipts/1", strings.NewReader("a receipt"), "application/pdf"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	body, err := s.Get(ctx, "receipts/1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = body.Close() }()
	read, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read the stored object: %v", err)
	}
	if got := string(read); got != "a receipt" {
		t.Errorf("Get = %q, want the bytes Put stored", got)
	}
	if err := s.Delete(ctx, "receipts/1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if exists, err := s.Exists(ctx, "receipts/1"); err != nil || exists {
		t.Errorf("Exists after Delete = %v, %v, want false", exists, err)
	}
	if err := s.Delete(ctx, "receipts/1"); err != nil {
		t.Errorf("Delete of a key that is not there = %v, want nil", err)
	}
}
