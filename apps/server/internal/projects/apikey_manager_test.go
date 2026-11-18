package projects_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestPostProjects_AnIntegrationIsNoManager is API access design K56: a
// project an API key creates is created, but its integration is given no
// role on it, so it has no manager until a person is given the role.
func TestPostProjects_AnIntegrationIsNoManager(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key, integration := h.SignInAPIKey(t, modtest.APIKey{}, "projects:access", "projects:create", "projects:view-all")

	created := createProject(t, key, nil)
	if len(created.Managers) != 0 || created.Capabilities.CanManage {
		t.Errorf("managers %+v, canManage %t, want none", created.Managers, created.Capabilities.CanManage)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.project_roles WHERE user_id = $1`, integration); n != 0 {
		t.Errorf("the integration holds %d project roles, want none", n)
	}
}
