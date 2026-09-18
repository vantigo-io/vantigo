package timetracking

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestMount_WithoutAProjectDirectory_Fails proves a composition that
// reaches time's mount without projects fails there, loudly, rather than
// mounting a module whose every write would panic on a nil directory.
// config refuses "time" without "projects" first; this is the second line.
func TestMount_WithoutAProjectDirectory_Fails(t *testing.T) {
	t.Parallel()
	_, err := mount(module.Deps{})
	if err == nil || !strings.Contains(err.Error(), "requires the projects module") {
		t.Errorf("mount without Deps.Projects: err = %v, want it to name the missing projects module", err)
	}
}
